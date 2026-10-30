package ui

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

func TestThemeFollowsSystemAndOverrides(t *testing.T) {
	for _, systemDark := range []bool{false, true} {
		for _, preference := range []string{"system", "light", "dark"} {
			t.Run(fmt.Sprintf("systemDark=%t/%s", systemDark, preference), func(t *testing.T) {
				base := ui.LightTheme()
				base.Dark = systemDark
				got := viewTheme(base, preference)
				wantDark := preference == "dark" || (preference == "system" && systemDark)
				if got.Dark != wantDark {
					t.Fatalf("dark = %t, want %t", got.Dark, wantDark)
				}
				if base.Dark != systemDark {
					t.Fatal("theme changed the source theme")
				}
			})
		}
	}
}

func TestThemeTextContrast(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		theme := viewTheme(ui.LightTheme(), appearance)
		pairs := []struct {
			name            string
			ink, background ui.Color
		}{
			{name: "body", ink: theme.Text, background: theme.Background},
			{name: "secondary", ink: theme.TextMuted, background: theme.Background},
			{name: "card secondary", ink: theme.TextMuted, background: theme.Surface},
			{name: "primary", ink: theme.AccentText, background: theme.Accent},
			{name: "error", ink: theme.Danger, background: theme.Surface},
			{name: "success", ink: theme.Success, background: theme.Surface},
			{name: "warning", ink: theme.Warning, background: theme.Surface},
			{name: "pressed text", ink: theme.Text, background: theme.SurfacePressed},
			{name: "pressed secondary", ink: theme.TextMuted, background: theme.SurfacePressed},
			{name: "hovered primary", ink: theme.AccentText, background: theme.AccentHover},
			{name: "pressed primary", ink: theme.AccentText, background: theme.AccentPressed},
			{name: "tooltip", ink: theme.InverseText, background: theme.Inverse},
		}
		for _, pair := range pairs {
			t.Run(appearance+"/"+pair.name, func(t *testing.T) {
				a, b := luminance(pair.ink), luminance(pair.background)
				if a < b {
					a, b = b, a
				}
				if ratio := (a + 0.05) / (b + 0.05); ratio < 4.5 {
					t.Fatalf("contrast %.2f:1, want at least 4.5:1", ratio)
				}
			})
		}
	}
}

func luminance(c ui.Color) float64 {
	linear := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(c.R) + 0.7152*linear(c.G) + 0.0722*linear(c.B)
}

// 在两种外观下渲染真实原生视图，包括最小窗口。
// 设置 VRAY_UI_PREVIEW 后，可选择保留 PNG 以供测试运行之外的视觉检查。
func TestAppearanceRendersEveryPage(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		for _, size := range [][2]int{{760, 520}, {1120, 760}} {
			for page := range pageRoutes {
				name := fmt.Sprintf("%s-%dx%d-%s", appearance, size[0], size[1], pageRoutes[page].ID)
				t.Run(name, func(t *testing.T) {
					r, svc := newRoot(t)
					r.cur, r.route = Page(page), pageRoutes[page].ID
					svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Theme = appearance })
					tr := ui.NewTester(r.View, size[0], size[1])
					tr.Frame()
					tr.Advance(time.Second)
					if Page(page) == PageSettings {
						bounds, ok := tr.Find("保存")
						if !ok || bounds.Y < 0 || bounds.Y+bounds.H > float32(size[1]) {
							t.Fatalf("save is outside the window: %+v", bounds)
						}
					}
					if dir := os.Getenv("VRAY_UI_PREVIEW"); dir != "" {
						if err := os.MkdirAll(dir, 0o755); err != nil {
							t.Fatal(err)
						}
						f, err := os.Create(filepath.Join(dir, name+".png"))
						if err != nil {
							t.Fatal(err)
						}
						defer f.Close()
						if err := png.Encode(f, tr.Image()); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func TestTitlebarAppearancePersistsAndDismisses(t *testing.T) {
	r, svc := newRoot(t)
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	if err := tr.Click("外观设置"); err != nil {
		t.Fatal(err)
	}
	tr.Advance(time.Second)
	if err := tr.Click("深色"); err != nil {
		t.Fatal(err)
	}
	if r.lookSaving != nil {
		<-r.lookSaving
	}
	tr.Frame()
	if !hasColor(tr.Image(), viewTheme(ui.LightTheme(), "dark").Background) {
		t.Fatal("dark selection did not change the rendered palette")
	}
	if err := tr.Click("Mica Alt"); err != nil {
		t.Fatal(err)
	}
	if r.lookSaving != nil {
		<-r.lookSaving
	}
	tr.Frame()
	if got := svc.State().Snapshot().Settings.Material; got != "micaAlt" {
		t.Fatalf("material = %q", got)
	}
	if err := tr.Click("完成"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if r.lookOpen {
		t.Fatal("appearance popover did not close")
	}
	if got := svc.State().Snapshot().Settings; got.Theme != "dark" || got.Material != "micaAlt" {
		t.Fatalf("appearance was not saved: %+v", got)
	}
}
