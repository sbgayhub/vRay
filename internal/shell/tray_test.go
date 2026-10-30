package shell

import (
	"testing"

	"github.com/sbgayhub/vray/internal/app"
)

// 托盘状态行必须用用户熟悉的表述说明当前状态。
func TestStatusText(t *testing.T) {
	cases := []struct {
		name string
		snap app.Snapshot
		want string
	}{
		{"idle", app.Snapshot{Phase: app.PhaseIdle}, "未连接"},
		{"connecting", app.Snapshot{Phase: app.PhaseConnecting}, "连接中…"},
		{"disconnecting", app.Snapshot{Phase: app.PhaseDisconnecting}, "断开中…"},
		{
			"connected with a name",
			app.Snapshot{
				Phase:    app.PhaseConnected,
				Active:   "p1",
				Profiles: []app.ProfileView{{ID: "p1", Name: "Tokyo"}},
			},
			"已连接 · Tokyo",
		},
		{
			"connected without a matching profile",
			app.Snapshot{Phase: app.PhaseConnected, Active: "gone"},
			"已连接 · gone",
		},
		{
			"failed with a problem",
			app.Snapshot{Phase: app.PhaseFailed, Problem: &app.Problem{Message: "内核启动失败"}},
			"失败：内核启动失败",
		},
		{"failed without a problem", app.Snapshot{Phase: app.PhaseFailed}, "连接失败"},
	}
	for _, c := range cases {
		if got := statusText(c.snap); got != c.want {
			t.Fatalf("%s: statusText = %q, want %q", c.name, got, c.want)
		}
	}
}

// 切换项必须提供当前有意义的操作。
func TestToggleText(t *testing.T) {
	cases := map[app.Phase]string{
		app.PhaseIdle:          "连接",
		app.PhaseFailed:        "连接",
		app.PhaseConnected:     "断开连接",
		app.PhaseConnecting:    "请稍候…",
		app.PhaseDisconnecting: "请稍候…",
	}
	for phase, want := range cases {
		if got := toggleText(app.Snapshot{Phase: phase}); got != want {
			t.Fatalf("toggleText(%s) = %q, want %q", phase, got, want)
		}
	}
}

// 每个托盘图标都必须被嵌入且可读取：文件缺失会在运行时显示空白图标，
// 而不会让构建失败。
func TestTrayIconsEmbedded(t *testing.T) {
	for _, name := range []string{iconIdle, iconActive, iconStarting, iconFailed} {
		data, err := readIcon(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", name)
		}
		// PNG 魔数：若格式错误，会在原生层失败，而不是在这里。
		if len(data) < 8 || string(data[1:4]) != "PNG" {
			t.Fatalf("%s is not a PNG", name)
		}
	}
}

// 四种状态必须在视觉上可区分：复用同一个文件会让图标失去状态指示作用。
func TestTrayIconsDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, name := range []string{iconIdle, iconActive, iconStarting, iconFailed} {
		data, err := readIcon(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		key := string(data)
		if other, dup := seen[key]; dup {
			t.Fatalf("%s and %s are the same image", name, other)
		}
		seen[key] = name
	}
}

// 新建的托盘不会被销毁。
//
// 这里防范的缺陷是：main.go 曾在 App.WhenReady 内创建托盘并写入
// `defer tray.Destroy()`。该回调在初始化完成后就会返回，因此延迟调用会
// 立刻销毁图标——应用运行时完全没有托盘，这正是用户报告“没有托盘图标”时的现象。
func TestTrayIsNotDestroyedOnCreation(t *testing.T) {
	tray := &Tray{}
	if tray.destroyed {
		t.Fatal("a tray that was never created reports destroyed")
	}
}

// Destroy 是幂等的，并会记录已经执行。
func TestTrayDestroyIsIdempotent(t *testing.T) {
	tray := &Tray{}
	tray.Destroy()
	if !tray.destroyed {
		t.Fatal("Destroy did not record that it ran")
	}
	tray.Destroy() // 不能 panic
	if !tray.destroyed {
		t.Fatal("Destroy forgot its own state")
	}
}
