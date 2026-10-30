package suburl

import (
	"encoding/json"
	"testing"

	"github.com/sbgayhub/vray/internal/core/configgen"
)

const clashFixture = `mixed-port: 7890
proxies:
  - {name: Tokyo, type: vless, server: jp.example.com, port: 443, uuid: 11111111-2222-3333-4444-555555555555, tls: true, flow: xtls-rprx-vision, skip-cert-verify: true, servername: cert.example.com}
  - {name: Osaka, type: hysteria2, server: osaka.example.com, port: 8443, password: secret, sni: hy.example.com, skip-cert-verify: true}
proxy-groups:
  - {name: Main, type: select, proxies: [Auto, Tokyo, DIRECT]}
  - {name: Auto, type: url-test, proxies: [Tokyo, Osaka], url: 'https://example.com/check', interval: 300}
rules:
  - MATCH,Main
`

func TestClashPreservesNodesGroupsAndTLS(t *testing.T) {
	result := ParseText(clashFixture)
	if len(result.Profiles) != 2 || len(result.Groups) != 2 || len(result.Diagnostics) != 0 {
		t.Fatalf("unexpected parse: nodes=%d groups=%d diagnostics=%v", len(result.Profiles), len(result.Groups), result.Diagnostics)
	}
	var node configgen.Outbound
	_ = json.Unmarshal(result.Profiles[0].RawOutbound, &node)
	if node.Security != "tls" || node.SNI != "cert.example.com" || node.Flow != "xtls-rprx-vision" || !node.AllowInsecure {
		t.Fatal("TLS or Vision settings were lost")
	}
	if result.Groups[0].Members[0] != "Auto" || result.Groups[1].Interval != 300 {
		t.Fatal("nested groups or interval were lost")
	}
}

func TestInsecureAliasIsPreservedForCertificateMigration(t *testing.T) {
	for _, scheme := range []string{"vless", "trojan", "hysteria2"} {
		profile, err := parseLinkForTest(scheme + "://11111111-2222-3333-4444-555555555555@node.example.com:443?security=tls&insecure=1")
		if err != nil {
			t.Fatal(err)
		}
		var node configgen.Outbound
		_ = json.Unmarshal(profile.RawOutbound, &node)
		if !node.AllowInsecure {
			t.Fatalf("%s lost insecure flag", scheme)
		}
	}
}
