package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	xlog "github.com/xtls/xray-core/common/log"
	xray "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"

	"github.com/sbgayhub/vray/internal/core/configgen"
)

func localInspectionNode(t *testing.T, handler http.Handler) configgen.Outbound {
	t.Helper()
	api := httptest.NewServer(handler)
	t.Cleanup(api.Close)
	apiURL, _ := url.Parse(api.URL)
	port := freePort(t)
	configuration, _ := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": port, "protocol": "shadowsocks",
			"settings": map[string]any{"method": "aes-128-gcm", "password": "inspection-test-password", "network": "tcp"},
		}},
		"outbounds": []any{map[string]any{
			"protocol": "freedom", "settings": map[string]any{
				"redirect": apiURL.Host, "finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
			},
		}},
	})
	server, err := xray.StartInstance("json", configuration)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return configgen.Outbound{Protocol: "shadowsocks", Address: "127.0.0.1", Port: port, Method: "aes-128-gcm", Secret: "inspection-test-password"}
}

type inspectionLogSpy struct{ markers atomic.Int32 }

func (s *inspectionLogSpy) Handle(message xlog.Message) {
	if strings.Contains(message.String(), "inspection-log-check") {
		s.markers.Add(1)
	}
}

func TestNodeHTTPClientUsesRealSelectedProxyAndPreservesEngine(t *testing.T) {
	// 该目标地址没有本地 DNS 记录。只有上面的真实 Shadowsocks 服务器
	// 才能通过重定向远端目标访问测试 API。
	const destination = "http://exit-probe.invalid/metadata"
	for _, connected := range []bool{false, true} {
		name := "idle"
		if connected {
			name = "connected-with-blocking-user-rules"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			node := localInspectionNode(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, `{"ip":"8.8.4.4"}`)
			}))
			e := New()
			defer e.Stop()
			if connected {
				if err := e.Start([]byte(`{"log":{"loglevel":"none"},"outbounds":[{"tag":"blocked","protocol":"blackhole"}],"routing":{"rules":[{"type":"field","network":"tcp","outboundTag":"blocked"}]}}`)); err != nil {
					t.Fatal(err)
				}
			}
			originalInstance := e.inst
			spy := &inspectionLogSpy{}
			xlog.RegisterHandler(spy)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, cleanup, err := e.NodeHTTPClient(ctx, node)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if client.Transport.(*http.Transport).Proxy != nil {
				t.Fatal("inspection client consults environment proxy")
			}
			response, err := client.Get(destination)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(raw) != `{"ip":"8.8.4.4"}` || requests.Load() != 1 {
				t.Fatalf("proxy request: body=%q requests=%d error=%v", raw, requests.Load(), err)
			}
			cleanup()
			if e.inst != originalInstance || e.Running() != connected {
				t.Fatal("inspection changed the main engine or its running state")
			}
			if connected {
				manager := e.inst.GetFeature(outbound.ManagerType()).(outbound.Manager)
				if manager.GetDefaultHandler().Tag() != "blocked" || len(manager.ListHandlers(context.Background())) != 1 {
					t.Fatal("inspection replaced the default route or left an outbound behind")
				}
			}
			xlog.Record(&xlog.GeneralMessage{Content: "inspection-log-check"})
			if spy.markers.Load() != 1 {
				t.Fatal("inspection replaced the process log handler")
			}
		})
	}
}

func TestNodeHTTPClientCancellationClosesProxyRequest(t *testing.T) {
	arrived, cancelled := make(chan struct{}), make(chan struct{})
	node := localInspectionNode(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-r.Context().Done()
		close(cancelled)
	}))
	e := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, cleanup, err := e.NodeHTTPClient(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	done := make(chan error, 1)
	go func() {
		response, err := client.Get("http://exit-probe.invalid/wait")
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached the selected proxy's API")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not interrupt the HTTP connection")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not release the remote proxy request")
	}
}
