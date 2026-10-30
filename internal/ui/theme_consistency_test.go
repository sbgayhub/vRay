package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
)

func TestAccentStatesRemainReadable(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		for _, color := range []string{"#2563eb", "#7166c5", "#187348", "#b92b64", "#facc15", "#3b82f6"} {
			t.Run(appearance+"/"+color, func(t *testing.T) {
				theme := viewTheme(ui.LightTheme(), appearance)
				applyAccent(&theme, ui.Hex(color), ui.RGB(255, 255, 255))
				for _, fill := range []ui.Color{theme.Accent, theme.AccentHover, theme.AccentPressed} {
					a, b := luminance(theme.AccentText), luminance(fill)
					if (max(a, b)+0.05)/(min(a, b)+0.05) < 4.5 {
						t.Fatalf("unreadable accent state: text=%v fill=%v", theme.AccentText, fill)
					}
				}
				ink := accentForeground(&theme)
				selected := theme.Accent.Alpha(0.12).Over(theme.Surface)
				a, b := luminance(ink), luminance(selected)
				if (max(a, b)+0.05)/(min(a, b)+0.05) < 4.5 {
					t.Fatal("tinted choice text is unreadable")
				}
			})
		}
	}
}

func TestWindowAndNativePopupShareThemeTokens(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		for _, material := range []string{"none", "mica"} {
			r, svc := newRoot(t)
			svc.State().Update(func(sn *app.Snapshot) {
				sn.Settings.Theme, sn.Settings.Material, sn.Settings.Accent = appearance, material, "#187348"
			})
			var mainTheme, popupTheme ui.Theme
			main := ui.NewTester(func(c *ui.Context) { r.View(c); mainTheme = *c.Theme() }, 1120, 760)
			main.SetVibrancy(true)
			r.importOpen = true
			host := &nativePopup{kind: "添加订阅", material: material, modal: true}
			popup := ui.NewTester(func(c *ui.Context) { r.nativePopupView(c, host); popupTheme = *c.Theme() }, 440, 360)
			popup.SetVibrancy(true)
			if mainTheme != popupTheme {
				t.Fatalf("%s/%s: main and popup theme tokens differ", appearance, material)
			}
		}
	}
}

func TestHighContrastDisablesTranslucentSurfaces(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Material = "mica" })
	var theme ui.Theme
	tr := ui.NewTester(func(c *ui.Context) { r.View(c); theme = *c.Theme() }, 1120, 760)
	tr.SetVibrancy(true)
	tr.SetPreferences(ui.Preferences{HighContrast: true})
	if r.materialVisible || theme.Surface.A != 255 {
		t.Fatal("high contrast still uses translucent surfaces")
	}
}
