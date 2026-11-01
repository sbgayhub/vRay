package configgen

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/tun"

	// golden 文件必须能加载，因此测试二进制需要与应用程序相同的注册项。
	_ "github.com/sbgayhub/vray/internal/core/distro"
)

// realityNode 是一个完整指定的 REALITY + Vision 节点。
func realityNode() Outbound {
	return Outbound{
		Name:        "tokyo",
		Protocol:    "vless",
		Address:     "jp.example.com",
		Port:        443,
		Secret:      "11111111-2222-3333-4444-555555555555",
		Flow:        "xtls-rprx-vision",
		Network:     "tcp",
		Security:    "reality",
		SNI:         "www.microsoft.com",
		Fingerprint: "chrome",
		PublicKey:   "qHVQgljCf-qjNBmOC9HBPAjhHEHDjqOtptIN7qCWWUc",
		ShortID:     "abcd",
		SpiderX:     "/",
	}
}

func systemProxyInput() Input {
	return Input{
		Active: realityNode(),
		Mode:   ModeSystemProxy,
		Settings: Settings{
			MixedPort:  2080,
			LogLevel:   "warning",
			APIPort:    45123,
			ProbeURL:   "https://www.gstatic.com/generate_204",
			DNSServers: []string{"https://1.1.1.1/dns-query"},
			DNSPort:    5353,
		},
		Rules: []Rule{
			{ID: "lan", Name: "私网直连", IPs: []string{"10.0.0.0/8", "192.168.0.0/16"}, Outbound: TagDirect},
			{ID: "ads", Name: "广告拦截", Domains: []string{"domain:ads.example.com"}, Outbound: TagBlock},
		},
	}
}

func tunInput() Input {
	in := systemProxyInput()
	in.Mode = ModeTun
	in.Settings.TunName = "utun7"
	in.Settings.TunMTU = 1500
	in.Settings.TunGateway = []string{"172.19.0.1/30"}
	in.Settings.TunOutboundInterface = "en0"
	return in
}

// everyBuildLoads 是最关键的检查：Xray 必须接受我们生成的配置。
func everyBuildLoads(t *testing.T, in Input) {
	t.Helper()
	raw, diags, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v (diagnostics %+v)", err, diags)
	}
	if _, err := core.LoadConfig("json", bytes.NewReader(raw)); err != nil {
		t.Fatalf("generated config does not load: %v\n%s", err, raw)
	}
}

// system-proxy 模式下的 REALITY 节点会生成 Xray 可接受的配置。
func TestBuildSystemProxyLoads(t *testing.T) {
	everyBuildLoads(t, systemProxyInput())
}

// TUN 模式配置也能加载，并且保留环路防护。
func TestBuildTunLoads(t *testing.T) {
	everyBuildLoads(t, tunInput())
}

func TestTunRuntimeSettingsReachCore(t *testing.T) {
	in := tunInput()
	in.Settings.TunOutboundInterface = "auto"
	raw, _, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := core.LoadConfig("json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, inbound := range parsed.Inbound {
		if inbound.Tag != TagInTun {
			continue
		}
		value, err := inbound.ProxySettings.GetInstance()
		if err != nil {
			t.Fatal(err)
		}
		settings, ok := value.(*tun.Config)
		if !ok || settings.Name != in.Settings.TunName || len(settings.Gateway) != 1 || settings.Gateway[0] != "172.19.0.1/30" || settings.MTU != 1500 || settings.AutoOutboundsInterface != "auto" || len(settings.AutoSystemRoutingTable) != 2 {
			t.Fatal("TUN interface, addresses or loop protection were discarded by the core")
		}
		return
	}
	t.Fatal("TUN inbound did not reach the core")
}

// 我们声称支持的每种协议和传输方式都能加载。
func TestBuildTransportsAndProtocolsLoad(t *testing.T) {
	cases := map[string]Outbound{
		"vless-reality": realityNode(),
		"vless-ws-tls": {
			Protocol: "vless", Address: "a.example.com", Port: 443, Secret: "uuid",
			Network: "ws", Path: "/ws", Host: "cdn.example.com", Security: "tls", SNI: "cdn.example.com",
		},
		"vless-grpc": {
			Protocol: "vless", Address: "b.example.com", Port: 443, Secret: "uuid",
			Network: "grpc", Service: "svc", Security: "tls", SNI: "b.example.com",
		},
		"vless-xhttp": {
			Protocol: "vless", Address: "c.example.com", Port: 443, Secret: "uuid",
			Network: "xhttp", Path: "/x", Host: "c.example.com", XHTTPMode: "auto",
			Security: "tls", SNI: "c.example.com",
		},
		"vless-httpupgrade": {
			Protocol: "vless", Address: "d.example.com", Port: 443, Secret: "uuid",
			Network: "httpupgrade", Path: "/u", Host: "d.example.com",
			Security: "tls", SNI: "d.example.com",
		},
		"vless-tcp-plain": {
			Protocol: "vless", Address: "127.0.0.1", Port: 80, Secret: "uuid",
		},
		"vmess-ws": {
			Protocol: "vmess", Address: "f.example.com", Port: 443, Secret: "uuid",
			Network: "ws", Path: "/v", Security: "tls", SNI: "f.example.com",
		},
		"trojan": {
			Protocol: "trojan", Address: "g.example.com", Port: 443, Secret: "password",
			Security: "tls", SNI: "g.example.com",
		},
		"shadowsocks": {
			Protocol: "shadowsocks", Address: "h.example.com", Port: 8388,
			Secret: "pass", Method: "aes-256-gcm",
		},
	}

	for name, node := range cases {
		t.Run(name, func(t *testing.T) {
			in := systemProxyInput()
			in.Active = node
			everyBuildLoads(t, in)
		})
	}
}

// Mux 及额外的可选节点也能加载。
func TestBuildWithMuxAndExtraNodesLoads(t *testing.T) {
	in := systemProxyInput()
	in.Active.Mux = &Mux{Enabled: true, Concurrency: 8, XUDP: 8, UDP443: "reject"}
	in.Others = []Outbound{{
		Protocol: "trojan", Address: "other.example.com", Port: 443, Secret: "p",
		Security: "tls", SNI: "other.example.com",
	}}
	everyBuildLoads(t, in)
}

// TUN 模式会让 Xray 自身的流量绕开隧道：配置必须指定出站接口，
// 否则经由 tun 的默认路由会形成环路。
func TestTunConfigKeepsLoopGuard(t *testing.T) {
	raw, _, err := Build(tunInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var cfg struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
			Settings struct {
				AutoSystemRoutingTable []string `json:"autoSystemRoutingTable"`
				AutoOutboundsInterface string   `json:"autoOutboundsInterface"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var found bool
	for _, ib := range cfg.Inbounds {
		if ib.Tag != TagInTun {
			continue
		}
		found = true
		if ib.Port != 0 {
			t.Fatalf("tun inbound port = %d, want 0", ib.Port)
		}
		if ib.Protocol != "tun" {
			t.Fatalf("protocol = %q", ib.Protocol)
		}
		if len(ib.Settings.AutoSystemRoutingTable) == 0 {
			t.Fatal("autoSystemRoutingTable missing")
		}
		if ib.Settings.AutoOutboundsInterface == "" {
			t.Fatal("autoOutboundsInterface missing; the uplink would loop through tun")
		}
	}
	if !found {
		t.Fatal("no tun inbound in the config")
	}
}

// 每条路由规则都带有 ruleTag，便于 UI 标识命中的规则（计划 P10），
// 并且兜底规则位于最后。
func TestRoutingRulesCarryRuleTag(t *testing.T) {
	raw, _, err := Build(systemProxyInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var cfg struct {
		Routing struct {
			Rules []struct {
				RuleTag     string `json:"ruleTag"`
				OutboundTag string `json:"outboundTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rules := cfg.Routing.Rules
	if len(rules) == 0 {
		t.Fatal("no routing rules")
	}
	for i, r := range rules {
		if r.RuleTag == "" {
			t.Fatalf("rule %d has no ruleTag", i)
		}
	}
	// 通过 tag 查找用户规则，而不是假定索引：DNS 和 API 规则排在最前面。
	var lanSeen bool
	for _, r := range rules {
		if r.RuleTag == "rule-lan" {
			lanSeen = true
			if r.OutboundTag != TagDirect {
				t.Fatalf("rule-lan goes to %q, want %q", r.OutboundTag, TagDirect)
			}
		}
	}
	if !lanSeen {
		t.Fatalf("no rule tagged rule-lan in %+v", rules)
	}
}

// 诊断信息会指明字段，Build 会拒绝生成无法工作的配置。
func TestValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Input)
		field  string
	}{
		{"no address", func(in *Input) { in.Active.Address = "" }, "address"},
		{"bad port", func(in *Input) { in.Active.Port = 0 }, "port"},
		{"no secret", func(in *Input) { in.Active.Secret = "" }, "secret"},
		{"bad protocol", func(in *Input) { in.Active.Protocol = "wireguard" }, "protocol"},
		{"reality without key", func(in *Input) { in.Active.PublicKey = "" }, "publicKey"},
		{"bad mode", func(in *Input) { in.Mode = "socks" }, "mode"},
		{"bad tun name", func(in *Input) { in.Mode = ModeTun; in.Settings.TunName = "eth0" }, "tunName"},
		{"bad mixed port", func(in *Input) { in.Settings.MixedPort = 0 }, "mixedPort"},
		{"bad rule target", func(in *Input) { in.Rules[0].Outbound = "elsewhere" }, "rule/lan"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := systemProxyInput()
			tc.mutate(&in)
			raw, diags, err := Build(in)
			if err == nil {
				t.Fatalf("Build accepted invalid input: %s", raw)
			}
			if raw != nil {
				t.Fatal("Build returned a config for invalid input")
			}
			var found bool
			for _, d := range diags {
				if d.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("diagnostics %+v do not mention field %q", diags, tc.field)
			}
		})
	}
}

// 两种模式仅在入站配置上不同。
func TestModesDifferOnlyInInbounds(t *testing.T) {
	sp, _, err := Build(systemProxyInput())
	if err != nil {
		t.Fatalf("system-proxy: %v", err)
	}
	tun, _, err := Build(tunInput())
	if err != nil {
		t.Fatalf("tun: %v", err)
	}

	var a, b map[string]json.RawMessage
	if err := json.Unmarshal(sp, &a); err != nil {
		t.Fatalf("decode sp: %v", err)
	}
	if err := json.Unmarshal(tun, &b); err != nil {
		t.Fatalf("decode tun: %v", err)
	}
	for key := range a {
		if key == "inbounds" {
			continue
		}
		if !bytes.Equal(a[key], b[key]) {
			t.Fatalf("%q differs between modes but should not", key)
		}
	}
}

// golden 文件固定精确的 JSON，因此任何意外变化都会在审查中显现。
func TestGolden(t *testing.T) {
	cases := map[string]Input{
		"system-proxy.json": systemProxyInput(),
		"tun.json":          tunInput(),
		// 数据文件存在时使用相同输入：路由构建器的 geo 分支会生成另一组规则。
		"geo.json": withGeo(systemProxyInput()),
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			raw, diags, err := Build(in)
			if err != nil {
				t.Fatalf("Build: %v (%+v)", err, diags)
			}
			path := filepath.Join("testdata", "golden", name)
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, raw, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create it): %v", err)
			}
			if !bytes.Equal(raw, want) {
				t.Fatalf("generated config differs from %s\ngot:\n%s", path, raw)
			}
		})
	}
}

// DNS 入站必须拨号 IP，绝不能使用主机名，否则主机名上游可能
// 经由它即将供给的隧道解析（计划 P11）。
func TestDNSInboundUsesIP(t *testing.T) {
	raw, _, err := Build(systemProxyInput())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(string(raw), `"address": "1.1.1.1"`) {
		t.Fatalf("DNS inbound did not resolve to an IP:\n%s", raw)
	}
}

// withGeo 返回将 geo 数据文件标记为存在的输入。
func withGeo(in Input) Input {
	in.Settings.GeoReady = true
	return in
}
