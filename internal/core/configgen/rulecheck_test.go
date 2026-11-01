package configgen

import (
	"encoding/json"
	"strings"
	"testing"
)

// 路由器能理解的每种域名形式都应被接受。
func TestValidateRuleAcceptsRouterDomainForms(t *testing.T) {
	for _, d := range []string{
		"example.com",
		".example.com",
		"domain:example.com",
		"full:example.com",
		"keyword:doubleclick",
		"geosite:category-ads-all",
		"regexp:^ads?[0-9]*\\.",
	} {
		r := Rule{ID: "r", Domains: []string{d}, Outbound: TagDirect}
		if got := ValidateRule(r); len(got) != 0 {
			t.Fatalf("%q rejected: %+v", d, got)
		}
	}
}

// 格式错误的域名会被拒绝，并且消息中会指出该值。
func TestValidateRuleRejectsBadDomains(t *testing.T) {
	for _, d := range []string{
		"",
		"nodots",
		"has space.com",
		"https://example.com",
		"trailing.",
		"double..dot.com",
		"example.com:443",
		"geosite:",
		"keyword:",
	} {
		r := Rule{ID: "r", Domains: []string{d}, Outbound: TagDirect}
		got := ValidateRule(r)
		if len(got) != 1 {
			t.Fatalf("%q accepted: %+v", d, got)
		}
		if got[0].Field != RuleFieldDomains {
			t.Fatalf("%q reported on field %q", d, got[0].Field)
		}
	}
}

// IP：地址、CIDR 和 geoip 名称有效；其他内容无效。
func TestValidateRuleIPs(t *testing.T) {
	for _, ip := range []string{"10.0.0.0/8", "192.168.1.1", "::1", "fd00::/8", "geoip:private", "geoip:cn"} {
		if got := ValidateRule(Rule{ID: "r", IPs: []string{ip}}); len(got) != 0 {
			t.Fatalf("%q rejected: %+v", ip, got)
		}
	}
	for _, ip := range []string{"", "10.0.0.0/33", "999.1.1.1", "not-an-ip", "geoip:", "10.0.0.0/"} {
		got := ValidateRule(Rule{ID: "r", IPs: []string{ip}})
		if len(got) != 1 || got[0].Field != RuleFieldIPs {
			t.Fatalf("%q: got %+v, want one ips problem", ip, got)
		}
	}
}

// 端口列表遵循 Xray 语法：单个值、范围和 env: 引用。
func TestValidateRulePorts(t *testing.T) {
	for _, p := range []string{"", "80", "80,443", "80,8000-8100", "1-65535", "env:PORTS"} {
		if got := ValidateRule(Rule{ID: "r", Ports: p}); len(got) != 0 {
			t.Fatalf("%q rejected: %+v", p, got)
		}
	}
	for _, p := range []string{"0", "65536", "80,", "80-", "abc", "8100-8000", "env:", "1-2-3"} {
		got := ValidateRule(Rule{ID: "r", Ports: p})
		if len(got) != 1 || got[0].Field != RuleFieldPorts {
			t.Fatalf("%q: got %+v, want one ports problem", p, got)
		}
	}
}

// 所有问题都会被报告，而不只是第一个：逐个修复如同猜谜。
func TestValidateRuleReportsAll(t *testing.T) {
	got := ValidateRule(Rule{
		ID:      "r",
		Domains: []string{"", "ok.com"},
		IPs:     []string{"bad"},
		Ports:   "99999",
		Process: []string{" "},
	})
	if len(got) != 4 {
		t.Fatalf("problems = %d (%+v), want 4", len(got), got)
	}
}

// 格式错误的规则会阻止构建，并归因到该规则，
// 使失败信息指出具体值，而不是笼统的 "config invalid"。
func TestBuildRejectsMalformedRule(t *testing.T) {
	in := systemProxyInput()
	in.Rules = []Rule{{ID: "1", Name: "bad", IPs: []string{"10.0.0.0/33"}, Outbound: TagDirect}}
	if _, _, err := Build(in); err == nil {
		t.Fatal("Build accepted a malformed CIDR")
	}
}

// 有效规则会以带独立 tag 的路由规则形式进入配置。
func TestBuildEmitsRuleWithProcess(t *testing.T) {
	in := systemProxyInput()
	in.Rules = []Rule{{
		ID: "1", Name: "work", Outbound: TagDirect,
		Domains: []string{"corp.example.com"},
		IPs:     []string{"10.0.0.0/8"},
		Ports:   "80,443",
		Process: []string{"chrome.exe"},
	}}
	cfg, diags, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v (%+v)", err, diags)
	}
	var parsed map[string]any
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("config is not JSON: %v", err)
	}
	text := string(cfg)
	for _, want := range []string{
		`"ruleTag": "rule-1"`,
		`"corp.example.com"`,
		`"10.0.0.0/8"`,
		`"80,443"`,
		`"chrome.exe"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config lost %s\n%s", want, text)
		}
	}
}

// 只配进程名的规则同样是有效规则：曾经因为 buildRouting 的"空规则"判断
// 漏掉 Process 而被静默丢弃，永不生效。
func TestBuildEmitsRuleWithOnlyProcess(t *testing.T) {
	in := systemProxyInput()
	in.Rules = []Rule{{
		ID: "1", Name: "work", Outbound: TagDirect,
		Process: []string{"steam.exe"},
	}}
	cfg, diags, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v (%+v)", err, diags)
	}
	text := string(cfg)
	for _, want := range []string{
		`"ruleTag": "rule-1"`,
		`"steam.exe"`,
		`"outboundTag": "direct"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config lost %s\n%s", want, text)
		}
	}
}
