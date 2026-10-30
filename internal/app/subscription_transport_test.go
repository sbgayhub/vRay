package app

import (
	"encoding/base64"
	"net/http"
	"testing"
)

// 通过真实 HTTP 提供订阅，其中节点带有不在身份字段中的传输配置：这是粘贴 https 链接所走的完整路径，最终得到仍可连接的节点。
func TestRealSubscriptionKeepsTransport(t *testing.T) {
	links := "vless://11111111-2222-3333-4444-555555555555@a.example.com:443?security=tls&type=ws&path=%2Fws#Tokyo\n" +
		"hysteria2://pw@b.example.com:8443?sni=b.example.com#Osaka\n"
	body := base64.StdEncoding.EncodeToString([]byte(links))

	srv := subscriptionServer(t, body, http.Header{
		"Subscription-Userinfo": {"upload=1024; download=2048; total=1048576"},
		"Profile-Title":         {"base64:" + base64.StdEncoding.EncodeToString([]byte("My Panel"))},
	})

	svc, _ := newTestService(t)
	before := svc.State().Snapshot().Version
	svc.Import(ImportInput{Text: srv.URL})
	snap := waitImport(t, svc, before)

	if snap.ImportAdded != 2 {
		t.Fatalf("imported %d nodes, want 2", snap.ImportAdded)
	}
	if len(snap.Sources) != 1 {
		t.Fatalf("sources = %d, want 1", len(snap.Sources))
	}
	if got := snap.Sources[0].NodeCount; got != 2 {
		t.Fatalf("source node count = %d, want 2", got)
	}
	if snap.Sources[0].Name != subscriptionName(srv.URL) {
		t.Fatalf("source name = %q, want %q", snap.Sources[0].Name, subscriptionName(srv.URL))
	}

	sawHysteria, sawWS := false, false
	for _, p := range snap.Profiles {
		switch p.Protocol {
		case "hysteria2":
			sawHysteria = true
		case "vless":
			stored, ok := svc.Profile(p.ID)
			if !ok {
				t.Fatalf("profile %s not readable back", p.ID)
			}
			if len(stored.RawOutbound) == 0 {
				t.Fatal("the ws transport was dropped on import")
			}
			sawWS = true
		}
	}
	if !sawHysteria || !sawWS {
		t.Fatalf("hysteria2=%v vless=%v, want both", sawHysteria, sawWS)
	}
}
