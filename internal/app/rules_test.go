package app

import (
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

// waitRuleNames 轮询直到规则列表恰好按顺序显示 names。
func waitRuleNames(t *testing.T, svc *Service, names ...string) []RuleView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := svc.State().Snapshot().Rules
		if len(got) == len(names) {
			ok := true
			for i := range names {
				if got[i].Name != names[i] {
					ok = false
					break
				}
			}
			if ok {
				return got
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	var shown []string
	for _, r := range svc.State().Snapshot().Rules {
		shown = append(shown, r.Name)
	}
	t.Fatalf("rules = %v, want %v", shown, names)
	return nil
}

func rule(id, name, outbound string) store.Rule {
	return store.Rule{
		ID: id, Name: name, Enabled: true, Outbound: outbound,
		Domains: []string{"example.com"},
	}
}

// 保存的规则会出现在快照中，且顺序与切片顺序一致。
func TestSaveRulesPersistsInOrder(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveRules([]store.Rule{
		rule("a", "first", "direct"),
		rule("b", "second", "block"),
	})
	waitRuleNames(t, svc, "first", "second")
}

// SaveRules 会拒绝语法错误的规则，且不会写入。
//
// 若写入，规则页会显示一条随后被配置生成器拒绝的规则，用户连接时只会看到 "config invalid"，而看不到真正错误的 CIDR。
func TestSaveRulesRefusesBadSyntax(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveRules([]store.Rule{rule("a", "good", "direct")})
	waitRuleNames(t, svc, "good")

	bad := rule("b", "bad", "direct")
	bad.IPs = []string{"10.0.0.0/33"}
	svc.SaveRules([]store.Rule{rule("a", "good", "direct"), bad})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p := svc.State().Snapshot().Problem; p != nil && p.Code == "RULE_INVALID" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p := svc.State().Snapshot().Problem; p == nil || p.Code != "RULE_INVALID" {
		t.Fatalf("problem = %+v, want RULE_INVALID", p)
	}
	waitRuleNames(t, svc, "good")
}

// 未知 outbound 会被拒绝：路由器没有地方发送它。
func TestSaveRulesRefusesUnknownOutbound(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveRules([]store.Rule{rule("a", "odd", "blackhole-typo")})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p := svc.State().Snapshot().Problem; p != nil && p.Code == "RULE_INVALID" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("problem = %+v, want RULE_INVALID", svc.State().Snapshot().Problem)
}

// 单独保存新规则时会追加，因此已有规则保持原有含义。
func TestSaveRuleAppendsNewRule(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveRules([]store.Rule{rule("a", "first", "direct")})
	waitRuleNames(t, svc, "first")

	svc.SaveRule(rule("b", "second", "block"))
	waitRuleNames(t, svc, "first", "second")
}

// 编辑已有规则会保持其位置。
func TestSaveRuleEditsInPlace(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveRules([]store.Rule{
		rule("a", "first", "direct"),
		rule("b", "second", "block"),
		rule("c", "third", "proxy"),
	})
	waitRuleNames(t, svc, "first", "second", "third")

	edited := rule("b", "second-renamed", "direct")
	svc.SaveRule(edited)
	waitRuleNames(t, svc, "first", "second-renamed", "third")
}

// 上移规则会改变路由器看到的先后顺序，而且这一变化必须经过存储往返后仍然有效。
func TestMoveRuleReorders(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveRules([]store.Rule{
		rule("a", "first", "direct"),
		rule("b", "second", "block"),
	})
	waitRuleNames(t, svc, "first", "second")

	svc.MoveRule("b", true)
	waitRuleNames(t, svc, "second", "first")

	svc.MoveRule("b", false)
	waitRuleNames(t, svc, "first", "second")
}

// 移过任一端不会做任何事：这不是错误，也绝不能循环绕回，否则会静默反转整个列表。
func TestMoveRuleStopsAtEdges(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveRules([]store.Rule{
		rule("a", "first", "direct"),
		rule("b", "second", "block"),
	})
	waitRuleNames(t, svc, "first", "second")

	svc.MoveRule("a", true) // 已在首位
	time.Sleep(50 * time.Millisecond)
	waitRuleNames(t, svc, "first", "second")

	svc.MoveRule("b", false) // 已在末位
	time.Sleep(50 * time.Millisecond)
	waitRuleNames(t, svc, "first", "second")
}

// 删除会重新编号剩余规则，因此存储顺序保持连续。
func TestDeleteRuleRenumbers(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveRules([]store.Rule{
		rule("a", "first", "direct"),
		rule("b", "second", "block"),
		rule("c", "third", "proxy"),
	})
	waitRuleNames(t, svc, "first", "second", "third")

	svc.DeleteRule("b")
	waitRuleNames(t, svc, "first", "third")
}

// 禁用规则会列出但绝不会参与编译：配置不得包含它。
func TestDisabledRuleIsNotCompiled(t *testing.T) {
	off := rule("a", "off", "block")
	off.Enabled = false
	on := rule("b", "on", "direct")

	got := configRules([]store.Rule{off, on})
	if len(got) != 1 {
		t.Fatalf("compiled rules = %d, want 1", len(got))
	}
	if got[0].ID != "b" {
		t.Fatalf("compiled rule = %q, want b", got[0].ID)
	}
}

// 启用和禁用状态会反映在编译结果中。
func TestSetRuleEnabled(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveRules([]store.Rule{rule("a", "first", "direct")})
	waitRuleNames(t, svc, "first")

	svc.SetRuleEnabled("a", false)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r := svc.State().Snapshot().Rules; len(r) == 1 && !r[0].Enabled {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("rule stayed enabled: %+v", svc.State().Snapshot().Rules)
}

// 值错误的规则会在列表中带有标注，因此页面无需用户操作就能指出它。
func TestRuleViewCarriesProblem(t *testing.T) {
	views := ruleViews([]store.Rule{
		{ID: "a", Name: "bad", Enabled: true, IPs: []string{"nope"}, Outbound: "direct"},
		{ID: "b", Name: "good", Enabled: true, Domains: []string{"ok.com"}, Outbound: "direct"},
	})
	if views[0].Problem == "" {
		t.Fatal("the bad rule has no problem annotation")
	}
	if views[1].Problem != "" {
		t.Fatalf("the good rule was annotated: %q", views[1].Problem)
	}
}
