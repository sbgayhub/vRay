package ui

import (
	"image"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 每个标题栏按钮图形都必须能够解析并渲染。
//
// 这里防范连续出现的两个缺陷：按钮最初用 ui.ButtonBase(c).Label("─") 构建，
// 其中 Label 只设置无障碍名称而不绘制内容，因此标题栏出现三个不可见的框；
// 之后改用 Unicode 框线字符，但它们的粗细和基线始终不匹配，看起来比系统按钮更差。
func TestCaptionGlyphsRender(t *testing.T) {
	for _, name := range []string{"minimise", "maximise", "restore", "close"} {
		if svg, ok := captionGlyphs[name]; !ok || svg == nil {
			t.Fatalf("glyph %q did not parse", name)
		}
		view := func(c *ui.Context) {
			ui.Row(c).Fill().Children(func() {
				ui.Icon(c, captionGlyphs[name]).Size(10, 10).TextColor(c.Theme().Text)
			})
		}
		tr := ui.NewTester(view, 24, 24)
		tr.Frame()
		img := tr.Image()
		if img == nil {
			t.Fatalf("glyph %q produced no frame", name)
		}
		if !hasInk(img) {
			t.Fatalf("glyph %q drew nothing", name)
		}
	}
}

// hasInk 报告是否存在与左上角不同的像素；左上角是仅绘制图形时该帧的背景色。
func hasInk(img image.Image) bool {
	b := img.Bounds()
	bg := img.At(b.Min.X, b.Min.Y)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.At(x, y) != bg {
				return true
			}
		}
	}
	return false
}

// 按钮保留无障碍名称，因此屏幕阅读器和测试仍能区分它们。
func TestCaptionButtonKeepsAccessibleName(t *testing.T) {
	r := &Root{}
	view := func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			r.captionButton(c, "close", "关闭", true)
		})
	}
	tr := ui.NewTester(view, 60, 60)
	tr.Frame()
	if !tr.HasText("关闭") {
		t.Fatal("the accessible name was lost")
	}
}

// 悬停关闭按钮时，它像 Windows 一样填充红色；悬停其他按钮时，则填充主题的悬停色。
// 没有悬停反馈的标题栏按钮看起来像已禁用。
func TestCaptionButtonHoverFills(t *testing.T) {
	cases := []struct {
		name        string
		destructive bool
	}{
		{"close", true},
		{"minimise", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Root{}
			// 预期颜色取自按钮使用的同一主题，因此测试不会固定为浅色模式常量。
			var want ui.Color
			view := func(c *ui.Context) {
				th := c.Theme()
				want = th.SurfaceHover
				if tc.destructive {
					want = closeHover
				}
				ui.Row(c).Fill().Background(th.Background).Children(func() {
					r.captionButton(c, tc.name, "button", tc.destructive)
				})
			}
			tr := ui.NewTester(view, 80, 60)
			tr.Frame()
			tr.Move(23, 18)
			tr.Frame()

			if !hasColor(tr.Image(), want) {
				t.Fatalf("hovering %q did not fill it with %v", tc.name, want)
			}
		})
	}
}

// hasColor 报告是否存在等于 c 的像素。
func hasColor(img image.Image, c ui.Color) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if sameColor(img.At(x, y), c) {
				return true
			}
		}
	}
	return false
}

// sameColor 将每通道 8 位的像素与主题颜色进行比较。
func sameColor(p color.Color, want ui.Color) bool {
	r, g, b, _ := p.RGBA()
	return uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(b>>8) == want.B
}
