package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

func TestTitlebarLanguageSwitchPersists(t *testing.T) {
	r, svc := newRoot(t)
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("首页") || svc.State().Snapshot().Settings.Language != "zh-Hans" {
		t.Fatal("a fresh installation must use Simplified Chinese")
	}
	if err := tester.Click("切换语言"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.Click("English"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for svc.State().Snapshot().Settings.Language != "en" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	tester.Frame()
	if !tester.HasText("Home") || !tester.HasText("Local proxy") || r.languageOpen {
		t.Fatal("language selection did not update and dismiss the popover")
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if svc.State().Snapshot().Settings.Language != "en" {
		t.Fatal("language was not persisted")
	}
	r.cur, r.route = PageConnections, pageRoutes[PageConnections].ID
	tester.Frame()
	if !tester.HasText("Connections") {
		t.Fatal("navigation did not follow the selected language")
	}
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	tester.Frame()
	if !tester.HasText("Manual import") || tester.HasText("手动导入") {
		t.Fatal("generated source names were not translated")
	}
}

func TestLanguagesAndPoliciesLayout(t *testing.T) {
	for _, language := range []string{"zh-Hans", "zh-Hant", "en"} {
		for _, width := range []int{760, 1120} {
			t.Run(fmt.Sprintf("%s-%d", language, width), func(t *testing.T) {
				r, svc := homeDashboardFixture(t)
				svc.State().Update(func(sn *app.Snapshot) {
					sn.Settings.Language = language
					sn.Groups = []store.PolicyGroup{
						{ID: "manual", Name: "Proxy · 手动选择", Type: "select", Members: []string{"p1", "auto"}, Selected: "auto", SourceID: "sub1"},
						{ID: "auto", Name: "Auto · 自动测速", Type: "url-test", Members: []string{"p1"}, SourceID: "sub1"},
					}
				})
				tester := ui.NewTester(r.View, width, 760)
				for _, page := range []Page{PageHome, PageGroups, PageNodes, PageConnections, PageSources, PageRules, PageSettings, PageLogs} {
					r.cur, r.route = page, pageRoutes[page].ID
					tester.Frame()
					tester.Advance(time.Second)
					if page == PageGroups {
						bounds, ok := tester.Find("策略组成员")
						if !ok || bounds.H < 140 || bounds.X+bounds.W > float32(width)+1 || bounds.Y+bounds.H > 760 {
							t.Fatalf("policy members do not fit: %+v", bounds)
						}
						if !tester.HasText("Proxy · 手动选择") {
							t.Fatal("user-defined group names were translated or hidden")
						}
					}
					if dir := os.Getenv("VRAY_LANGUAGE_PREVIEW"); dir != "" {
						if err := os.MkdirAll(dir, 0755); err != nil {
							t.Fatal(err)
						}
						f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%d-%s.png", language, width, pageRoutes[page].ID)))
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(f, tester.Image())
						_ = f.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		}
	}
}

func TestLocalProxyAndTunTextRowsAlign(t *testing.T) {
	r, svc := homeDashboardFixture(t)
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()
	address, ok := tester.Find("本地代理主要信息")
	if !ok {
		t.Fatal("missing proxy address")
	}
	port, _ := tester.Find("HTTP / SOCKS 端口 2080")
	svc.State().Update(func(sn *app.Snapshot) { sn.Mode = "tun" })
	tester.Frame()
	tun, ok := tester.Find("本地代理主要信息")
	if !ok {
		t.Fatal("missing TUN label")
	}
	detail, _ := tester.Find("通过虚拟网卡接管流量")
	if address.Y != tun.Y || address.H != tun.H || port.Y != detail.Y || port.H != detail.H {
		t.Fatalf("mode switch shifts text rows: proxy=%+v/%+v TUN=%+v/%+v", address, port, tun, detail)
	}
}
