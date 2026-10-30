package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

// waitImport 轮询直到最后一次导入产生结果。
func waitImport(t *testing.T, svc *app.Service) app.Snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sn := svc.State().Snapshot()
		if sn.ImportAdded > 0 || sn.ImportProblem != "" {
			return sn
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the import never reported an outcome")
	return app.Snapshot{}
}

// 订阅页显示粘贴框（这是节点进入系统的唯一方式），以及未订阅任何内容时的空状态。
func TestSourcesShowsImportBox(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageSources, pageRoutes[PageSources].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if err := tr.Click("添加订阅"); err != nil {
		t.Fatal(err)
	}
	tr.Advance(time.Second)
	if !tr.HasText("导入") {
		t.Fatal("the import box is missing")
	}
	if !tr.HasText("粘贴分享链接") {
		t.Fatal("the paste box does not say what to paste")
	}
}

// 成功导入会在页面上提示，而不只是在节点列表中体现。
func TestSourcesReportsImportResult(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageSources, pageRoutes[PageSources].ID

	r.importText = "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp#osaka"
	svc.ImportText(r.importText)
	sn := waitImport(t, svc)

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if sn.ImportAdded != 1 {
		t.Fatalf("ImportAdded = %d, want 1", sn.ImportAdded)
	}
	if !tr.HasText("已导入 1 个节点") {
		t.Fatal("a successful import is not confirmed on screen")
	}
}

// 垃圾输入会说明为何没有导入任何内容。
func TestSourcesReportsImportFailure(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageSources, pageRoutes[PageSources].ID

	svc.ImportText("这不是链接")
	sn := waitImport(t, svc)

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if sn.ImportProblem == "" {
		t.Fatal("no problem was recorded")
	}
	if !tr.HasText(sn.ImportProblem) {
		t.Fatal("the import problem is not shown")
	}
}

// 已存储的订阅显示为卡片，其状态以文字呈现，因此页面绝不只依赖颜色。
func TestSourcesShowsSubscriptionCards(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().SetSources([]app.SourceView{
		{ID: "s1", Name: "机场A", Enabled: true, NodeCount: 12},
		{ID: "s2", Name: "机场B", Enabled: false, NodeCount: 0, LastError: "超时"},
	})
	r.cur, r.route = PageSources, pageRoutes[PageSources].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()

	for _, want := range []string{"机场A", "机场B", "已启用", "已停用", "12 个节点", "超时"} {
		if !tr.HasText(want) {
			t.Fatalf("%q is not shown", want)
		}
	}
}
