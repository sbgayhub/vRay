package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

// 设置页显示它可编辑的每个已存储值。
func TestSettingsPageShowsStoredValues(t *testing.T) {
	r, svc := newRoot(t)
	v := svc.State().Snapshot().Settings
	v.MixedPort = 17890
	v.LogLevel = "info"
	svc.SaveSettings(v)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && svc.State().Snapshot().Settings.MixedPort != 17890 {
		time.Sleep(5 * time.Millisecond)
	}

	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()
	for _, want := range []string{"连接", "代理模式", "本地端口", "日志级别", "窗口", "数据目录", "选择数据目录", "环境自检", "保存"} {
		if !tr.HasText(want) {
			t.Fatalf("%q is not on screen", want)
		}
	}
	// 用户将看到的端口是已存储的端口。
	if got := r.settingsF.port; got != "17890" {
		t.Fatalf("draft port = %q, want 17890", got)
	}
}

// 非法端口会在对应字段上报错，并阻止保存。
func TestSettingsPageBlocksBadPort(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()

	r.settingsF.port = "70000"
	tr.Frame()
	if r.settingsF.portProblem() == "" {
		t.Fatal("an out-of-range port was accepted")
	}
	if !tr.HasText("端口需在 1–65535 之间") {
		t.Fatal("the port error is not shown")
	}

	r.settingsF.port = "abc"
	if !strings.Contains(r.settingsF.portProblem(), "数字") {
		t.Fatalf("non-numeric port = %q", r.settingsF.portProblem())
	}

	r.settingsF.port = ""
	if r.settingsF.portProblem() == "" {
		t.Fatal("an empty port was accepted")
	}

	r.settingsF.port = "2080"
	if r.settingsF.portProblem() != "" {
		t.Fatalf("a valid port was rejected: %q", r.settingsF.portProblem())
	}
}

// 从页面保存会写入用户输入的端口。
func TestSettingsPageSavesPort(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()

	r.settingsF.port = "18999"
	if r.settingsF.portProblem() != "" {
		t.Fatalf("port rejected: %q", r.settingsF.portProblem())
	}
	svc.SaveSettings(r.settingsF.view())

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if svc.State().Snapshot().Settings.MixedPort == 18999 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("settings = %+v", svc.State().Snapshot().Settings)
}

// 草稿不会跟随快照，Reload 是返回已存储值的方式。
//
// 这里过去断言的是相反行为，而正是该断言放过了真实缺陷：
// 每次快照版本变化都重新同步草稿，导致任何状态变化（例如流量计时刷新、托盘刷新）
// 都会静默丢弃用户的点击。
// 用户的编辑优先；采用别处所做的更改必须是显式操作。
func TestSettingsDraftIsNotResetBySnapshotChanges(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()

	if got := r.settingsF.mode; got != "system-proxy" {
		t.Fatalf("mode = %q", got)
	}

	// 用户选择一种模式。
	r.settingsF.mode = "tun"

	// 页面背后的已存储设置发生了变化。
	svc.SetMode("direct")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && svc.State().Snapshot().Settings.Mode != "direct" {
		time.Sleep(5 * time.Millisecond)
	}
	tr.Frame()
	if got := r.settingsF.mode; got != "tun" {
		t.Fatalf("the draft was reset to %q, want the user's tun to stand", got)
	}

	// Reload 是采用已存储值的显式方式。
	r.settingsF.Reload(svc.State().Snapshot())
	if got := r.settingsF.mode; got != "direct" {
		t.Fatalf("after Reload the draft is %q, want direct", got)
	}
}

// 自检为每一行渲染文字，而不仅依赖颜色。
func TestSelfCheckRendersWords(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
	r.checks = svc.SelfCheck()

	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()
	if !tr.HasText("运行平台") {
		t.Fatal("the platform check is missing")
	}
	// 至少有一项检查正常或需要注意；文字才是承载含义的部分。
	if !tr.HasText("正常") && !tr.HasText("注意") && !tr.HasText("失败") {
		t.Fatal("no check state word is drawn")
	}
}

// checkLabel 为每种状态命名，因此新状态不会只渲染成一个裸标记。
func TestCheckLabelWords(t *testing.T) {
	for _, tc := range []struct {
		state app.CheckState
		want  string
	}{
		{app.CheckOK, "正常"},
		{app.CheckWarn, "注意"},
		{app.CheckFail, "失败"},
	} {
		if got, _ := checkLabel(tc.state, ui.LightTheme()); got != tc.want {
			t.Fatalf("%q = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// 日志级别名称覆盖页面提供的所有选项。
func TestLogLevelNames(t *testing.T) {
	for _, level := range []string{"debug", "info", "warning", "error", "none"} {
		got := logLevelName(level)
		if got == "" || got == level {
			t.Fatalf("%q has no Chinese name: %q", level, got)
		}
	}
}

func TestSettingsHeaderAndActionsStayFixedWhileBodyScrolls(t *testing.T) {
	r, _ := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
	tester := ui.NewTester(r.View, 760, 520)
	tester.Frame()

	header, ok := tester.Find("页面标题栏")
	if !ok || header.H < 50 {
		t.Fatalf("settings header = %+v", header)
	}
	saveBefore, ok := tester.Find("保存")
	if !ok {
		t.Fatal("Save is not in the settings page header")
	}
	cancelBefore, ok := tester.Find("取消")
	if !ok {
		t.Fatal("Cancel is not in the settings page header")
	}
	for label, bounds := range map[string]ui.Rect{"保存": saveBefore, "取消": cancelBefore} {
		if bounds.X < header.X || bounds.X+bounds.W > header.X+header.W+1 ||
			bounds.Y < header.Y || bounds.Y+bounds.H > header.Y+header.H+1 {
			t.Fatalf("%s is outside the settings header: %+v header=%+v", label, bounds, header)
		}
	}

	tester.Scroll(480, 360, 0, 600)
	tester.Frame()
	saveAfter, _ := tester.Find("保存")
	cancelAfter, _ := tester.Find("取消")
	if saveAfter != saveBefore || cancelAfter != cancelBefore {
		t.Fatalf("header actions moved with body scroll: save %+v -> %+v, cancel %+v -> %+v",
			saveBefore, saveAfter, cancelBefore, cancelAfter)
	}
}

// 选择目录后服务写入待迁移状态，并请求应用重启。
func TestChoosingDataDirectoryPersistsAndRequestsRestart(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
	target := filepath.Join(t.TempDir(), "new-data")
	relaunched := make(chan struct{}, 1)
	r.pickDirectory = func(_ *mygo.Window, _, _, _ string) (string, error) {
		return target, nil
	}
	r.relaunch = func() {
		relaunched <- struct{}{}
	}

	r.chooseDataRoot()
	select {
	case <-relaunched:
	case <-time.After(3 * time.Second):
		t.Fatal("选择数据目录后没有请求重启")
	}
	if !svc.State().Snapshot().DataRootPending {
		t.Fatal("选择数据目录后没有进入待迁移状态")
	}

	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()
	if !tr.HasText("数据目录已设置，正在重启并迁移文件…") {
		t.Fatal("设置页没有说明数据目录正在等待迁移")
	}
}

// 固定模式会明确说明为什么不能修改，而不是留下一个无响应的按钮。
func TestDataDirectorySectionExplainsFixedMode(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().Update(func(sn *app.Snapshot) {
		sn.DataRootEditable = false
		sn.DataRootNotice = "当前数据目录由环境变量 VRAY_DATA 固定，无法在应用内修改"
	})
	r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID

	tr := ui.NewTester(r.View, 960, 900)
	tr.Frame()
	if !tr.HasText("VRAY_DATA") {
		t.Fatal("固定模式的原因没有显示")
	}
}
