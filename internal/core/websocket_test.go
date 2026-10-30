package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	xray "github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/proxy/vless/inbound"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/core/logs"
)

// 服务器会检查 WebSocket Host，因此请求被接受证明新的
// wsSettings.host 仍能到达线上。保留的目标地址只能通过
// VLESS/WebSocket 中继的重定向访问 HTTP fixture。
func TestWebSocketIndependentHostReachesServer(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 让 fixture 套接字保持存活，直到客户端消费已知的完整响应体。
		// 立即关闭小响应可能使 Xray 的 WebSocket 关闭帧与最后转发的
		// 载荷发生竞态。
		w.Header().Set("Content-Length", "17")
		_, _ = io.WriteString(w, "websocket-relayed")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer api.Close()
	apiURL, _ := url.Parse(api.URL)
	port := freePort(t)
	const id = "11111111-2222-3333-4444-555555555555"
	const host = "cdn.example.com"
	cfg, err := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": port, "protocol": "vless",
			"settings":       map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": id}}},
			"streamSettings": map[string]any{"network": "ws", "wsSettings": map[string]any{"path": "/proxy", "host": host}},
		}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{
			"redirect": apiURL.Host, "finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := xray.StartInstance("json", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	e := New()
	defer e.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, cleanup, err := e.NodeHTTPClient(ctx, configgen.Outbound{
		Protocol: "vless", Address: "127.0.0.1", Port: port, Secret: id,
		Network: "ws", Host: host, Path: "/proxy",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	trace, err := logs.NewCollector(100, "")
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	trace.SetLevel(logs.LevelDebug)
	trace.Attach()
	resp, err := client.Get("http://websocket-fixture.invalid/data")
	if err != nil {
		_ = trace.Close()
		for _, entry := range trace.Snapshot() {
			t.Log(entry.Text)
		}
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != "websocket-relayed" {
		t.Fatalf("response status=%d body=%q err=%v", resp.StatusCode, body, err)
	}
}
