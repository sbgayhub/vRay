package configgen

import (
	"encoding/json"
	"testing"
)

// ALPN 列表按逗号拆分，并丢弃空项。
func TestSplitALPN(t *testing.T) {
	cases := map[string][]string{
		"":                 nil,
		"h2":               {"h2"},
		"h2,http/1.1":      {"h2", "http/1.1"},
		" h2 , , http/1.1": {"h2", "http/1.1"},
	}
	for in, want := range cases {
		got := splitALPN(in)
		if len(got) != len(want) {
			t.Fatalf("splitALPN(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("splitALPN(%q) = %v, want %v", in, got, want)
			}
		}
	}
}

// firstNonEmpty 返回第一个非空值；若没有则返回 ""。
func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"", "", "c"}, "c"},
		{[]string{"a", "b"}, "a"},
		{[]string{"", ""}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := firstNonEmpty(c.in...); got != c.want {
			t.Fatalf("firstNonEmpty(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// isTunName 仅接受 utunN 和 tunN。
func TestIsTunName(t *testing.T) {
	good := []string{"utun0", "utun7", "tun10", "tun0"}
	bad := []string{"", "utun", "tun", "eth0", "utunx", "utun7a", "en0"}
	for _, n := range good {
		if !isTunName(n) {
			t.Fatalf("isTunName(%q) = false, want true", n)
		}
	}
	for _, n := range bad {
		if isTunName(n) {
			t.Fatalf("isTunName(%q) = true, want false", n)
		}
	}
}

// dnsHost 将各种端点形式缩减为裸主机，并保留 IPv6 字面量。
func TestDNSHost(t *testing.T) {
	cases := map[string]string{
		"https://1.1.1.1/dns-query":           "1.1.1.1",
		"tls://dns.quad9.net:853":             "dns.quad9.net",
		"1.1.1.1":                             "1.1.1.1",
		"https://[2606:4700::1111]/dns-query": "[2606:4700::1111]",
	}
	for in, want := range cases {
		if got := dnsHost(in); got != want {
			t.Fatalf("dnsHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeDNS、kcp、ALPN 和规则网络都能进入配置，且配置仍可加载。
func TestFakeDNSKCPAndNetworksLoad(t *testing.T) {
	in := systemProxyInput()
	in.Settings.FakeDNS = true
	in.Settings.DNSPort = 5353
	in.Active = Outbound{
		Protocol: "vless", Address: "k.example.com", Port: 443, Secret: "uuid",
		Network: "kcp", Security: "tls", SNI: "k.example.com", ALPN: "h2,http/1.1",
	}
	in.Rules = []Rule{{
		ID: "udp", Domains: []string{"domain:a.example.com"},
		Networks: []string{"udp"}, Ports: "53,443", Outbound: TagDirect,
	}}

	raw, diags, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v (%+v)", err, diags)
	}

	var cfg struct {
		DNS struct {
			Servers []struct {
				Address string `json:"address"`
			} `json:"servers"`
		} `json:"dns"`
		Inbounds []struct {
			Tag  string `json:"tag"`
			Port int    `json:"port"`
		} `json:"inbounds"`
		Routing struct {
			Rules []struct {
				RuleTag string `json:"ruleTag"`
				Network string `json:"network"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(cfg.DNS.Servers) == 0 || cfg.DNS.Servers[0].Address != "fakedns" {
		t.Fatalf("fakeDNS server missing: %+v", cfg.DNS.Servers)
	}
	var dnsPort int
	for _, ib := range cfg.Inbounds {
		if ib.Tag == TagInDNS {
			dnsPort = ib.Port
		}
	}
	if dnsPort != 5353 {
		t.Fatalf("dns inbound port = %d, want 5353", dnsPort)
	}
	var udpNetwork string
	for _, r := range cfg.Routing.Rules {
		if r.RuleTag == "rule-udp" {
			udpNetwork = r.Network
		}
	}
	if udpNetwork != "udp" {
		t.Fatalf("rule network = %q, want udp", udpNetwork)
	}
}

// 没有 API 端口就不生成 API 入站：Xray 会拒绝缺少端口的 dokodemo，
// 因此生成器不得输出它。
func TestNoAPIInboundWithoutPort(t *testing.T) {
	in := systemProxyInput()
	in.Settings.APIPort = 0
	raw, _, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var cfg struct {
		Inbounds []struct {
			Tag string `json:"tag"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, ib := range cfg.Inbounds {
		if ib.Tag == TagInAPI {
			t.Fatal("API inbound emitted without a port")
		}
	}
}

// 没有 mux 和流设置的节点仍会生成普通 TCP 出站。
func TestPlainNodeHasNoStreamSettings(t *testing.T) {
	in := systemProxyInput()
	in.Active = Outbound{Protocol: "vless", Address: "p.example.com", Port: 80, Secret: "uuid"}
	raw, _, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var cfg struct {
		Outbounds []struct {
			Tag            string          `json:"tag"`
			StreamSettings json.RawMessage `json:"streamSettings"`
			Mux            json.RawMessage `json:"mux"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, o := range cfg.Outbounds {
		if o.Tag != TagProxy {
			continue
		}
		if len(o.StreamSettings) != 0 {
			t.Fatalf("plain node got streamSettings: %s", o.StreamSettings)
		}
		if len(o.Mux) != 0 {
			t.Fatalf("plain node got mux: %s", o.Mux)
		}
	}
}

// 空协议会报告为缺失，而不是不支持。
func TestEmptyProtocolDiagnostic(t *testing.T) {
	in := systemProxyInput()
	in.Active.Protocol = ""
	_, diags, err := Build(in)
	if err == nil {
		t.Fatal("Build accepted a node without a protocol")
	}
	var found bool
	for _, d := range diags {
		if d.Field == "protocol" && d.Message == "缺少协议" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v, want a missing-protocol one", diags)
	}
}

// 没有 DNS、探测 URL 和网关的 TUN 模式仍可构建，并会省略这些
// 配置段，而不是输出空配置段。
func TestTunWithoutDNSOrProbe(t *testing.T) {
	in := tunInput()
	in.Settings.DNSServers = nil
	in.Settings.ProbeURL = ""
	in.Settings.TunGateway = nil
	in.Settings.TunOutboundInterface = ""

	raw, diags, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v (%+v)", err, diags)
	}
	var cfg struct {
		DNS         json.RawMessage `json:"dns"`
		Observatory json.RawMessage `json:"observatory"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cfg.DNS) != 0 {
		t.Fatalf("dns section emitted without servers: %s", cfg.DNS)
	}
	if len(cfg.Observatory) != 0 {
		t.Fatalf("observatory emitted without a probe URL: %s", cfg.Observatory)
	}
}

// IPv6 DNS 端点会完整保留，而简单的冒号拆分可能破坏它。
func TestIPv6DNSEndpoint(t *testing.T) {
	in := systemProxyInput()
	in.Settings.DNSServers = []string{"https://[2606:4700:4700::1111]/dns-query"}
	in.Settings.DNSPort = 5353
	raw, _, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var cfg struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Settings struct {
				Address string `json:"address"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, ib := range cfg.Inbounds {
		if ib.Tag == TagInDNS && ib.Settings.Address != "[2606:4700:4700::1111]" {
			t.Fatalf("IPv6 DNS address = %q, want the bracketed literal", ib.Settings.Address)
		}
	}
}
