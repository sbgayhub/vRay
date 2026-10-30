package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 节点搜索框必须可输入：这是缩小长订阅范围的唯一方式。
func TestNodeSearchAcceptsTyping(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()

	count, ok := tr.Find("搜索节点")
	if !ok {
		t.Fatal("the count row is missing")
	}
	tr.ClickAt(count.X+count.W/2, count.Y+count.H/2)
	tr.Frame()
	tr.Type("zo")
	tr.Frame()

	if r.search != "zo" {
		t.Fatalf("search = %q, want the typed text", r.search)
	}
}

// 该框必须在其大部分高度上响应点击，而不只是文字所在的一行：
// 一个 36px 高的控件只有 20px 内可响应，会让人觉得坏了。
//
// 它还守护外观：input.SearchInput 原本没有高度，并将行左侧内边距设为零，
// 导致图标贴着边框，输入框看起来像页面中的空隙，而不是控件。
// 正因如此，这个输入框由本包自行构建；该测试确保它仍是真实可点击的表面。
func TestNodeSearchBoxLooksLikeAControl(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()

	count, ok := tr.Find("搜索节点")
	if !ok {
		t.Fatal("the count row is missing")
	}
	if count.Y < 20 {
		t.Fatalf("the toolbar sits at y=%.0f, so it has no room above it", count.Y)
	}

	hits, total := 0, 0
	for dy := float32(2); dy < count.H-2; dy += 2 {
		total++
		r.search = ""
		tr.ClickAt(count.X+count.W/2, count.Y+dy)
		tr.Frame()
		tr.Type("x")
		tr.Frame()
		if r.search == "x" {
			hits++
		}
	}
	if hits*2 < total {
		t.Fatalf("only %d of %d rows of the search box accept a click", hits, total)
	}
}
