package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

// 点击设置页中的模式按钮后，选择会保留。
//
// 这正是过去缺失的测试：此前所有设置测试都断言草稿字段而不是执行点击，
// 因此“每帧都把草稿重置为已存储值”的缺陷全部通过，而页面按钮毫无作用。
// 该测试通过 Tester 点击，与用户操作一致。
func TestSettingsModeButtonKeepsTheChoice(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID

	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()

	if got := r.settingsF.mode; got != "system-proxy" {
		t.Fatalf("mode starts as %q", got)
	}

	if err := tr.Click("TUN"); err != nil {
		t.Fatalf("click TUN: %v", err)
	}
	if got := r.settingsF.mode; got != "tun" {
		t.Fatalf("mode after clicking TUN = %q, want tun", got)
	}

	// 状态变化不得撤销该选择：过去正是如此，因为每次版本号增加都会重新同步草稿。
	r.svc.State().Update(func(sn *app.Snapshot) { sn.Traffic.UpBps = 1234 })
	tr.Frame()
	if got := r.settingsF.mode; got != "tun" {
		t.Fatalf("mode after a state change = %q, want tun still", got)
	}
}

// 点击日志级别按钮也会保留选择。
func TestSettingsLogLevelButtonKeepsTheChoice(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID

	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()
	if got := r.settingsF.logLevel; got != "warning" {
		t.Fatalf("log level starts as %q", got)
	}

	if err := tr.Click("调试"); err != nil {
		t.Fatalf("click 调试: %v", err)
	}
	if got := r.settingsF.logLevel; got != "debug" {
		t.Fatalf("log level after click = %q, want debug", got)
	}
}

// 设置页上的复选框点击后会切换状态。
func TestSettingsCheckboxToggles(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID

	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()
	before := r.settingsF.closeTray
	tr.Scroll(700, 450, 0, 500)
	tr.Frame()

	if err := tr.Click("关闭窗口时留在托盘继续运行"); err != nil {
		t.Fatalf("click the tray checkbox: %v", err)
	}
	if r.settingsF.closeTray == before {
		t.Fatalf("the tray checkbox did not toggle (still %v)", before)
	}
}

// 保存会写入草稿，且已保存的值在重新加载后仍然存在。
func TestSettingsSaveWritesTheDraft(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID

	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()
	if err := tr.Click("调试"); err != nil {
		t.Fatalf("click 调试: %v", err)
	}
	if err := tr.Click("保存"); err != nil {
		t.Fatalf("click 保存: %v", err)
	}

	deadline := 0
	for deadline < 600 {
		if svc.State().Snapshot().Settings.LogLevel == "debug" {
			return
		}
		deadline++
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("settings = %+v, want logLevel debug", svc.State().Snapshot().Settings)
}

func TestSettingsReportsRejectedSave(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
	tr := ui.NewTester(r.View, 760, 520)
	tr.Frame()
	r.settingsF.probeURL = "https://?missing-host"
	if err := tr.Click("保存"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap := svc.State().Snapshot()
		if !snap.SettingsSaving && snap.SettingsError != "" {
			tr.Frame()
			if tr.HasText("已保存") || !tr.HasText(snap.SettingsError) {
				t.Fatal("rejected settings save was not reported accurately")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("settings failure was not published")
}
