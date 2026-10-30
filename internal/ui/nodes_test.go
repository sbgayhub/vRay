package ui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

// 节点页列出已存储的节点及其协议和地址。
func TestNodesListsProfiles(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()

	if !tr.HasText("tokyo") {
		t.Fatal("the node name is not listed")
	}
	if !tr.HasText("jp.example.com") {
		t.Fatal("the node address is not listed")
	}
	if !tr.HasText("共 1 个节点") {
		t.Fatal("the count is missing")
	}
}

func TestNodeListSeparatesExitFromServerAndShowsUnqueriedState(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	svc.State().SetProfiles([]app.ProfileView{{ID: "p1", Name: "Relay node", Protocol: "vless", Address: "entry.example.com", IP: "203.0.113.1", Port: 443}})
	tr := ui.NewTester(r.View, 1280, 1000)
	tr.Frame()
	list, _ := tr.Find("节点列表")
	entry, found := tr.Find("203.0.113.1")
	if !found || entry.X < list.X+list.W {
		t.Fatal("server IP should only appear in the inspector, not as a list exit")
	}
	if !tr.HasText("未查询") || !tr.HasText("服务器 IP") || !tr.HasText("出口 IP 信息") {
		t.Fatal("entry/exit labels or unqueried state are missing")
	}
	svc.State().Update(func(sn *app.Snapshot) {
		sn.NodeDetails = map[string]app.IPDetails{"p1": {IP: "198.51.100.20"}}
	})
	tr.Frame()
	exit, found := tr.Find("198.51.100.20")
	if !found || exit.X >= list.X+list.W {
		t.Fatal("measured exit is not shown in the node list")
	}
	svc.State().Update(func(sn *app.Snapshot) { sn.NodeDetails["p1"] = app.IPDetails{Error: "查询失败"} })
	tr.Frame()
	if tr.HasText("198.51.100.20") || !tr.HasText("查询失败") {
		t.Fatal("failed lookup still shows a stale or invented exit")
	}
	svc.State().Update(func(sn *app.Snapshot) {
		sn.NodeDetails["p1"] = app.IPDetails{IP: "198.51.100.20", Error: "更新失败"}
	})
	tr.Frame()
	if !tr.HasText("198.51.100.20") || !tr.HasText("更新出口 IP 信息") || !tr.HasText("已保留上次成功查询的记录") {
		t.Fatal("failed manual refresh did not explain and retain the previous observation")
	}
}

// 空数据库会说明如何填充，而不是显示空框。
func TestNodesEmptyState(t *testing.T) {
	svc, _ := app.NewFake(t.TempDir())
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	r := NewRoot(svc)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("还没有节点") {
		t.Fatal("the empty state does not explain what to do")
	}
}

// 搜索无匹配时会明确提示，同时仍提供计数。
func TestNodesSearchNoMatch(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	r.search = "osaka"

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("没有匹配的节点") {
		t.Fatal("an empty result is not explained")
	}
	// 搜索缩小列表时，检查器可能保留当前选择。
	if !tr.HasText("显示 0 / 1 个节点") {
		t.Fatal("the filtered list count is wrong")
	}
}

// filterProfiles 以不区分大小写的方式匹配名称、地址和协议。
func TestFilterProfiles(t *testing.T) {
	list := []app.ProfileView{
		{ID: "a", Name: "Tokyo 01", Address: "jp.example.com", Protocol: "vless"},
		{ID: "b", Name: "Osaka", Address: "os.example.com", Protocol: "trojan"},
	}

	cases := []struct {
		query string
		want  int
	}{
		{"", 2},
		{"  ", 2},
		{"tokyo", 1},
		{"TOKYO", 1},
		{"example.com", 2},
		{"trojan", 1},
		{"nowhere", 0},
	}
	for _, c := range cases {
		if got := len(filterProfiles(list, c.query)); got != c.want {
			t.Fatalf("filterProfiles(%q) = %d rows, want %d", c.query, got, c.want)
		}
	}
}

// filterProfiles 绝不返回调用方的切片，因此视图不会意外修改快照。
func TestFilterProfilesCopies(t *testing.T) {
	list := []app.ProfileView{{ID: "a", Name: "tokyo"}}
	got := filterProfiles(list, "")
	got[0].Name = "changed"
	if list[0].Name != "tokyo" {
		t.Fatal("filterProfiles returned the caller's slice")
	}
}

// 虚拟化限制渲染的行数；过滤不得隐藏后续匹配项。
func TestFilterProfilesKeepsAllMatches(t *testing.T) {
	list := make([]app.ProfileView, 1050)
	for i := range list {
		list[i] = app.ProfileView{ID: fmt.Sprint(i), Name: "node"}
	}
	if got := len(filterProfiles(list, "node")); got != len(list) {
		t.Fatalf("filtered length = %d, want %d", got, len(list))
	}
}

// 1000 行绘制不会 panic：列表已虚拟化（S7-2 要求保持流畅，而单元测试只能验证正确性）。
func TestNodesHandlesManyRows(t *testing.T) {
	r, svc := newRoot(t)
	views := make([]app.ProfileView, 1000)
	for i := range views {
		views[i] = app.ProfileView{
			ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("node-%d", i),
			Protocol: "vless", Address: "example.com", Port: 443,
		}
	}
	svc.State().SetProfiles(views)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("共 1000 个节点") {
		t.Fatal("the count is wrong for a large list")
	}
}

// countLabel 指明哪些数字重要：未过滤时显示总数，过滤时两者都显示。
func TestCountLabel(t *testing.T) {
	if got := countLabel(3, 3); got != "共 3 个节点" {
		t.Fatalf("countLabel(3,3) = %q", got)
	}
	if got := countLabel(2, 5); got != "显示 2 / 5 个节点" {
		t.Fatalf("countLabel(2,5) = %q", got)
	}
}
