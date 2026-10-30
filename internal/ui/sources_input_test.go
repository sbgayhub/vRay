package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 导入框必须足够高，以便阅读多条粘贴的链接。它过去会缩到约一行，
// 因为 input.AutoTextArea 默认将 MinLines 设为 3，而 MyGO 会把空文本域限制为三行，
// 所以 Lines 从未使其增高。
func TestImportBoxIsUsablyTall(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageSources, pageRoutes[PageSources].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()

	if err := tr.Click("添加订阅"); err != nil {
		t.Fatal(err)
	}
	tr.Advance(time.Second)
	box, ok := tr.Find("订阅链接或分享内容")
	if !ok || box.H < importBoxHeight {
		t.Fatalf("import box bounds = %+v", box)
	}
}

// 该输入框是真正的编辑器：点击可获得焦点，输入内容会写入模型。
// 只渲染却无法输入的框比没有更糟。
func TestImportBoxAcceptsTyping(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageSources, pageRoutes[PageSources].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()

	if err := tr.Click("添加订阅"); err != nil {
		t.Fatal(err)
	}
	tr.Advance(time.Second)
	box, _ := tr.Find("订阅链接或分享内容")
	tr.ClickAt(box.X+20, box.Y+20)
	tr.Frame()
	tr.Type("vless://abc")
	tr.Frame()

	if r.importText != "vless://abc" {
		t.Fatalf("importText = %q, want the typed text", r.importText)
	}
}
