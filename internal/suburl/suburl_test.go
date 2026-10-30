package suburl

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/store"
)

// parseLinkForTest 把解析诊断转换成 error，便于测试直接断言。
func parseLinkForTest(raw string) (store.Profile, error) {
	p, diag := parseLink(strings.TrimSpace(raw))
	if diag != nil {
		return store.Profile{}, fmt.Errorf("%s: %s", diag.Field, diag.Message)
	}
	return p, nil
}

// 每种解析器都必须保留链接描述的传输层和安全配置。
//
// 否则导入时只会保留名称、地址、端口和密钥：基于 WebSocket 的 REALITY
// 节点会悄悄变成普通 TCP，连接失败时看起来像服务器故障。生成器只能根据
// 出站 JSON 中记录的内容构建配置，因此链接中的细节必须在导入后保留。
func TestImportKeepsTransportAndSecurity(t *testing.T) {
	cases := []struct {
		name string
		link string
		want configgen.Outbound
	}{
		{
			name: "vless reality ws",
			link: "vless://11111111-2222-3333-4444-555555555555@jp.example.com:443" +
				"?type=ws&path=%2Fws&host=cdn.example.com&security=reality" +
				"&pbk=PUBKEY&sid=abcd&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#A",
			want: configgen.Outbound{
				Name: "A", Protocol: "vless", Address: "jp.example.com", Port: 443,
				Secret:  "11111111-2222-3333-4444-555555555555",
				Network: "ws", Path: "/ws", Host: "cdn.example.com",
				Security: "reality", SNI: "www.microsoft.com", Fingerprint: "chrome",
				PublicKey: "PUBKEY", ShortID: "abcd", Flow: "xtls-rprx-vision",
			},
		},
		{
			name: "trojan tls grpc",
			link: "trojan://pass@tr.example.com:8443" +
				"?type=grpc&serviceName=mygrpc&security=tls&sni=tr.example.com&fp=firefox#B",
			want: configgen.Outbound{
				Name: "B", Protocol: "trojan", Address: "tr.example.com", Port: 8443,
				Secret: "pass", Network: "grpc", Service: "mygrpc",
				Security: "tls", SNI: "tr.example.com", Fingerprint: "firefox",
			},
		},
		{
			name: "hysteria2 tls",
			link: "hy2://auth@hy.example.com:443?security=tls&sni=hy.example.com&insecure=0#C",
			want: configgen.Outbound{
				Name: "C", Protocol: "hysteria2", Address: "hy.example.com", Port: 443,
				Secret: "auth", Security: "tls", SNI: "hy.example.com",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := parseLinkForTest(tc.link)
			if err != nil {
				t.Fatalf("ParseLink: %v", err)
			}
			if len(p.RawOutbound) == 0 {
				t.Fatal("RawOutbound is empty: the transport is lost on import")
			}
			var got configgen.Outbound
			if err := json.Unmarshal(p.RawOutbound, &got); err != nil {
				t.Fatalf("RawOutbound is not an outbound: %v", err)
			}
			if got != tc.want {
				t.Fatalf("outbound = %+v\nwant        %+v", got, tc.want)
			}
		})
	}
}

// vmess 链接通过 JSON 字段而不是查询字符串携带传输配置。
func TestImportVMessKeepsTransport(t *testing.T) {
	body := `{"ps":"D","add":"vm.example.com","port":"443","id":"11111111-2222-3333-4444-555555555555",` +
		`"net":"ws","path":"/vm","host":"cdn.example.com","tls":"tls","sni":"vm.example.com",` +
		`"alpn":["h2","http/1.1"],"fp":"chrome","scy":"auto"}`
	link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(body))

	p, err := parseLinkForTest(link)
	if err != nil {
		t.Fatalf("ParseLink: %v", err)
	}
	if len(p.RawOutbound) == 0 {
		t.Fatal("RawOutbound is empty: the vmess transport is lost on import")
	}
	var got configgen.Outbound
	if err := json.Unmarshal(p.RawOutbound, &got); err != nil {
		t.Fatalf("RawOutbound is not an outbound: %v", err)
	}
	want := configgen.Outbound{
		Name: "D", Protocol: "vmess", Address: "vm.example.com", Port: 443,
		Secret:  "11111111-2222-3333-4444-555555555555",
		Network: "ws", Path: "/vm", Host: "cdn.example.com",
		Security: "tls", SNI: "vm.example.com", Fingerprint: "chrome",
		ALPN: "h2,http/1.1",
	}
	if got != want {
		t.Fatalf("outbound = %+v\nwant        %+v", got, want)
	}
}

// 普通链接仍会生成出站配置，使生成器只需处理一种结构。
func TestImportPlainLinkStillHasOutbound(t *testing.T) {
	for _, link := range []string{
		"vless://uuid@plain.example.com:80?security=none#P",
		"trojan://pass@plain.example.com:80#P",
		"hy2://auth@plain.example.com:80#P",
	} {
		p, err := parseLinkForTest(link)
		if err != nil {
			t.Fatalf("ParseLink(%q): %v", link, err)
		}
		var got configgen.Outbound
		if err := json.Unmarshal(p.RawOutbound, &got); err != nil {
			t.Fatalf("ParseLink(%q) RawOutbound: %v", link, err)
		}
		if got.Security != "" || got.Network != "" {
			t.Fatalf("ParseLink(%q) invented transport/security: %+v", link, got)
		}
	}
}

// 带 Vision 的 REALITY VLESS 链接可解析，并保留名称和 uuid。
func TestParseVLESSReality(t *testing.T) {
	link := "vless://11111111-2222-3333-4444-555555555555@jp.example.com:443" +
		"?type=tcp&security=reality&pbk=PUBKEY&sid=abcd&sni=www.microsoft.com" +
		"&fp=chrome&flow=xtls-rprx-vision#%E4%B8%9C%E4%BA%AC"

	p, err := parseLinkForTest(link)
	if err != nil {
		t.Fatalf("ParseLink: %v", err)
	}
	if p.Protocol != store.ProtoVLESS {
		t.Fatalf("protocol = %s", p.Protocol)
	}
	if p.Address != "jp.example.com" || p.Port != 443 {
		t.Fatalf("address = %s:%d", p.Address, p.Port)
	}
	if p.SecretRef != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("uuid = %q", p.SecretRef)
	}
	if p.Name != "东京" {
		t.Fatalf("name = %q, want 东京 (percent-decoded)", p.Name)
	}
}

// 缺少公钥的 REALITY 会被拒绝，并返回字段级诊断。
func TestParseVLESSRealityNeedsPublicKey(t *testing.T) {
	link := "vless://uuid@host.example.com:443?security=reality&sni=a.com"
	_, err := parseLinkForTest(link)
	if err == nil {
		t.Fatal("expected an error for REALITY without pbk")
	}
	if !strings.Contains(err.Error(), "pbk") {
		t.Fatalf("err = %v, want it to name pbk", err)
	}
}

// 缺少地址或端口时，会在对应字段上报告。
func TestParseLinkMissingParts(t *testing.T) {
	cases := map[string]string{
		"vless://uuid@:443":     "address",
		"vless://uuid@host.com": "port",
		"vless://uuid@host:0":   "port",
	}
	for link, field := range cases {
		_, err := parseLinkForTest(link)
		if err == nil {
			t.Fatalf("%s: expected an error", link)
		}
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("%s: err = %v, want it to name %s", link, err, field)
		}
	}
}

// v2rayN 风格的 vmess 链接可从 base64 JSON 解析，端口可为数字或字符串。
func TestParseVMess(t *testing.T) {
	for _, port := range []string{`443`, `"443"`} {
		payload := `{"ps":"tokyo","add":"jp.example.com","port":` + port + `,"id":"uuid-1"}`
		link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(payload))
		p, err := parseLinkForTest(link)
		if err != nil {
			t.Fatalf("port %s: ParseLink: %v", port, err)
		}
		if p.Protocol != store.ProtoVMess || p.Port != 443 || p.Name != "tokyo" {
			t.Fatalf("port %s: got %+v", port, p)
		}
	}
}

// Trojan 会保留密码。
func TestParseTrojan(t *testing.T) {
	p, err := parseLinkForTest("trojan://secret@sg.example.com:8443?sni=sg.example.com#Singapore")
	if err != nil {
		t.Fatalf("ParseLink: %v", err)
	}
	if p.Protocol != store.ProtoTrojan || p.SecretRef != "secret" || p.Name != "Singapore" {
		t.Fatalf("got %+v", p)
	}
}

// ss 链接的 SIP002 形式和旧版 base64 形式都能解析。
func TestParseShadowsocksForms(t *testing.T) {
	userinfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pass"))

	sip002 := "ss://" + userinfo + "@hk.example.com:8388#HK"
	legacy := "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:pass@hk.example.com:8388")) + "#HK"

	for name, link := range map[string]string{"sip002": sip002, "legacy": legacy} {
		p, err := parseLinkForTest(link)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.Protocol != store.ProtoShadowsocks || p.Port != 8388 || p.Address != "hk.example.com" {
			t.Fatalf("%s: got %+v", name, p)
		}
		if !strings.HasPrefix(p.SecretRef, "aes-256-gcm:") {
			t.Fatalf("%s: method/pass = %q", name, p.SecretRef)
		}
	}
}

// Hysteria2 从 URL 的 userinfo 中读取认证信息。
func TestParseHysteria2(t *testing.T) {
	p, err := parseLinkForTest("hysteria2://auth-token@us.example.com:443?sni=us.example.com#US")
	if err != nil {
		t.Fatalf("ParseLink: %v", err)
	}
	if p.Protocol != store.ProtoHysteria2 || p.SecretRef != "auth-token" {
		t.Fatalf("got %+v", p)
	}
}

// 不支持的 scheme 会按名称拒绝。
func TestParseUnsupportedScheme(t *testing.T) {
	_, err := parseLinkForTest("wireguard://abc@host:1")
	if err == nil || !strings.Contains(err.Error(), "wireguard") {
		t.Fatalf("err = %v, want it to name the scheme", err)
	}
}

// 批量解析会保留有效行，并用从 1 开始的编号报告无效行。
func TestParseTextKeepsGoodLinesAndReportsBad(t *testing.T) {
	text := strings.Join([]string{
		"# a comment",
		"vless://uuid@a.example.com:443?security=tls&sni=a.com#A",
		"this line is garbage",
		"",
		"trojan://p@b.example.com:443#B",
		"vless://uuid@c.example.com:443?security=reality", // 缺少 pbk
	}, "\n")

	res := ParseText(text)
	if len(res.Profiles) != 2 {
		t.Fatalf("%d profiles, want 2", len(res.Profiles))
	}
	if len(res.Diagnostics) != 2 {
		t.Fatalf("%d diagnostics, want 2: %+v", len(res.Diagnostics), res.Diagnostics)
	}
	if res.Diagnostics[0].Line != 3 {
		t.Fatalf("first diagnostic line = %d, want 3", res.Diagnostics[0].Line)
	}
	if res.Diagnostics[1].Line != 6 {
		t.Fatalf("second diagnostic line = %d, want 6", res.Diagnostics[1].Line)
	}
	if res.Diagnostics[1].Field != "pbk" {
		t.Fatalf("second diagnostic field = %q, want pbk", res.Diagnostics[1].Field)
	}
}

// base64 订阅正文会展开为其包含的各行。
func TestParseTextDecodesBase64Subscription(t *testing.T) {
	inner := "vless://uuid@a.example.com:443#A\ntrojan://p@b.example.com:443#B"
	body := base64.StdEncoding.EncodeToString([]byte(inner))

	res := ParseText(body)
	if len(res.Profiles) != 2 {
		t.Fatalf("%d profiles, want 2 (diagnostics: %+v)", len(res.Profiles), res.Diagnostics)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", res.Diagnostics)
	}
}

// 流量响应头可解析，无效响应头不会返回结果。
func TestParseSubscriptionUserInfo(t *testing.T) {
	info, ok := ParseSubscriptionUserInfo("upload=100; download=200; total=1000; expire=1767225600")
	if !ok {
		t.Fatal("expected the header to parse")
	}
	if info.Upload != 100 || info.Download != 200 || info.Total != 1000 || info.Expire != 1767225600 {
		t.Fatalf("got %+v", info)
	}

	if _, ok := ParseSubscriptionUserInfo(""); ok {
		t.Fatal("empty header should not parse")
	}
	if _, ok := ParseSubscriptionUserInfo("garbage"); ok {
		t.Fatal("junk header should not parse")
	}
}
