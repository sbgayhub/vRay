package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// ButtonBase 的可点击性由什么决定：绘制出的子元素还是尺寸？
//
// 通过实测确定，因为此前猜测导致了一轮“按钮失效”：子元素才让元素拥有可点击区域。
// 仅设置标签不会绘制任何内容，因此测量尺寸为零且无法点击；把标签作为子元素绘制，
// 即使没有显式尺寸也可点击。textButton 同时做这两件事，因为它还必须看起来像一个控件。
func TestButtonMakesItselfClickable(t *testing.T) {
	cases := []struct {
		name    string
		build   func(c *ui.Context) ui.Element
		wantHit bool
	}{
		{
			name: "bare label",
			build: func(c *ui.Context) ui.Element {
				return ui.ButtonBase(c).Label("Bang")
			},
			wantHit: false,
		},
		{
			name: "label with an explicit height",
			build: func(c *ui.Context) ui.Element {
				return ui.ButtonBase(c).Label("Bang").Height(32).Width(80)
			},
			wantHit: true,
		},
		{
			name: "label drawn as a child, no size",
			build: func(c *ui.Context) ui.Element {
				btn := ui.ButtonBase(c).Label("Bang")
				btn.Children(func() { ui.Text(c, "Bang") })
				return btn
			},
			wantHit: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var clicks int
			view := func(c *ui.Context) {
				if tc.build(c).Clicked() {
					clicks++
				}
			}
			tr := ui.NewTester(view, 400, 200)
			tr.Frame()
			if err := tr.Click("Bang"); err != nil {
				// 甚至无法找到它，与无法点击是同一回事。
				if tc.wantHit {
					t.Fatalf("the button is not clickable: %v", err)
				}
				return
			}
			tr.Frame()
			if tc.wantHit && clicks != 1 {
				t.Fatalf("clicks = %d, want 1", clicks)
			}
			if !tc.wantHit && clicks != 0 {
				t.Fatalf("clicks = %d, want 0: this control should not be clickable", clicks)
			}
		})
	}
}
