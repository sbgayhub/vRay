package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/core/configgen"
)

func TestIPLookupFallbackKeepsForcedNodeAndPersists(t *testing.T) {
	t.Setenv("VRAY_IPAPI_KEY", "private-primary-key")
	svc, _ := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	var clients, requests, cleanups int
	svc.nodeClient = func(_ context.Context, node configgen.Outbound) (*http.Client, func(), error) {
		clients++
		if node.Address != testProfile().Address {
			t.Fatal("lookup did not use the selected node")
		}
		return &http.Client{Transport: sourceRoundTrip(func(r *http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				if r.URL.Host != "api.ipapi.is" || r.Method != http.MethodPost {
					t.Fatal("wrong primary request")
				}
				return nil, io.EOF
			}
			if r.URL.Host != "ipwho.is" || r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Body != nil {
				t.Fatal("fallback must use the same transport without sharing the primary API key")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ip":"8.8.4.4","success":true,"country":"Example","region":"Region","city":"City","connection":{"asn":15169,"org":"Example AS","isp":"Example ISP"}}`))}, nil
		})}, func() { cleanups++ }, nil
	}
	awaitOperation(t, svc.InspectNode("p1"))
	info := svc.State().Snapshot().NodeDetails["p1"]
	if clients != 1 || requests != 2 || cleanups != 1 || info.Error != "" || info.IP != "8.8.4.4" || info.Provider != "Example ISP" || info.ASN != "AS15169 · Example AS" || info.Score != "" || info.Type != "" {
		t.Fatalf("invalid fallback result: clients=%d requests=%d cleanups=%d info=%+v", clients, requests, cleanups, info)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if saved := svc.State().Snapshot().NodeDetails["p1"]; saved.IP != info.IP || saved.ASN != info.ASN || saved.CheckedAt.IsZero() {
		t.Fatal("fallback information was not persisted")
	}
	awaitOperation(t, svc.InspectSelectedNode("p1"))
	if requests != 2 {
		t.Fatal("cached IP information was queried again")
	}
}

func TestIPFallbackRejectsProviderFailure(t *testing.T) {
	if _, err := decodeIPDetails([]byte(`{"success":false,"ip":"8.8.8.8"}`), IPDetails{}); err == nil {
		t.Fatal("provider failure must not become a saved observation")
	}
}

func useIPTestServer(svc *Service, server *httptest.Server) {
	svc.ipEndpoint = server.URL
	svc.nodeClient = func(context.Context, configgen.Outbound) (*http.Client, func(), error) {
		return server.Client(), func() {}, nil
	}
}

func TestIPLookupUsesProviderFieldsAndKeepsKeySecret(t *testing.T) {
	t.Setenv("VRAY_IPAPI_KEY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var query map[string]string
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			t.Error(err)
		}
		if _, hasEntry := query["q"]; hasEntry || query["key"] != "test-private-key" {
			t.Errorf("wrong IP query: %+v", query)
		}
		_, _ = w.Write([]byte(`{"ip":"8.8.4.4","company":{"name":"Example provider","type":"business","abuser_score":"0.01 (Low)"},"asn":{"asn":15169,"org":"Example AS"},"location":{"country":"United States","state":"California","city":"Example city"},"is_datacenter":true}`))
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	p := testProfile()
	p.Address = "8.8.8.8"
	if err := svc.AddTestProfile(p, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	useIPTestServer(svc, server)
	awaitOperation(t, svc.SaveIPLookupKey("test-private-key"))
	awaitOperation(t, svc.InspectNode(p.ID))
	snap := svc.State().Snapshot()
	info := snap.NodeDetails[p.ID]
	if info.Error != "" || info.IP != "8.8.4.4" || info.Score != "0.01 (Low)" || info.Type != "数据中心" || !strings.Contains(info.ASN, "15169") {
		t.Fatalf("IP details = %+v", info)
	}
	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "test-private-key") {
		t.Fatal("IP API key leaked into config")
	}
}

func TestSelectionLoadsAndCachesIPDetails(t *testing.T) {
	t.Setenv("VRAY_IPAPI_KEY", "")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"ip":"8.8.8.8","company":"Example provider","asn":"AS15169 Example","country":"United States"}`))
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	p := testProfile()
	p.Address = "8.8.8.8"
	if err := svc.AddTestProfile(p, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	svc.noResolve = false
	useIPTestServer(svc, server)
	awaitOperation(t, svc.InspectSelectedNode(p.ID))
	awaitOperation(t, svc.InspectSelectedNode(p.ID))
	if requests.Load() != 1 || svc.State().Snapshot().NodeDetails[p.ID].Country != "United States" {
		t.Fatal("selection lookup or cache failed")
	}
}

func TestIPProviderLimitBacksOffAcrossNodes(t *testing.T) {
	t.Setenv("VRAY_IPAPI_KEY", "")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	for i, address := range []string{"8.8.8.8", "8.8.4.4"} {
		p := testProfile()
		p.ID, p.Address = address, address
		p.Name = string(rune('a' + i))
		if err := svc.AddTestProfile(p, "secret"); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	useIPTestServer(svc, server)
	awaitOperation(t, svc.InspectNode("8.8.8.8"))
	awaitOperation(t, svc.InspectNode("8.8.4.4"))
	if requests.Load() != 1 || !strings.Contains(svc.State().Snapshot().NodeDetails["8.8.4.4"].Error, "限额") {
		t.Fatal("IP source limit was not respected")
	}
}

func TestAnonymousIPLookupDoesNotInventRiskOrType(t *testing.T) {
	info, err := decodeIPDetails([]byte(`{"ip":"8.8.8.8","company":"Example provider","asn":"AS15169 Example","country":"United States","region":"California","city":"Example"}`), IPDetails{IP: "8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Provider != "Example provider" || info.Country != "United States" || info.Score != "" || info.Type != "" {
		t.Fatalf("anonymous details = %+v", info)
	}
}

func TestPrivateExitIsRejectedWithoutFallingBackToServer(t *testing.T) {
	svc, _ := newTestService(t)
	p := testProfile()
	p.Address = "127.0.0.1"
	if err := svc.AddTestProfile(p, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ip":"127.0.0.1"}`))
	}))
	defer server.Close()
	useIPTestServer(svc, server)
	awaitOperation(t, svc.InspectNode(p.ID))
	if info := svc.State().Snapshot().NodeDetails[p.ID]; !strings.Contains(info.Error, "公网出口") || info.IP != "" {
		t.Fatalf("private address result=%+v", info)
	}
}

func TestExitRecordsArePerNodeAndRetainedUntilManualRefresh(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := "8.8.8.8"
		if requests.Add(1) > 1 {
			ip = "8.8.4.4"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"ip": ip})
	}))
	defer server.Close()
	svc, _ := newTestService(t)
	for index, id := range []string{"first", "second"} {
		p := testProfile()
		p.ID, p.Address, p.Port = id, "127.0.0.1", 10000+index
		if err := svc.AddTestProfile(p, "secret"); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	useIPTestServer(svc, server)
	svc.noResolve = false
	t.Cleanup(svc.CancelInspection)
	awaitOperation(t, svc.InspectSelectedNode("first"))
	awaitOperation(t, svc.InspectSelectedNode("second"))
	awaitOperation(t, svc.InspectSelectedNode("first"))
	snap := svc.State().Snapshot()
	if requests.Load() != 2 || snap.NodeDetails["first"].IP != "8.8.8.8" || snap.NodeDetails["second"].IP != "8.8.4.4" {
		t.Fatalf("nodes sharing an entry reused exit data: requests=%d details=%+v", requests.Load(), snap.NodeDetails)
	}
	d, _ := svc.store.Load()
	p := d.Profiles[0]
	p.Port++
	if err := svc.store.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if svc.State().Snapshot().NodeDetails[p.ID].IP != snap.NodeDetails[p.ID].IP {
		t.Fatal("editing a node discarded its saved exit")
	}
	awaitOperation(t, svc.InspectSelectedNode(p.ID))
	if requests.Load() != 2 {
		t.Fatal("editing a node triggered an automatic exit lookup")
	}
	// 即使记录很旧也不会过期；只有显式刷新才会替换它们。
	d, _ = svc.store.Load()
	saved := d.NodeDetails[p.ID]
	saved.CheckedAt = time.Now().Add(-365 * 24 * time.Hour).UTC()
	d.NodeDetails[p.ID] = saved
	if err := svc.store.Save(d); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	awaitOperation(t, svc.InspectSelectedNode(p.ID))
	if requests.Load() != 2 {
		t.Fatal("old persisted record caused an automatic lookup")
	}
	awaitOperation(t, svc.InspectNode(p.ID))
	if requests.Load() != 3 {
		t.Fatal("manual refresh did not send a new observation")
	}
	d, _ = svc.store.Load()
	if d.NodeDetails[p.ID].IP != "8.8.4.4" || !d.NodeDetails[p.ID].CheckedAt.After(saved.CheckedAt) {
		t.Fatal("manual refresh did not replace the durable observation")
	}
}

func TestExitLookupFailureNeverUsesEntryAddress(t *testing.T) {
	svc, engine := newTestService(t)
	p := testProfile()
	p.Address = "8.8.8.8"
	if err := svc.AddTestProfile(p, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	svc.nodeClient = func(context.Context, configgen.Outbound) (*http.Client, func(), error) {
		return nil, nil, errors.New("secret must not appear in state")
	}
	awaitOperation(t, svc.InspectNode(p.ID))
	info := svc.State().Snapshot().NodeDetails[p.ID]
	starts, stops := engine.Counts()
	if info.IP != "" || info.Error == "" || strings.Contains(info.Error, "secret") || starts != 0 || stops != 0 {
		t.Fatalf("failed inspection: %+v; engine starts/stops=%d/%d", info, starts, stops)
	}
}

func TestInvalidExitResponsesAreNotAccepted(t *testing.T) {
	for _, ip := range []string{"", "not-an-ip", "10.0.0.1", "::1", "fe80::1", "224.0.0.1"} {
		raw, _ := json.Marshal(map[string]string{"ip": ip})
		if info, err := decodeIPDetails(raw, IPDetails{}); err == nil || info.IP != "" {
			t.Fatalf("invalid exit %q accepted: %+v %v", ip, info, err)
		}
	}
	if info, err := decodeIPDetails([]byte(`{"ip":"2606:4700:4700::1111"}`), IPDetails{}); err != nil || info.IP != "2606:4700:4700::1111" {
		t.Fatalf("public IPv6 exit rejected: %+v %v", info, err)
	}
}
