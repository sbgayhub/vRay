package ui

import (
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/store"
)

func TestHomeChartOnlyUsesRecentThirtySeconds(t *testing.T) {
	r, svc := homeDashboardFixture(t)
	now := time.Now()
	svc.State().Update(func(sn *app.Snapshot) {
		sn.History = []app.TrafficPoint{
			{At: now.Add(-45 * time.Second), DownBps: 1 << 30},
			{At: now.Add(-10 * time.Second), DownBps: 4096},
			{At: now, DownBps: 2048},
		}
	})
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("峰值 "+rateLabel(4096)) || !tester.HasText("30 s") {
		t.Fatal("chart peak included an old sample or did not expose its time range")
	}
	if got := recentTraffic(svc.State().Snapshot().History); len(got) != 2 {
		t.Fatal("chart retained samples outside 30 seconds")
	}
}

func TestTunPermissionDialogIsProminentAndCanBeRepeated(t *testing.T) {
	r, svc := newRoot(t)
	if err := svc.SetModeForTest("tun"); err != nil {
		t.Fatal(err)
	}
	problem := &app.Problem{Code: "TUN_UNAVAILABLE", Message: "TUN 需要管理员权限，请以管理员身份运行 vRay 后重试"}
	svc.State().SetPhase(app.PhaseFailed, problem)
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()
	tester.Advance(time.Second)
	if !r.tunProblemOpen || !tester.HasText("无法启用 TUN") {
		t.Fatal("permission error did not open a dialog")
	}
	if err := tester.Click("知道了"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if r.tunProblemOpen {
		t.Fatal("permission dialog reopened after dismissing")
	}
	svc.State().SetPhase(app.PhaseConnecting, nil)
	svc.State().SetPhase(app.PhaseFailed, problem)
	tester.Frame()
	tester.Advance(time.Second)
	if !r.tunProblemOpen {
		t.Fatal("another connection attempt did not reopen the warning")
	}
	if err := tester.Click("改用系统代理"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for svc.State().Snapshot().Mode != "system-proxy" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if svc.State().Snapshot().Mode != "system-proxy" || r.tunProblemOpen {
		t.Fatal("system-proxy recovery did not work")
	}
}

func TestHomePolicyDisplaysActualNodeAndPersistedExit(t *testing.T) {
	r, svc := homeDashboardFixture(t)
	svc.State().Update(func(sn *app.Snapshot) {
		sn.Groups = []store.PolicyGroup{
			{ID: "main", Name: "我的策略组", Type: "select", Selected: "auto", Members: []string{"auto"}},
			{ID: "auto", Name: "自动选择", Type: "url-test", Members: []string{"p1", "1"}},
		}
		sn.Settings.ActiveGroup = "main"
		sn.Dashboard.PolicySince = sn.Since
		sn.Dashboard.PolicyExits = xcore.PolicyExits{GroupID: "main", Source: "selected", Nodes: []xcore.PolicyExitNode{{ID: "p1"}}}
	})
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()
	if !tester.HasText("203.0.113.28") || !tester.HasText("东京 · 高速 01") || !tester.HasText("Example Transit") {
		t.Fatal("policy card replaced actual node/exit information with the group label")
	}
	if _, ok := tester.Find("更新出口"); !ok {
		t.Fatal("policy exit information cannot be refreshed")
	}
	svc.State().Update(func(sn *app.Snapshot) {
		sn.Dashboard.PolicyExits.Source = "active"
		sn.Dashboard.PolicyExits.Nodes = append(sn.Dashboard.PolicyExits.Nodes, xcore.PolicyExitNode{ID: "1"})
	})
	tester.Frame()
	if !tester.HasText("2 个活动节点") || !tester.HasText("查看连接") {
		t.Fatal("multiple actual exits were presented as one fixed node")
	}
	snap := svc.State().Snapshot()
	snap.Dashboard.PolicyExits.Nodes[0].ID = "corrupt"
	if svc.State().Snapshot().Dashboard.PolicyExits.Nodes[0].ID != "p1" {
		t.Fatal("dashboard snapshot shares policy nodes")
	}
	svc.State().Update(func(sn *app.Snapshot) { sn.Dashboard.PolicySince = sn.Since.Add(-time.Minute) })
	if _, ok := dashboardNode(svc.State().Snapshot()); ok {
		t.Fatal("a previous connection's automatic selection leaked into the current dashboard")
	}
}

func TestImportDialogNamesSourceAndPreventsDuplicateSubmit(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte("vless://11111111-2222-3333-4444-555555555555@127.0.0.1:443#Local"))
	}))
	defer server.Close()
	r, svc := newRoot(t)
	r.cur, r.route = PageSources, pageRoutes[PageSources].ID
	r.importOpen, r.importText, r.importName = true, server.URL, "我的新订阅"
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()
	tester.Advance(time.Second)
	if err := tester.Click("导入"); err != nil {
		close(release)
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("import did not fetch the source")
	}
	tester.Frame()
	if !svc.State().Snapshot().ImportBusy || !tester.HasText("导入中…") {
		close(release)
		t.Fatal("import progress was not visible")
	}
	svc.Import(app.ImportInput{Text: server.URL, Name: "重复请求"})
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for svc.State().Snapshot().ImportBusy && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	snap := svc.State().Snapshot()
	if len(snap.Sources) != 1 || snap.Sources[0].Name != "我的新订阅" || snap.ImportAdded != 1 {
		t.Fatal("named import was duplicated or did not finish")
	}
	tester.Frame()
	if err := tester.Click("完成"); err != nil {
		t.Fatal(err)
	}
	if r.importOpen || r.importText != "" {
		t.Fatal("completed import did not close and clear the form")
	}
}

func TestHomeUpdatesPreviews(t *testing.T) {
	dir := os.Getenv("VRAY_HOME_UPDATES_PREVIEW")
	if dir == "" {
		t.Skip("set VRAY_HOME_UPDATES_PREVIEW to export previews")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"light", "dark"} {
		for _, size := range [][2]int{{1120, 760}, {760, 520}} {
			for _, dialog := range []string{"home", "import", "permission"} {
				t.Run(fmt.Sprintf("%s-%d-%s", theme, size[0], dialog), func(t *testing.T) {
					r, svc := homeDashboardFixture(t)
					svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Theme = theme })
					if dialog == "import" {
						r.importOpen = true
						r.importName = "我的订阅"
					}
					if dialog == "permission" {
						svc.State().SetPhase(app.PhaseFailed, &app.Problem{
							Code: "TUN_UNAVAILABLE", Message: "TUN 需要管理员权限，请以管理员身份运行 vRay 后重试",
						})
					}
					tester := ui.NewTester(r.View, size[0], size[1])
					tester.Frame()
					tester.Advance(time.Second)
					label := ""
					if dialog == "import" {
						label = "导入"
					} else if dialog == "permission" {
						label = "知道了"
					}
					if label != "" {
						bounds, ok := tester.Find(label)
						if !ok || bounds.Y+bounds.H > float32(size[1]) {
							t.Fatalf("dialog action clipped: %+v", bounds)
						}
					}
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%d-%s.png", theme, size[0], dialog)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(f, tester.Image())
					_ = f.Close()
					if err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
