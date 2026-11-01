package shell

import (
	"log/slog"

	"github.com/egoist/mygo"
)

// Lifecycle 决定关闭窗口意味着什么。
//
// 关闭窗口只是隐藏窗口并保持代理运行：窗口一关就停止代理的客户端没法用。
// 只有托盘的“退出”菜单项或系统退出才会结束进程。
type Lifecycle struct {
	win      *mygo.Window
	quitting bool
	// quitRan 保证退出回调恰好执行一次。
	quitRan bool

	// closeHides 表示关闭窗口时是隐藏窗口而不是退出。没有托盘时必须为
	// false：藏起来却没有回来的入口，进程就困在没有窗口的状态。
	closeHides bool

	// startedHidden 记录应用是随系统登录启动的，在被唤起前窗口保持隐藏。
	startedHidden bool

	// onQuit 在进程结束前恰好执行一次，是系统变更的第二层补偿：
	// 托盘或系统发起的退出必须还原变更，而且必须在内核还活着时完成，
	// 否则还原没有意义。
	onQuit func()
}

// OnQuit 注册进程退出前执行的工作。
//
// 即使多处同时请求退出，回调也至多执行一次：跑两遍还原会试图撤销
// 已经不存在的变更。
func (l *Lifecycle) OnQuit(fn func()) { l.onQuit = fn }

// runQuitWork 执行退出回调，至多一次。
func (l *Lifecycle) runQuitWork() {
	if l.quitRan {
		return
	}
	l.quitRan = true
	if l.onQuit != nil {
		l.onQuit()
	}
}

// NewLifecycle 接线窗口的关闭行为。
func NewLifecycle(win *mygo.Window) *Lifecycle {
	l := &Lifecycle{
		win:           win,
		closeHides:    true,
		startedHidden: mygo.App.WasOpenedAtLogin(),
	}

	win.OnClose(func(e *mygo.CloseEvent) {
		if l.quitting || !l.closeHides {
			// 真正的退出：在进程结束前先还原。
			l.runQuitWork()
			return // 放行关闭
		}
		e.PreventDefault()
		win.Hide()
	})

	// 系统发起的退出（菜单、注销）同样不能被拦截。
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		l.quitting = true
		l.runQuitWork()
	})

	return l
}

// QuittingPtr 暴露该标记给托盘的“退出”菜单项：它必须先标记为真正退出，
// 再请求窗口关闭。
func (l *Lifecycle) QuittingPtr() *bool { return &l.quitting }

// CloseHides 设置关闭按钮是否改为隐藏窗口。
func (l *Lifecycle) CloseHides(hide bool) { l.closeHides = hide }

// StartedHidden 返回应用是否随登录启动。
func (l *Lifecycle) StartedHidden() bool { return l.startedHidden }

// SetOpenAtLogin 开启或关闭随系统启动。
func SetOpenAtLogin(on bool) error {
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		slog.Error("设置开机自启失败", "错误", err)
		return err
	}
	return nil
}
