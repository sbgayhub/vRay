package ui

import (
	"fmt"
	"image/color"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPopupRemainsReadableOverTranslucentWindow(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, stripes := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%t/stripes=%t", dark, stripes), func(t *testing.T) {
				root := &Root{materialVisible: true}
				palette := *viewThemeForPopupTest(dark)
				base := palette.Surface
				palette.Surface = palette.Surface.Alpha(0.28)
				tr := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(&palette)
					c.Root().Background(ui.Transparent)
					if stripes {
						ui.Row(c).Size(128, 128).Children(func() {
							for i := range 32 {
								ink := ui.Transparent
								if i%2 == 0 {
									ink = palette.Text
								}
								ui.Box(c).Width(4).FillHeight().Background(ink)
							}
						})
					}
					ui.Overlay(c, func() {
						panel := ui.Box(c).Absolute().Left(0).Top(0).Size(128, 128)
						root.popupSurface(c, panel)
					})
				}, 128, 128)
				tr.Frame()
				a, b := tr.Image().At(62, 64), tr.Image().At(66, 64)
				if !stripes {
					_, _, _, alpha := a.RGBA()
					if alpha < 59000 || alpha == 65535 {
						t.Fatalf("popup must provide a dense translucent surface, alpha=%d", alpha)
					}
					return
				}
				composite := func(pixel color.Color) [3]float64 {
					r, g, b, a := pixel.RGBA()
					remaining := 1 - float64(a)/65535
					return [3]float64{
						float64(r)/257 + float64(base.R)*remaining,
						float64(g)/257 + float64(base.G)*remaining,
						float64(b)/257 + float64(base.B)*remaining,
					}
				}
				left, right := composite(a), composite(b)
				for channel := range 3 {
					if math.Abs(left[channel]-right[channel]) > 8 {
						t.Fatalf("lower-layer strokes remain sharp: %v vs %v", left, right)
					}
				}
			})
		}
	}
}

func viewThemeForPopupTest(dark bool) *ui.Theme {
	if dark {
		return ui.DarkTheme()
	}
	return ui.LightTheme()
}
