package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/core/configgen"
	xray "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/stats"
)

func policyRelay(t *testing.T, name string, healthy *atomic.Bool) configgen.Outbound {
	t.Helper()
	stop := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		_, _ = w.Write([]byte(name))
		if r.URL.Path == "/hold" {
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-stop:
			}
		}
	}))
	t.Cleanup(api.Close)
	t.Cleanup(func() { close(stop) })
	target, _ := url.Parse(api.URL)
	port := freePort(t)
	const id = "11111111-2222-3333-4444-555555555555"
	raw, _ := json.Marshal(map[string]any{
		"log":       map[string]string{"loglevel": "none"},
		"inbounds":  []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "vless", "settings": map[string]any{"clients": []any{map[string]any{"id": id}}, "decryption": "none"}}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"redirect": target.Host, "finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}},
	})
	server, err := xray.StartInstance("json", raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return configgen.Outbound{Name: name, Protocol: "vless", Address: "127.0.0.1", Port: port, Secret: id}
}

func TestPolicyExitsReportConcurrentLoadBalancedNodes(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	a, b := policyRelay(t, "first", &healthy), policyRelay(t, "second", &healthy)
	port := freePort(t)
	in := configgen.Input{
		Active: a, Mode: configgen.ModeSystemProxy, ActivePolicy: "g",
		Settings:     configgen.Settings{MixedPort: port, LogLevel: "none", ProbeURL: "http://policy-probe.invalid/", ProbeInterval: "1s"},
		PolicyNodes:  []configgen.PolicyNode{{ID: "a", Outbound: a}, {ID: "b", Outbound: b}},
		PolicyGroups: []configgen.PolicyGroup{{ID: "g", Type: "load-balance", Members: []string{"a", "b"}}},
	}
	raw, _, err := configgen.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	e := New()
	if err := e.Start(raw); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	proxy, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	for range 2 {
		response, err := client.Get("http://policy-target.invalid/hold")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
	}
	exits := e.PolicyExits()
	if exits.Source != "active" || exits.GroupID != "g" || len(exits.Nodes) != 2 {
		t.Fatalf("concurrent actual nodes were collapsed into one: %+v", exits)
	}
	for _, node := range exits.Nodes {
		if node.Connections != 1 {
			t.Fatal("active connection counts were not exposed")
		}
	}
}

func TestPoliciesUseRealMembersAndPreserveFallbackPriority(t *testing.T) {
	var first, second atomic.Bool
	first.Store(true)
	second.Store(true)
	a, b := policyRelay(t, "first", &first), policyRelay(t, "second", &second)
	for _, kind := range []string{"select", "load-balance", "url-test", "fallback"} {
		t.Run(kind, func(t *testing.T) {
			first.Store(true)
			port := freePort(t)
			in := configgen.Input{Active: a, Mode: configgen.ModeSystemProxy, ActivePolicy: "g",
				Settings:     configgen.Settings{MixedPort: port, LogLevel: "none", ProbeURL: "http://policy-probe.invalid/", ProbeInterval: "200ms"},
				PolicyNodes:  []configgen.PolicyNode{{ID: "a", Outbound: a}, {ID: "b", Outbound: b}},
				PolicyGroups: []configgen.PolicyGroup{{ID: "g", Name: "group", Type: kind, Selected: "b", Members: []string{"a", "b"}}},
			}
			in.Rules = []configgen.Rule{{ID: "explicit-proxy", Domains: []string{"full:policy-target.invalid"}, Outbound: configgen.TagProxy}}
			raw, _, err := configgen.Build(in)
			if err != nil {
				t.Fatal(err)
			}
			e := New()
			if err := e.Start(raw); err != nil {
				t.Fatal(err)
			}
			defer e.Stop()
			proxy, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
			transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
			get := func() string {
				resp, err := client.Get("http://policy-target.invalid/")
				if err != nil {
					if kind == "fallback" && !first.Load() {
						return ""
					}
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				return string(body)
			}
			if kind == "select" {
				if exits := e.PolicyExits(); len(exits.Nodes) != 1 || exits.Nodes[0].ID != "b" {
					t.Fatal("manual policy did not expose its selected node")
				}
				if got := get(); got != "second" {
					t.Fatalf("manual group used %q", got)
				}
				return
			}
			if kind == "load-balance" {
				counts := map[string]int{}
				for range 4 {
					name := get()
					counts[name]++
					deadline := time.Now().Add(time.Second)
					for e.ConnectionCount() != 0 && time.Now().Before(deadline) {
						time.Sleep(time.Millisecond)
					}
					exits := e.PolicyExits()
					if len(exits.Nodes) != 1 || exits.Nodes[0].Name != name || exits.Source != "recent" {
						t.Fatalf("load balance did not expose the actual last node: %+v", exits)
					}
				}
				if counts["first"] != 2 || counts["second"] != 2 {
					t.Fatalf("connections were not balanced: %v", counts)
				}
				manager := e.inst.GetFeature(stats.ManagerType()).(stats.Manager)
				for _, id := range []string{"a", "b"} {
					counter := manager.GetCounter(configgen.Name(configgen.ScopeOutbound, configgen.PolicyNodePrefix+id, configgen.DirDownlink))
					if counter == nil || counter.Value() <= 0 {
						t.Fatalf("actual member %s has no traffic accounting", id)
					}
				}
				return
			}
			if kind == "url-test" {
				if got := get(); got != "first" && got != "second" {
					t.Fatalf("invalid automatic member %q", got)
				}
				return
			}
			if got := get(); got != "first" {
				t.Fatalf("fallback ignored priority: %s", got)
			}
			first.Store(false)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if got := get(); got == "second" {
					health := e.PolicyHealth()
					if len(health) != 2 || !health["b"].Alive {
						continue
					}
					if health["a"].Alive || health["a"].CheckedAt.IsZero() {
						t.Fatal("policy health did not reflect the real failed member")
					}
					exits := e.PolicyExits()
					found := false
					for _, node := range exits.Nodes {
						if node.ID == "b" {
							found = true
						}
					}
					if !found {
						t.Fatal("fallback exit metadata did not follow the working node")
					}
					delete(health, "b")
					if !e.PolicyHealth()["b"].Alive {
						t.Fatal("health snapshot mutated the observer")
					}
					if err := e.Stop(); err != nil {
						t.Fatal(err)
					}
					if len(e.PolicyHealth()) != 0 {
						t.Fatal("stopped engine kept stale policy health")
					}
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatal("fallback did not move to the healthy member")
		})
	}
}
