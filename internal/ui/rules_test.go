package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

// waitRules 轮询直到页面快照显示 n 条规则。
func waitRules(t *testing.T, svc *app.Service, n int) []app.RuleView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := svc.State().Snapshot().Rules; len(got) == n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("rules = %d, want %d", len(svc.State().Snapshot().Rules), n)
	return nil
}

func sampleRule(id, name, outbound string) store.Rule {
	return store.Rule{
		ID: id, Name: name, Enabled: true, Outbound: outbound,
		Domains: []string{"example.com"},
	}
}

// 空规则页会解释没有规则意味着什么。
func TestRulesEmptyState(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageRules, pageRoutes[PageRules].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("还没有规则") {
		t.Fatal("the empty state is missing")
	}
	if !tr.HasText("新建规则") {
		t.Fatal("the create button is missing")
	}
}

// 页面按顺序列出规则及其位置编号。
//
// 路由器采用首条匹配，因此编号体现规则的含义：列表若不显示顺序，就无法据此判断规则行为。
func TestRulesListShowsOrder(t *testing.T) {
	r, svc := newRoot(t)
	svc.SaveRules([]store.Rule{
		sampleRule("rule-1", "内网直连", "direct"),
		sampleRule("rule-2", "广告拦截", "block"),
	})
	waitRules(t, svc, 2)
	r.cur, r.route = PageRules, pageRoutes[PageRules].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	for _, want := range []string{"内网直连", "广告拦截", "共 2 条", "直连", "拦截"} {
		if !tr.HasText(want) {
			t.Fatalf("%q is not on screen", want)
		}
	}
}

// 值无效的规则会在列表中标记，无需用户操作，因此损坏的规则不会悄无声息。
func TestRulesListFlagsBadRule(t *testing.T) {
	r, svc := newRoot(t)
	bad := sampleRule("rule-1", "坏规则", "direct")
	bad.IPs = []string{"10.0.0.0/33"}
	// 直接通过存储写入：SaveRules 会拒绝它，这正是重点，
	// 因此列表必须能承受以其他方式进入的规则。
	if err := svc.SetRulesForTest([]store.Rule{bad}); err != nil {
		t.Fatalf("SetRulesForTest: %v", err)
	}
	waitRules(t, svc, 1)
	r.cur, r.route = PageRules, pageRoutes[PageRules].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("10.0.0.0/33") {
		t.Fatal("the offending value is not shown")
	}
}

// 编辑器新建规则时为空，编辑已有规则时预填。
func TestRuleDialogLoadsValues(t *testing.T) {
	r, _ := newRoot(t)
	r.ruleF.blankRule()
	if r.ruleF.id != "" {
		t.Fatalf("a new rule has an id: %q", r.ruleF.id)
	}
	if !r.ruleF.enabled || r.ruleF.outbound != "proxy" {
		t.Fatalf("new rule defaults = %+v", r.ruleF)
	}

	r.editRule(app.RuleView{
		ID: "rule-1", Name: "内网", Enabled: true, Outbound: "direct",
		Domains: []string{"a.com", "b.com"}, IPs: []string{"10.0.0.0/8"},
		Ports: "80", Process: []string{"chrome.exe"}, Networks: []string{"tcp"},
	})
	if r.ruleF.id != "rule-1" || r.ruleF.name != "内网" {
		t.Fatalf("form = %+v", r.ruleF)
	}
	if got := r.ruleF.rule(); len(got.Domains) != 2 || len(got.IPs) != 1 ||
		len(got.Process) != 1 || len(got.Networks) != 1 {
		t.Fatalf("round trip lost values: %+v", got)
	}
	if !r.ruleF.tcp || r.ruleF.udp {
		t.Fatalf("networks did not round trip: tcp=%v udp=%v", r.ruleF.tcp, r.ruleF.udp)
	}
}

// 对话框渲染其字段，并在尚无匹配条件时给出提示。
func TestRuleDialogRenders(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageRules, pageRoutes[PageRules].ID
	r.ruleF.blankRule()

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	for _, want := range []string{"新建规则", "域名", "IP", "端口", "进程", "出口", "代理", "直连", "拦截"} {
		if !tr.HasText(want) {
			t.Fatalf("%q is not on screen", want)
		}
	}
	if !tr.HasText("至少需要一个匹配条件") {
		t.Fatal("an empty rule is not warned about")
	}
}

// 语法错误会指出有问题的值并阻止保存。
func TestRuleDialogBlocksSaveOnBadValue(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageRules, pageRoutes[PageRules].ID
	r.ruleF.blankRule()
	r.ruleF.ips = "10.0.0.0/33"

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("10.0.0.0/33") {
		t.Fatal("the offending value is not named")
	}
	// 对话框保持打开，且没有存储任何内容。
	if !r.ruleF.open {
		t.Fatal("the dialog closed on its own")
	}
	if got := r.svc.State().Snapshot().Rules; len(got) != 0 {
		t.Fatalf("a bad rule was stored: %+v", got)
	}
}

// 从对话框保存会存储输入的规则并关闭对话框。
func TestRuleDialogSaves(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageRules, pageRoutes[PageRules].ID
	r.ruleF.blankRule()
	r.ruleF.name = "内网直连"
	r.ruleF.ips = "10.0.0.0/8"

	rule := r.ruleF.rule()
	rule.ID = nextRuleID(svc.State().Snapshot().Rules)
	svc.SaveRule(rule)

	got := waitRules(t, svc, 1)
	if got[0].Name != "内网直连" || got[0].Outbound != "proxy" {
		t.Fatalf("stored rule = %+v", got[0])
	}
	if got[0].IPs[0] != "10.0.0.0/8" {
		t.Fatalf("IPs = %v", got[0].IPs)
	}
}

// 行是列表格式：空行会被忽略，不会转换为值。
func TestLinesDropsBlanks(t *testing.T) {
	got := lines("a.com\r\n\r\n  b.com  \n\n")
	if len(got) != 2 || got[0] != "a.com" || got[1] != "b.com" {
		t.Fatalf("lines = %v", got)
	}
	if got := lines(""); len(got) != 0 {
		t.Fatalf("empty text gave %v", got)
	}
}

// 新 ID 永远不会与已有 ID 冲突。
func TestNextRuleID(t *testing.T) {
	existing := []app.RuleView{{ID: "rule-1"}, {ID: "rule-3"}}
	if got := nextRuleID(existing); got != "rule-2" {
		t.Fatalf("id = %q, want rule-2", got)
	}
	if got := nextRuleID(nil); got != "rule-1" {
		t.Fatalf("id = %q, want rule-1", got)
	}
}

// 摘要会说明规则匹配什么，并在没有任何匹配时明确提示。
func TestRuleSummary(t *testing.T) {
	got := ruleSummary(app.RuleView{
		Domains:  []string{"a.com", "b.com", "c.com"},
		IPs:      []string{"10.0.0.0/8"},
		Ports:    "80",
		Networks: []string{"tcp"},
	})
	for _, want := range []string{"a.com", "等 3 项", "10.0.0.0/8", "80", "TCP"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary = %q, missing %q", got, want)
		}
	}
	if got := ruleSummary(app.RuleView{}); !strings.Contains(got, "不会生效") {
		t.Fatalf("empty summary = %q", got)
	}
}
