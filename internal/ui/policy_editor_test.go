package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

func policyEditorFixture(t *testing.T) (*Root, *app.Service) {
	t.Helper()
	r, svc := newRoot(t)
	for i := 1; i <= 3; i++ {
		profile := store.Profile{
			ID: fmt.Sprintf("p%d", i), Name: fmt.Sprintf("东京节点 %02d", i),
			Protocol: store.ProtoVLESS, Address: fmt.Sprintf("jp%d.example.com", i), Port: 443,
		}
		if err := svc.AddTestProfile(profile, "uuid"); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	svc.State().Update(func(sn *app.Snapshot) {
		sn.NodeDetails = map[string]app.IPDetails{
			"p1": {IP: "203.0.113.10", Country: "日本", Region: "东京"},
			"p2": {IP: "203.0.113.10", Country: "日本", Region: "东京"},
			"p3": {IP: "198.51.100.20", Country: "新加坡", Region: "新加坡"},
		}
	})
	r.cur, r.route = PageGroups, pageRoutes[PageGroups].ID
	return r, svc
}

func waitPolicyEditor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the policy editor operation")
}

func TestPolicyEditorCreatesAnAutomaticGroupFromPersistedExits(t *testing.T) {
	r, svc := policyEditorFixture(t)
	tester := ui.NewTester(r.View, 1120, 900)
	tester.Frame()
	if err := tester.Click("新建策略组"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !r.policyEditor.open || !tester.HasText("策略组编辑") {
		t.Fatal("new group editor did not open")
	}
	if err := tester.Click("选择策略组类型 url-test"); err != nil {
		t.Fatal(err)
	}
	if !tester.HasText("策略组名称") {
		t.Fatal("the group name field is missing")
	}
	r.policyEditor.name = "日本固定出口"
	tester.Frame()
	if err := tester.Click("已查询出口"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("IP 203.0.113.10"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if _, ok := tester.Find("添加成员 东京节点 01"); !ok {
		t.Fatal("exit-IP filter did not keep the matching node")
	}
	if _, ok := tester.Find("添加成员 东京节点 03"); ok {
		t.Fatal("exit-IP filter kept a node with a different persisted exit")
	}
	if err := tester.Click("选择结果"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("保存策略组"); err != nil {
		t.Fatal(err)
	}
	waitPolicyEditor(t, func() bool {
		sn := svc.State().Snapshot()
		return !sn.GroupBusy && !r.policyEditor.open && len(sn.Groups) == 1
	})
	group := svc.State().Snapshot().Groups[0]
	if group.Type != "url-test" || group.Name != "日本固定出口" ||
		fmt.Sprint(group.Members) != "[p1 p2]" {
		t.Fatalf("saved custom group = %+v", group)
	}
}

func TestCustomPolicyGroupCanBeEditedAndDeletedFromTheDetailPane(t *testing.T) {
	r, svc := policyEditorFixture(t)
	svc.SavePolicyGroup(app.PolicyGroupEdit{
		Name: "临时手动组", Type: "select", Members: []string{"p1"}, Selected: "p1",
	})
	waitPolicyEditor(t, func() bool {
		return !svc.State().Snapshot().GroupBusy && len(svc.State().Snapshot().Groups) == 1
	})
	group := svc.State().Snapshot().Groups[0]
	r.selectedGroup = group.ID
	tester := ui.NewTester(r.View, 1120, 900)
	tester.Frame()
	if err := tester.Click("编辑策略组"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !r.policyEditor.open || r.policyEditor.id != group.ID {
		t.Fatal("edit action did not load the custom group")
	}
	if err := tester.Click("删除策略组"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("删除策略组？") {
		t.Fatal("delete confirmation did not open")
	}
	if err := tester.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	waitPolicyEditor(t, func() bool {
		sn := svc.State().Snapshot()
		return !sn.GroupBusy && len(sn.Groups) == 0 && !r.policyEditor.open
	})
}

func TestPolicyEditorUsesTheSelectedInterfaceLanguage(t *testing.T) {
	r, svc := policyEditorFixture(t)
	svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Language = "en" })
	r.openNewPolicyGroup(svc.State().Snapshot())
	tester := ui.NewTester(r.View, 1120, 900)
	tester.Frame()
	for _, text := range []string{
		"Policy group name", "Manual", "Checked exits", "Selected only",
		"Select results", "Clear results", "Save policy group",
	} {
		if !tester.HasText(text) {
			t.Fatalf("English policy editor is missing %q", text)
		}
	}
}

func TestPolicyEditorKeepsPrimaryActionsVisibleAtMinimumSize(t *testing.T) {
	r, _ := policyEditorFixture(t)
	r.openNewPolicyGroup(r.svc.State().Snapshot())
	tester := ui.NewTester(r.View, 760, 520)
	tester.Frame()
	for _, label := range []string{"策略组编辑", "策略组名称", "搜索可选成员", "取消", "保存策略组"} {
		bounds, ok := tester.Find(label)
		if !ok || bounds.W <= 0 || bounds.H <= 0 || bounds.Y+bounds.H > 521 {
			t.Fatalf("%s is not visible at 760×520: %+v", label, bounds)
		}
	}
}
