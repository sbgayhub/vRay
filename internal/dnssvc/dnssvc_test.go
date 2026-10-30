package dnssvc

import (
	"net"
	"strings"
	"testing"
)

// 默认策略不修改系统解析器。
//
// 会悄悄重定向 DNS 的代理客户端无法让人信任，因此 "system" 必须意味着
// 完全不执行任何操作。
func TestDecideSystemPolicyDoesNothing(t *testing.T) {
	got := Decide(PolicySystem, Availability{Can: true, Mechanism: "netsh"})
	if got.Takeover {
		t.Fatal("the system policy took over DNS")
	}
	if got.Warning != "" {
		t.Fatalf("the system policy warned: %q", got.Warning)
	}
}

// 在支持隧道 DNS 的机器上请求隧道 DNS 时会静默接管。
func TestDecideTunnelWhenAvailable(t *testing.T) {
	got := Decide(PolicyTunnel, Availability{Can: true, Mechanism: "netsh"})
	if !got.Takeover {
		t.Fatal("tunnel DNS did not take over on a capable machine")
	}
	if got.Warning != "" {
		t.Fatalf("a capable machine warned: %q", got.Warning)
	}
}

// 在无法工作的环境中请求隧道 DNS 时会发出警告，而不是假装成功。
//
// “无法”与“不会”是不同信息：用户明确请求了该功能，保持沉默会看起来像
// 成功，而查询实际可能泄漏。
func TestDecideTunnelWithNoMechanismWarns(t *testing.T) {
	got := Decide(PolicyTunnel, Availability{Can: false, Why: "没有 systemd-resolved"})
	if got.Takeover {
		t.Fatal("DNS was taken over with no mechanism")
	}
	if !strings.Contains(got.Warning, "未接管") {
		t.Fatalf("warning = %q, want it to say DNS was not taken over", got.Warning)
	}
	if !strings.Contains(got.Warning, "没有 systemd-resolved") {
		t.Fatalf("warning = %q, want it to name the reason", got.Warning)
	}
	if !strings.Contains(got.Warning, "泄漏") {
		t.Fatalf("warning = %q, want it to say queries may leak", got.Warning)
	}
}

// 即使没有给出原因，警告仍会提供可操作的信息。
func TestDecideTunnelNoReasonStillWarns(t *testing.T) {
	got := Decide(PolicyTunnel, Availability{Can: false})
	if got.Warning == "" {
		t.Fatal("no warning at all")
	}
	if !strings.Contains(got.Warning, "没有可用的 DNS 接管机制") {
		t.Fatalf("warning = %q", got.Warning)
	}
}

// 设置页提供的每种策略都是已知的，其他值则不是。
func TestPolicyKnown(t *testing.T) {
	for _, p := range []Policy{PolicySystem, PolicyTunnel, PolicyDirect} {
		if !p.Known() {
			t.Fatalf("%q is not known", p)
		}
	}
	for _, p := range []Policy{"", "fakedns", "SYSTEM"} {
		if p.Known() {
			t.Fatalf("%q was accepted", p)
		}
	}
}

// 只有在确实有服务响应时，探测才返回成功。
func TestTCPProbe(t *testing.T) {
	// 实践中回环地址的 1 端口没有服务监听。
	if err := TCPProbe(1); err == nil {
		t.Skip("something is listening on 127.0.0.1:1; cannot test the negative case here")
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	port := l.Addr().(*net.TCPAddr).Port
	if err := TCPProbe(port); err != nil {
		t.Fatalf("probe against a live listener: %v", err)
	}
}

// 超出范围的端口会被拒绝，而不会发起拨号。
func TestTCPProbeRejectsBadPort(t *testing.T) {
	for _, port := range []int{0, -1, 70000} {
		if err := TCPProbe(port); err == nil {
			t.Fatalf("port %d was accepted", port)
		}
	}
}
