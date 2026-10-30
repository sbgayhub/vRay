package ui

import (
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

const cardRadius float32 = 16

// viewTheme 将调色板集中在一处；各控件沿用同一组令牌。
func viewTheme(base *ui.Theme, preference string) ui.Theme {
	dark := base.Dark
	if preference == "dark" {
		dark = true
	} else if preference == "light" {
		dark = false
	}
	t := *ui.LightTheme()
	if dark {
		t = *ui.DarkTheme()
		t.Background = ui.Hex("#171a1f")
		t.Surface = ui.Hex("#22262d")
		t.SurfaceHover = ui.Hex("#2d333d")
		t.SurfacePressed = ui.Hex("#38414d")
		t.Border = ui.Hex("#414b59")
		t.Text = ui.Hex("#f2f4f8")
		t.TextMuted = ui.Hex("#b4becb")
		t.Success = ui.Hex("#6cdda5")
		t.Warning = ui.Hex("#f2c779")
		t.Danger = ui.Hex("#ff8994")
	} else {
		t.Background = ui.Hex("#f3f4f6")
		t.Surface = ui.Hex("#ffffff")
		t.SurfaceHover = ui.Hex("#eef0f3")
		t.SurfacePressed = ui.Hex("#e2e5ea")
		t.Border = ui.Hex("#d8dde5")
		t.Text = ui.Hex("#1b2430")
		t.TextMuted = ui.Hex("#586474")
		t.Success = ui.Hex("#187348")
		t.Warning = ui.Hex("#8f5b0d")
		t.Danger = ui.Hex("#b92b44")
	}
	// 保留框架的系统强调色，包括与其形成对比的文字颜色。
	applyAccent(&t, base.Accent, base.AccentText)
	t.Inverse, t.InverseText = t.Text, t.Background
	t.Scrollbar = t.TextMuted.Alpha(0.45)
	t.Radius = 10
	t.FontSize = base.FontSize
	t.Font = base.Font
	return t
}

func materialFor(value string) mygo.Vibrancy {
	switch value {
	case "mica":
		return mygo.VibrancyMica
	case "micaAlt":
		return mygo.VibrancyTabbed
	case "acrylic":
		return mygo.VibrancyAcrylic
	case "liquidGlass":
		return mygo.VibrancyGlass
	default:
		return mygo.VibrancyNone
	}
}

func (r *Root) appearanceTheme(c *ui.Context, snap app.Snapshot) (ui.Theme, string, string) {
	preference, material, accent := snap.Settings.Theme, snap.Settings.Material, snap.Settings.Accent
	if r.lookOpen {
		preference, material, accent = r.lookTheme, r.lookMaterial, r.lookAccent
	}
	if preference == "" {
		preference = "system"
	}
	t := viewTheme(c.Theme(), preference)
	if len(accent) == 7 && accent[0] == '#' {
		applyAccent(&t, ui.Hex(accent), ui.RGB(255, 255, 255))
	}
	if c.Preferences().HighContrast {
		t = *c.Theme()
		material = "none"
	}
	if r.win != nil {
		prefs := mygo.Theme.Preferences()
		if prefs.HighContrast {
			t = *c.Theme()
			material = "none"
		}
		if prefs.ReduceTransparency {
			material = "none"
		}
	}
	return t, preference, material
}

func (r *Root) applyAppearance(c *ui.Context, snap app.Snapshot) {
	t, preference, material := r.appearanceTheme(c, snap)
	if r.win != nil {
		if r.appearance != preference {
			r.appearance = preference
			mygo.Theme.SetSource(mygo.ThemeSource(preference))
		}
		if r.material != material {
			r.material = material
			r.win.SetVibrancy(materialFor(material))
		}
	}
	r.materialVisible = materialFor(material) != mygo.VibrancyNone && c.Vibrancy()
	if r.materialVisible {
		t = materialTheme(t)
	}
	c.SetTheme(&t)
	if r.materialVisible {
		c.Root().Background(ui.Transparent)
	}
}

func materialTheme(t ui.Theme) ui.Theme {
	t.Surface, t.SurfaceHover = t.Surface.Alpha(0.28), t.SurfaceHover.Alpha(0.45)
	t.SurfacePressed, t.Border = t.SurfacePressed.Alpha(0.58), t.Border.Alpha(0.5)
	return t
}

func applyAccent(t *ui.Theme, accent, preferredInk ui.Color) {
	t.Accent = accent
	black, white := ui.RGB(0, 0, 0), ui.RGB(255, 255, 255)
	t.AccentText = preferredInk
	if colorContrast(accent, preferredInk) < 4.5 {
		t.AccentText = white
		if colorContrast(accent, black) > colorContrast(accent, white) {
			t.AccentText = black
		}
	}
	shade := black
	if colorLuminance(t.AccentText) < 0.5 {
		shade = white
	}
	t.AccentHover, t.AccentPressed = accent.Mix(shade, 0.08), accent.Mix(shade, 0.16)
	t.Selection, t.Focus = accent.Alpha(0.22), accent.Alpha(0.65)
}

func colorLuminance(c ui.Color) float64 {
	linear := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

func colorContrast(a, b ui.Color) float64 {
	x, y := colorLuminance(a), colorLuminance(b)
	return (max(x, y) + 0.05) / (min(x, y) + 0.05)
}

// 强调色填充保留所选系统颜色；着色控件中的文字可能需要该颜色更深或更浅的色调才能保持可读。
func accentForeground(t *ui.Theme) ui.Color {
	surface := t.Surface.Over(t.Background)
	selected := t.Accent.Alpha(0.12).Over(surface)
	for step := range 17 {
		tone := t.Accent.Mix(t.Text, float32(step)/16)
		if colorContrast(tone, surface) >= 4.5 && colorContrast(tone, selected) >= 4.5 {
			return tone
		}
	}
	return t.Text
}
