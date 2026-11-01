package ui

import (
	"math"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

const cardRadius float32 = 16

// 预先构建的明暗调色板：主题每帧重建，十六进制颜色只在进程启动时解析一次。
var (
	darkPalette  = buildPalette(true)
	lightPalette = buildPalette(false)
)

func buildPalette(dark bool) ui.Theme {
	t := *ui.DarkTheme()
	if !dark {
		t = *ui.LightTheme()
		t.Background, t.Surface, t.SurfaceHover, t.SurfacePressed = ui.Hex("#f3f4f6"), ui.Hex("#ffffff"), ui.Hex("#eef0f3"), ui.Hex("#e2e5ea")
		t.Border, t.Text, t.TextMuted = ui.Hex("#d8dde5"), ui.Hex("#1b2430"), ui.Hex("#586474")
		t.Success, t.Warning, t.Danger = ui.Hex("#187348"), ui.Hex("#8f5b0d"), ui.Hex("#b92b44")
		return t
	}
	t.Background, t.Surface, t.SurfaceHover, t.SurfacePressed = ui.Hex("#171a1f"), ui.Hex("#22262d"), ui.Hex("#2d333d"), ui.Hex("#38414d")
	t.Border, t.Text, t.TextMuted = ui.Hex("#414b59"), ui.Hex("#f2f4f8"), ui.Hex("#b4becb")
	t.Success, t.Warning, t.Danger = ui.Hex("#6cdda5"), ui.Hex("#f2c779"), ui.Hex("#ff8994")
	return t
}

// viewTheme 将调色板集中在一处；各控件沿用同一组令牌。
func viewTheme(base *ui.Theme, preference string) ui.Theme {
	dark := base.Dark
	if preference == "dark" {
		dark = true
	} else if preference == "light" {
		dark = false
	}
	t := lightPalette
	if dark {
		t = darkPalette
	}
	// 字号与字体跟随框架的当前值（DPI 变化会更新它们）；强调色保留
	// 框架的系统值，包括与其形成对比的文字颜色。
	t.FontSize, t.Font = base.FontSize, base.Font
	applyAccent(&t, base.Accent, base.AccentText)
	t.Inverse, t.InverseText = t.Text, t.Background
	t.Scrollbar = t.TextMuted.Alpha(0.45)
	t.Radius = 10
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

// accentForegroundCache 记住每种关键色组合的可读文字色。重绘路径每帧
// 调用 accentForeground 数十次，而结果只由主题的三个颜色决定；会话中的
// 主题数量有限（含外观弹窗的预览草稿），缓存不会无界增长。
var accentForegroundCache sync.Map // accentFgKey -> ui.Color

type accentFgKey struct {
	surface, accent, text ui.Color
}

// 强调色填充保留所选系统颜色；着色控件中的文字可能需要该颜色更深或更浅的色调才能保持可读。
func accentForeground(t *ui.Theme) ui.Color {
	key := accentFgKey{surface: t.Surface.Over(t.Background), accent: t.Accent, text: t.Text}
	if v, ok := accentForegroundCache.Load(key); ok {
		return v.(ui.Color)
	}
	c := computeAccentForeground(t)
	accentForegroundCache.Store(key, c)
	return c
}

func computeAccentForeground(t *ui.Theme) ui.Color {
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
