package ui

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/core/logs"
	"github.com/sbgayhub/vray/internal/store"
)

func TestMaterialSurfacesAreTranslucent(t *testing.T) {
	for _, material := range []string{"none", "mica", "micaAlt", "acrylic", "liquidGlass"} {
		for _, available := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/available=%t", material, available), func(t *testing.T) {
				r, svc := newRoot(t)
				svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Material = material })
				tr := ui.NewTester(r.View, 1120, 760)
				tr.SetVibrancy(available)
				tr.Advance(time.Second)
				for _, point := range []image.Point{{X: 80, Y: 450}, {X: 700, Y: 220}} {
					_, _, _, alpha := tr.Image().At(point.X, point.Y).RGBA()
					translucent := alpha > 0 && alpha < 65535
					if translucent != (available && material != "none") {
						t.Fatalf("surface at %v has alpha=%d", point, alpha)
					}
				}
			})
		}
	}
}
func TestSystemAccentFollowsDesktopChanges(t *testing.T) {
	r, _ := newRoot(t)
	var accent ui.Color
	tr := ui.NewTester(func(c *ui.Context) { r.View(c); accent = c.Theme().Accent }, 1120, 760)
	for _, color := range []ui.Color{ui.RGB(219, 39, 119), ui.RGB(250, 204, 21)} {
		tr.SetPreferences(ui.Preferences{Accent: color})
		if accent != color {
			t.Fatalf("accent=%v want %v", accent, color)
		}
	}
}
func TestAppearancePopoverFitsMinimumWindow(t *testing.T) {
	r, _ := newRoot(t)
	tr := ui.NewTester(r.View, 760, 520)
	tr.Frame()
	if err := tr.Click("外观设置"); err != nil {
		t.Fatal(err)
	}
	tr.Advance(time.Second)
	finish, ok := tr.Find("完成")
	if !ok || finish.Y < captionHeight || finish.Y+finish.H > 520 {
		t.Fatalf("popover misplaced: %+v", finish)
	}
	green, ok := tr.Find("绿色")
	if !ok {
		t.Fatal("appearance color choices are missing")
	}
	if green.Y+green.H > 510 {
		tr.Scroll(finish.X, finish.Y+180, 0, 160)
		tr.Frame()
	}
	if err := tr.Click("绿色"); err != nil {
		t.Fatal(err)
	}
	if r.lookSaving != nil {
		<-r.lookSaving
	}
	tr.Frame()
	if r.lookAccent != "#187348" {
		t.Fatal("minimum-size color choice did not respond")
	}
}
func TestNodeSelectionOpensInspectorAndListUsesHeight(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().SetProfiles([]app.ProfileView{
		{ID: "p1", Name: "Tokyo", Protocol: "vless", Address: "jp.example.com", IP: "203.0.113.1", Port: 443, SourceName: "Main subscription"},
		{ID: "p2", Name: "Singapore", Protocol: "trojan", Address: "sg.example.com", IP: "203.0.113.2", Port: 443, SourceName: "Backup subscription"},
	})
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	tr.Advance(time.Second)
	if err := tr.Click("Singapore"); err != nil {
		t.Fatal(err)
	}
	if r.selectedNode != "p2" || !tr.HasText("Backup subscription") {
		t.Fatal("node selection did not update the inspector")
	}
	if tr.HasText("编辑节点") || tr.HasText("删除节点") || tr.HasText("使用") {
		t.Fatal("node rows still show operation buttons")
	}
	for _, height := range []int{760, 1000} {
		tr.SetSize(1120, height)
		bounds, ok := tr.Find("节点列表")
		if !ok || bounds.H < float32(height-330) || bounds.Y+bounds.H > float32(height) {
			t.Fatalf("list bounds at height %d = %+v", height, bounds)
		}
	}
}
func TestPagesHaveNoGlobalImportNodeAction(t *testing.T) {
	r, _ := newRoot(t)
	tr := ui.NewTester(r.View, 1120, 760)
	for page := range pageRoutes {
		r.cur, r.route = Page(page), pageRoutes[page].ID
		tr.Frame()
		if tr.HasText("导入节点") {
			t.Fatalf("page %d still has the global import action", page)
		}
	}
}
func TestSourceEditorAndDeleteThroughUI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("vless://11111111-2222-3333-4444-555555555555@jp.example.com:443#Imported"))
	}))
	defer server.Close()
	r, svc := newRoot(t)
	svc.Import(app.ImportInput{Text: server.URL, Name: "My subscription"})
	result := waitImport(t, svc)
	name := result.Sources[0].Name
	r.cur, r.route = PageSources, pageRoutes[PageSources].ID
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	tr.Advance(time.Second)
	if err := tr.Click("编辑订阅 " + name); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for svc.State().Snapshot().SourceDraft == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	tr.Frame()
	tr.Advance(time.Second)
	if !r.sourceF.open || r.sourceF.draft.URL != server.URL {
		t.Fatal("source editor did not load the saved link")
	}
	r.sourceF.draft.Name = "Renamed subscription"
	if err := tr.Click("保存订阅"); err != nil {
		t.Fatal(err)
	}
	for svc.State().Snapshot().SourceResult != "订阅已保存" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	tr.Frame()
	tr.Advance(time.Second)
	if r.sourceF.open || !tr.HasText("Renamed subscription") {
		t.Fatal("source edit was not reflected on the card")
	}
	if err := tr.Click("删除订阅 Renamed subscription"); err != nil {
		t.Fatal(err)
	}
	tr.Advance(time.Second)
	if err := tr.Click("确认删除订阅"); err != nil {
		t.Fatal(err)
	}
	for len(svc.State().Snapshot().Sources) > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(svc.State().Snapshot().Sources) > 0 {
		t.Fatal("source delete did not finish")
	}
}
func TestReworkRichPreviews(t *testing.T) {
	dir := os.Getenv("VRAY_REWORK_PREVIEW")
	if dir == "" {
		t.Skip("set VRAY_REWORK_PREVIEW to export the visual review fixtures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dark := range []bool{false, true} {
		for _, material := range []string{"none", "mica"} {
			for _, page := range []Page{PageHome, PageNodes, PageSources, PageRules, PageSettings, PageLogs} {
				t.Run(fmt.Sprintf("dark=%t/%s/%s", dark, material, pageRoutes[page].ID), func(t *testing.T) {
					r, svc := newRoot(t)
					r.cur, r.route = page, pageRoutes[page].ID
					appearance := "light"
					if dark {
						appearance = "dark"
					}
					svc.State().Update(func(sn *app.Snapshot) {
						sn.Settings.Theme, sn.Settings.Material = appearance, material
						sn.GeoReady, sn.GeoState = true, "ready"
						sn.Rules = []app.RuleView{{ID: "ads", Name: "广告拦截", Enabled: true, Domains: []string{"geosite:category-ads-all"}, Outbound: "block"}}
						sn.Phase, sn.Active, sn.Since = app.PhaseConnected, "p1", time.Now().Add(-25*time.Minute)
						sn.Traffic = app.Traffic{UpBps: 850 * 1024, DownBps: 4 * 1024 * 1024, TotalUp: 55 << 20, TotalDown: 940 << 20}
						sn.Usage = map[string]store.Transfer{"p1": {Upload: 128 << 20, Download: 9 << 30}}
						sn.Profiles = []app.ProfileView{}
						for i := range 12 {
							sn.Profiles = append(sn.Profiles, app.ProfileView{ID: fmt.Sprintf("p%d", i+1), Name: fmt.Sprintf("Tokyo · Reality %02d", i+1), Protocol: "vless", Address: "jp.example.com", IP: fmt.Sprintf("203.0.113.%d", i+1), Port: 443, SourceID: "s1", SourceName: "主力订阅"})
						}
						sn.Sources = []app.SourceView{
							{ID: "s1", Name: "主力订阅", Host: "subscription.example.com", Enabled: true, NodeCount: 12, Upload: 5 << 30, Download: 64 << 30, Total: 200 << 30, ExpireAt: time.Now().Add(60 * 24 * time.Hour), LastFetchedAt: time.Now().Add(-30 * time.Minute)},
							{ID: "s2", Name: "备用线路", Host: "backup.example.com", Enabled: true, NodeCount: 8, Upload: 1 << 30, Download: 12 << 30, Total: 100 << 30, ExpireAt: time.Now().Add(30 * 24 * time.Hour), LastFetchedAt: time.Now().Add(-3 * time.Hour)},
						}
						sn.History = []app.TrafficPoint{}
						for i := range 100 {
							sn.History = append(sn.History, app.TrafficPoint{At: time.Now().Add(time.Duration(i-100) * 500 * time.Millisecond), UpBps: int64((i*13%80 + 10) * 1024), DownBps: int64((i*77%400 + 20) * 1024)})
						}
						sn.NodeDetails = map[string]app.IPDetails{"p1": {IP: "203.0.113.1", Country: "Japan", Region: "Tokyo", ASN: "AS64500 · Example", Provider: "Example provider", Type: "数据中心", Score: "0.01 (Low)"}}
					})
					if page == PageLogs {
						svc.AttachLogs(&memLogs{entries: []logs.Entry{
							{At: time.Now(), Level: logs.LevelWarning, Text: "proxy/vless/outbound: failed to dial tcp:jp.example.com:443 > connection refused"},
							{At: time.Now(), Level: logs.LevelError, Text: "transport/internet/tcp: failed to establish connection > i/o timeout"},
						}})
					}
					tr := ui.NewTester(r.View, 1280, 820)
					tr.SetVibrancy(material != "none")
					tr.SetDark(dark)
					tr.Advance(2 * time.Second)
					if page == PageSettings {
						if err := tr.Click("外观设置"); err != nil {
							t.Fatal(err)
						}
						tr.Advance(time.Second)
					}
					name := appearance + "-" + material + "-" + pageRoutes[page].ID + ".png"
					f, err := os.Create(filepath.Join(dir, name))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tr.Image()); err != nil {
						t.Fatal(err)
					}
					if page == PageSources {
						for _, kind := range []string{"import", "edit"} {
							if kind == "import" {
								if err := tr.Click("添加订阅"); err != nil {
									t.Fatal(err)
								}
							} else {
								r.importOpen = false
								r.sourceF = sourceForm{open: true, draft: app.SourceEdit{Name: "主力订阅", URL: "https://subscription.example.com/link", Enabled: true}, interval: "180"}
								tr.Frame()
							}
							tr.Advance(time.Second)
							file, err := os.Create(filepath.Join(dir, appearance+"-"+material+"-sources-"+kind+".png"))
							if err != nil {
								t.Fatal(err)
							}
							err = png.Encode(file, tr.Image())
							file.Close()
							if err != nil {
								t.Fatal(err)
							}
						}
					}
				})
			}
		}
	}
}

func TestPageActionsBelongToPageHeading(t *testing.T) {
	for _, page := range []Page{PageNodes, PageSources} {
		r, _ := newRoot(t)
		r.cur, r.route = page, pageRoutes[page].ID
		tr := ui.NewTester(r.View, 1120, 760)
		tr.Advance(time.Second)
		action := "延迟测速"
		if page == PageSources {
			action = "添加订阅"
		}
		bounds, ok := tr.Find(action)
		if !ok || bounds.Y < captionHeight || bounds.Y > 100 {
			t.Fatalf("%s outside page heading: %+v", action, bounds)
		}
		if page == PageSources {
			if tr.HasText("粘贴分享链接") {
				t.Fatal("import form shown inline before opening")
			}
			if err := tr.Click(action); err != nil {
				t.Fatal(err)
			}
			tr.Advance(time.Second)
			if !tr.HasText("粘贴分享链接") || !tr.HasText("关闭") {
				t.Fatal("import modal not opened")
			}
		}
	}
}
func TestLogsDefaultToConfiguredThreshold(t *testing.T) {
	r, _ := newLogsRoot(t)
	r.logRaw = false
	r.svc.State().Update(func(sn *app.Snapshot) { sn.Settings.LogLevel = "warning" })
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	if tr.HasText("Xray 26.3.27 started") {
		t.Fatal("info shown below configured warning threshold")
	}
	if !tr.HasText("连接被拒绝") {
		t.Fatal("missing Chinese log description")
	}
	r.svc.State().Update(func(sn *app.Snapshot) { sn.Settings.LogLevel = "none" })
	tr.Frame()
	if tr.HasText("连接被拒绝") || tr.HasText("connection refused") {
		t.Fatal("logs still visible when logging is disabled")
	}
}
func TestChineseLogSearchAndOriginalToggle(t *testing.T) {
	r, _ := newLogsRoot(t)
	r.logRaw, r.logQuery = false, "连接被拒绝"
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Advance(time.Second)
	if !tr.HasText("连接被拒绝") || tr.HasText("拨号失败") {
		t.Fatal("Chinese search did not narrow log entries")
	}
	if err := tr.Click("原始日志"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if !tr.HasText("connection refused") {
		t.Fatal("original message was not preserved")
	}
}
func TestPopupMaterialBlursPageBehindIt(t *testing.T) {
	r := &Root{materialVisible: true}
	tr := ui.NewTester(func(c *ui.Context) {
		c.Root().Background(ui.RGB(255, 255, 255))
		ui.Row(c).Size(128, 128).Children(func() {
			for i := range 32 {
				color := ui.RGB(0, 0, 0)
				if i%2 == 0 {
					color = ui.RGB(255, 255, 255)
				}
				ui.Box(c).Width(4).FillHeight().Background(color)
			}
		})
		ui.Overlay(c, func() {
			panel := ui.Box(c).Absolute().Left(0).Top(0).Size(128, 128)
			r.popupSurface(c, panel)
		})
	}, 128, 128)
	tr.Frame()
	a, _, _, _ := tr.Image().At(62, 64).RGBA()
	b, _, _, _ := tr.Image().At(66, 64).RGBA()
	if math.Abs(float64(a)-float64(b))/257 > 5 {
		t.Fatalf("sharp stripes survived popup backdrop blur: %d, %d", a/257, b/257)
	}
}
func TestGeoTemplateOpensEditorAndSaves(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageRules, pageRoutes[PageRules].ID
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Advance(time.Second)
	if !tr.HasText("局域网直连") || !tr.HasText("国内域名直连") {
		t.Fatal("built-in routing is hidden")
	}
	if err := tr.Click("广告拦截"); err != nil {
		t.Fatal(err)
	}
	tr.Advance(time.Second)
	if !r.ruleF.open || !tr.HasText("规则库未就绪") {
		t.Fatal("Geo template editor or missing-data notice absent")
	}
	if err := tr.Click("保存"); err != nil {
		t.Fatal(err)
	}
	rules := waitRules(t, svc, 1)
	if rules[0].Domains[0] != "geosite:category-ads-all" || rules[0].Outbound != "block" {
		t.Fatalf("saved template = %+v", rules)
	}
}
func TestGeoEditorFitsMinimumWindow(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageRules, pageRoutes[PageRules].ID
	r.ruleF.blankRule()
	r.ruleF.name, r.ruleF.domains = "广告拦截", "geosite:category-ads-all"
	tr := ui.NewTester(r.View, 760, 520)
	tr.Advance(time.Second)
	bounds, ok := tr.Find("保存")
	if !ok || bounds.Y < 0 || bounds.Y+bounds.H > 510 {
		t.Fatalf("save button outside minimum window: %+v", bounds)
	}
}
func TestPopupsShareWindowMaterial(t *testing.T) {
	for _, material := range []string{"none", "mica"} {
		for _, popup := range []string{"appearance", "subscription"} {
			t.Run(fmt.Sprintf("%s/%s", material, popup), func(t *testing.T) {
				r, svc := newRoot(t)
				svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Material = material })
				tr := ui.NewTester(r.View, 1120, 760)
				tr.SetVibrancy(true)
				if popup == "appearance" {
					if err := tr.Click("外观设置"); err != nil {
						t.Fatal(err)
					}
				} else {
					r.sourceF = sourceForm{open: true, draft: app.SourceEdit{Name: "Test", URL: "https://example.com"}, interval: "0"}
					tr.Frame()
				}
				tr.Advance(time.Second)
				label := "个性化外观"
				if popup == "subscription" {
					label = "编辑订阅"
				}
				bounds, ok := tr.Find(label)
				if !ok {
					t.Fatal("popup missing")
				}
				x, y := int(bounds.X)-8, int(bounds.Y)+55
				if popup == "subscription" {
					x, y = int(bounds.X)+8, int(bounds.Y)+60
				}
				_, _, _, alpha := tr.Image().At(x, y).RGBA()
				if translucent := alpha > 0 && alpha < 65535; translucent != (material != "none") {
					t.Fatalf("popup alpha=%d at %d,%d; bounds=%+v", alpha, x, y, bounds)
				}
				if material != "none" && alpha < 59000 {
					t.Fatalf("popup has insufficient coverage for readable controls: alpha=%d", alpha)
				}
			})
		}
	}
}
