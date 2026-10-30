package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
)

func fitPopupTester(tr *ui.Tester, host *nativePopup, width int) {
	for range 6 {
		tr.Frame()
		tr.Advance(50 * time.Millisecond)
		if host.height > 0 {
			tr.SetSize(width, host.height)
		}
	}
}

func TestNativePopupsFitTheirContent(t *testing.T) {
	for _, tc := range []struct {
		kind, footer            string
		width, initial, padding int
	}{
		{"外观设置", "即时生效，自动保存", 288, 420, 16},
		{"添加订阅", "清空", 440, 360, 20},
		{"编辑订阅", "取消", 440, 440, 20},
		{"删除订阅", "取消", 440, 240, 20},
		{"编辑节点", "取消", 440, 440, 20},
		{"新建规则", "取消", 440, 560, 20},
		{"节点操作", "删除节点", 192, 112, 16},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			r, _ := newRoot(t)
			switch tc.kind {
			case "外观设置":
				r.lookOpen, r.lookTheme, r.lookMaterial, r.lookAccent = true, "light", "none", "system"
			case "添加订阅":
				r.importOpen = true
			case "编辑订阅":
				r.sourceF = sourceForm{open: true, interval: "180", draft: app.SourceEdit{Name: "订阅", URL: "https://example.com/sub"}}
			case "删除订阅":
				r.sourceDeleting = "s1"
			case "编辑节点":
				r.editF.load(profileForTest())
			case "新建规则":
				r.ruleF.blankRule()
			case "节点操作":
				r.nodeMore, r.selectedNode = true, r.svc.State().Snapshot().Profiles[0].ID
			}
			host := &nativePopup{kind: tc.kind, modal: tc.padding == 20, height: tc.initial, maxHeight: 712}
			tr := ui.NewTester(func(c *ui.Context) { r.nativePopupView(c, host) }, tc.width, tc.initial)
			fitPopupTester(tr, host, tc.width)
			footer, ok := tr.Find(tc.footer)
			gap := float32(host.height) - footer.Y - footer.H
			if !ok || gap < float32(tc.padding-2) || gap > float32(tc.padding+6) {
				t.Fatalf("bottom gap %.1f, height=%d, footer=%+v", gap, host.height, footer)
			}
		})
	}
}

func TestNativePopupGrowsAfterAsyncLoadAndShrinksAfterError(t *testing.T) {
	r, svc := newRoot(t)
	r.sourceF = sourceForm{open: true, waiting: "s1"}
	host := &nativePopup{kind: "编辑订阅", modal: true, height: 440, maxHeight: 472}
	tr := ui.NewTester(func(c *ui.Context) { r.nativePopupView(c, host) }, 440, 440)
	fitPopupTester(tr, host, 440)
	loadingHeight := host.height
	svc.State().Update(func(sn *app.Snapshot) {
		sn.SourceDraft = &app.SourceEdit{ID: "s1", Name: "订阅", URL: "https://example.com/sub", IntervalMinutes: 180}
	})
	fitPopupTester(tr, host, 440)
	formHeight := host.height
	if formHeight <= loadingHeight+100 {
		t.Fatalf("form failed to grow: loading=%d, form=%d", loadingHeight, formHeight)
	}
	svc.State().Update(func(sn *app.Snapshot) { sn.SourceResult = "订阅链接格式错误，请检查后重新保存。" })
	fitPopupTester(tr, host, 440)
	if host.height <= formHeight || host.height > host.maxHeight {
		t.Fatalf("error size invalid: %d, form=%d", host.height, formHeight)
	}
	svc.State().Update(func(sn *app.Snapshot) { sn.SourceResult = "" })
	fitPopupTester(tr, host, 440)
	if host.height != formHeight {
		t.Fatalf("empty error left extra space: %d vs %d", host.height, formHeight)
	}
}

func TestNativeSourceEditorShowsFieldsWhenRoomIsAvailable(t *testing.T) {
	r, _ := newRoot(t)
	r.sourceF = sourceForm{open: true, interval: "180", draft: app.SourceEdit{Name: "订阅", URL: "https://example.com/sub"}}
	host := &nativePopup{kind: "编辑订阅", modal: true, height: 440, maxHeight: 712}
	tr := ui.NewTester(func(c *ui.Context) { r.nativePopupView(c, host) }, 440, 440)
	fitPopupTester(tr, host, 440)
	checkbox, found := tr.Find("启用订阅")
	save, _ := tr.Find("保存订阅")
	if !found || checkbox.Y+checkbox.H > save.Y {
		t.Fatalf("fields remain clipped despite room: checkbox=%+v, save=%+v", checkbox, save)
	}
}
