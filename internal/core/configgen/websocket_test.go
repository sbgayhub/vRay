package configgen

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/xtls/xray-core/core"
)

func TestWebSocketUsesIndependentHost(t *testing.T) {
	for _, host := range []string{"cdn.example.com", ""} {
		node := Outbound{Protocol: "vless", Address: "server.example.com", Port: 443,
			Secret: "11111111-2222-3333-4444-555555555555", Network: "ws",
			Host: host, Path: "/proxy?ed=2048", Security: "tls", SNI: "tls.example.com"}
		in := systemProxyInput()
		in.Active = node
		main, _, err := Build(in)
		if err != nil {
			t.Fatal(err)
		}
		inspection, err := BuildNodeDialer(node)
		if err != nil {
			t.Fatal(err)
		}
		for name, raw := range map[string][]byte{"main": main, "inspection": inspection} {
			var cfg struct {
				Outbounds []struct {
					Stream struct {
						Network string                     `json:"network"`
						WS      map[string]json.RawMessage `json:"wsSettings"`
						TLS     struct {
							ServerName string `json:"serverName"`
						} `json:"tlsSettings"`
					} `json:"streamSettings"`
				} `json:"outbounds"`
			}
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			stream := cfg.Outbounds[0].Stream
			var gotHost, gotPath string
			if value := stream.WS["host"]; value != nil {
				if err := json.Unmarshal(value, &gotHost); err != nil {
					t.Fatal(err)
				}
			}
			if err := json.Unmarshal(stream.WS["path"], &gotPath); err != nil {
				t.Fatal(err)
			}
			if gotHost != host || gotPath != node.Path || stream.Network != "ws" || stream.TLS.ServerName != node.SNI {
				t.Fatalf("%s: websocket identity changed: host=%q path=%q network=%q sni=%q", name, gotHost, gotPath, stream.Network, stream.TLS.ServerName)
			}
			if _, exists := stream.WS["headers"]; exists {
				t.Fatalf("%s: deprecated headers emitted", name)
			}
			if _, err := core.LoadConfig("json", bytes.NewReader(raw)); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
}
