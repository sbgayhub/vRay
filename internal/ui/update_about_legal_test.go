package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

func TestFirstRunAgreementBlocksAndAcceptanceClosesIt(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().Update(func(sn *app.Snapshot) { sn.ConsentRequired = true })
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()

	if !r.consentOpen {
		t.Fatal("first-run agreement did not open")
	}
	for _, text := range []string{
		"免责声明与使用协议",
		"首次使用前请阅读并同意以下条款",
		"不同意并退出",
		"同意并继续",
	} {
		if !tester.HasText(text) {
			t.Fatalf("first-run agreement is missing %q", text)
		}
	}
	if _, found := tester.Find("关闭"); found {
		t.Fatal("blocking agreement exposed a normal close action")
	}
	if err := tester.Click("同意并继续"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && svc.State().Snapshot().ConsentRequired {
		time.Sleep(5 * time.Millisecond)
	}
	tester.Advance(50 * time.Millisecond)
	if svc.State().Snapshot().ConsentRequired || r.consentOpen {
		t.Fatal("agreement remained open after a successful save")
	}
}

func TestSettingsExposeUpdateAboutAndLegalEntries(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Advance(time.Second)

	for _, text := range []string{
		"软件更新",
		"每天 04:00 自动检查更新",
		"立即检查",
		"关于与支持",
		"关于 vRay",
		"免责声明与使用协议",
	} {
		if !tester.HasText(text) {
			t.Fatalf("settings page is missing %q", text)
		}
	}
}

func TestUpdateDialogShowsReleaseNotesAndRestartAction(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().Update(func(sn *app.Snapshot) {
		sn.Update = app.UpdateView{
			AvailableVersion: "0.3.0",
			Notes:            "修复连接稳定性并优化内存占用。",
			ReleaseDate:      time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
			Enabled:          true,
			Installed:        true,
			Downloaded:       100,
			Total:            100,
		}
	})
	r.updateOpen = true
	tester := ui.NewTester(r.View, 1120, 760)
	tester.Frame()

	for _, text := range []string{
		"软件更新",
		"v0.3.0",
		"修复连接稳定性并优化内存占用。",
		"更新已下载并校验完成，重启应用后生效。",
		"立即重启",
	} {
		if !tester.HasText(text) {
			t.Fatalf("update dialog is missing %q", text)
		}
	}
}

func TestAvailableUpdateUsesTheApplicationTitleBar(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().Update(func(sn *app.Snapshot) {
		sn.Update = app.UpdateView{AvailableVersion: "0.3.0", Enabled: true}
	})
	tester := ui.NewTester(r.View, 760, 520)
	tester.Frame()
	if r.updateOpen {
		t.Fatal("an available update opened a dialog without user action")
	}
	if !tester.HasText("更新 v0.3.0") {
		t.Fatal("the title bar has no update action")
	}
	if err := tester.Click("更新 v0.3.0"); err != nil {
		t.Fatal(err)
	}
	if !r.updateOpen {
		t.Fatal("clicking the title-bar update action did not start the update flow")
	}

	svc.State().Update(func(sn *app.Snapshot) {
		sn.Update.Installed = true
		sn.Update.Downloaded = 100
		sn.Update.Total = 100
	})
	tester.Frame()
	if !tester.HasText("重启更新") {
		t.Fatal("a staged update did not become a title-bar restart action")
	}
}
