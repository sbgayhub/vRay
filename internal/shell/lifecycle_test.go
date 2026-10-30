package shell

import (
	"testing"
)

// 关闭按钮会隐藏窗口；标记退出后则允许关闭。这是全部规则；一旦弄反，
// 要么窗口无法关闭，要么进程无法退出。
func TestQuitFlagControlsClose(t *testing.T) {
	l := &Lifecycle{closeHides: true}

	if l.quitting {
		t.Fatal("a fresh lifecycle reports quitting")
	}

	// 托盘的 Quit 项会在关闭前设置标志。
	ptr := l.QuittingPtr()
	*ptr = true
	if !l.quitting {
		t.Fatal("marking the flag did not register")
	}
}

// 没有托盘时，关闭窗口不能隐藏，否则将无法恢复。
func TestCloseHidesOff(t *testing.T) {
	l := &Lifecycle{closeHides: true}
	l.CloseHides(false)
	if l.closeHides {
		t.Fatal("CloseHides(false) did not take effect")
	}
}

// 无论有多少条路径请求退出，退出回调都只会执行一次。
//
// 有两条路径会请求退出：关闭按钮（托盘已标记退出后）和系统的 before-quit
// 事件。若恢复逻辑执行两次，会尝试撤销已不存在的更改，第二次失败还会被
// 当作真实问题报告给用户。
func TestQuitWorkRunsOnce(t *testing.T) {
	l := &Lifecycle{}
	var runs int
	l.OnQuit(func() { runs++ })

	l.runQuitWork()
	l.runQuitWork()
	l.runQuitWork()

	if runs != 1 {
		t.Fatalf("the quit callback ran %d times, want 1", runs)
	}
}

// 没有退出回调的生命周期在退出时不能 panic。
func TestQuitWorkWithoutCallback(t *testing.T) {
	l := &Lifecycle{}
	l.runQuitWork()
	if !l.quitRan {
		t.Fatal("the run guard did not engage")
	}
}
