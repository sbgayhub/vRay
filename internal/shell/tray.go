// Package shell 把 vRay 的窗口接入操作系统：托盘图标、托盘菜单，
// 以及“关闭窗口后代理继续运行”的规则。
//
// 这里的一切都在主线程上运行。托盘回调发生在主线程，读取的状态也来自
// 同一线程的快照，因此除 app.State 自带的同步外无需额外加锁。
package shell

import (
	"embed"
	"fmt"

	"github.com/egoist/mygo"

	"github.com/sbgayhub/vray/internal/app"
)

// IconIdle 返回未连接托盘图标在嵌入文件系统中的路径，供探测命令使用。
func IconIdle() string { return iconIdle }

// IconBytes 读取一个内嵌图标。
func IconBytes(path string) ([]byte, error) { return readIcon(path) }

// Icons 是内嵌的托盘图标；即使可执行文件被移动，图标依然可用。
//
//go:embed icons/*.png
var Icons embed.FS

// 托盘图标状态，每个状态一张图片。
const (
	iconIdle     = "icons/tray-idle.png"
	iconActive   = "icons/tray-active.png"
	iconStarting = "icons/tray-starting.png"
	iconFailed   = "icons/tray-failed.png"
)

// Tray 是系统托盘图标及其菜单。
type Tray struct {
	tray *mygo.Tray
	win  *mygo.Window
	svc  *app.Service

	// quitting 由“退出”菜单项和应用退出事件置位，使关闭流程能放行。
	quitting *bool

	// 状态跟随快照更新的菜单项。
	statusItem *mygo.MenuItem
	toggleItem *mygo.MenuItem

	// lastIcon 与 lastTip 避免冗余的原生调用：Windows 上 SetIcon 会重建
	// 图标，从而产生闪烁。
	lastIcon string
	lastTip  string

	// destroyed 记录 Destroy 是否执行过，让应用能区分“被提前拆除的托盘”
	// 与“只是空闲的托盘”。
	destroyed bool
}

// NewTray 创建托盘图标与菜单。必须在 App.WhenReady 中调用。
func NewTray(win *mygo.Window, svc *app.Service, quitting *bool) (*Tray, error) {
	t := &Tray{win: win, svc: svc, quitting: quitting}

	icon, err := readIcon(iconIdle)
	if err != nil {
		return nil, err
	}

	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "dev.sbgayhub.vray",
		Icon:           icon,
		IconIsTemplate: false,
		Title:          "vRay",
		ToolTip:        "vRay 未连接",
		Menu:           t.buildMenu(),
	})
	if err != nil {
		return nil, fmt.Errorf("外壳：创建托盘失败：%w", err)
	}
	t.tray = tray
	t.lastIcon = iconIdle
	t.lastTip = "vRay 未连接"

	// 左键点击图标恢复窗口，这是窗口隐藏时用户的自然预期。
	tray.OnClick(func() { t.showWindow() })
	return t, nil
}

// buildMenu 构建托盘菜单。
func (t *Tray) buildMenu() *mygo.Menu {
	t.statusItem = &mygo.MenuItem{ID: "status", Label: "未连接", Disabled: true}
	t.toggleItem = &mygo.MenuItem{
		ID:    "toggle",
		Label: "连接",
		Click: func(*mygo.MenuItem, *mygo.Window) { t.svc.Toggle() },
	}

	show := &mygo.MenuItem{
		ID:    "show",
		Label: "打开主窗口",
		Click: func(*mygo.MenuItem, *mygo.Window) { t.showWindow() },
	}
	quit := &mygo.MenuItem{
		ID:    "quit",
		Label: "退出",
		Click: func(*mygo.MenuItem, *mygo.Window) { t.quit() },
	}

	return mygo.NewMenu([]*mygo.MenuItem{
		t.statusItem,
		mygo.Separator(),
		show,
		t.toggleItem,
		mygo.Separator(),
		quit,
	})
}

// Refresh 依据快照更新图标、提示文字和菜单。
//
// 每次状态变更都会调用；廉价的比较把原生调用限制在真正变化的部分。
func (t *Tray) Refresh(snap app.Snapshot) {
	if t.tray == nil {
		return
	}

	icon := iconIdle
	switch snap.Phase {
	case app.PhaseConnected:
		icon = iconActive
	case app.PhaseConnecting, app.PhaseDisconnecting:
		icon = iconStarting
	case app.PhaseFailed:
		icon = iconFailed
	}
	if icon != t.lastIcon {
		// SetIcon 失败时会保留旧图标而不是留下空洞，因此这里无需处理错误。
		if data, err := readIcon(icon); err == nil {
			_ = t.tray.SetIcon(data, false)
			t.lastIcon = icon
		}
	}

	tip := "vRay · " + statusText(snap)
	if tip != t.lastTip {
		t.tray.SetToolTip(tip)
		t.lastTip = tip
	}

	setLabel(t.statusItem, statusText(snap))
	setLabel(t.toggleItem, toggleText(snap))
}

// Destroy 移除图标，可安全地重复调用。
func (t *Tray) Destroy() {
	if t.tray != nil {
		t.tray.Destroy()
		t.tray = nil
	}
	// 即使没有原生图标可移除也要记录：该标记回答的是“托盘是否被拆除”，
	// 而不是“原生调用是否做了事”。
	t.destroyed = true
}

// showWindow 恢复主窗口并聚焦。
func (t *Tray) showWindow() {
	if t.win == nil {
		return
	}
	t.win.Show()
	t.win.Focus()
}

// quit 请求真正关闭窗口。
func (t *Tray) quit() {
	if t.quitting != nil {
		*t.quitting = true
	}
	t.Destroy()
	if t.win != nil {
		t.win.Close()
		return
	}
	mygo.App.Quit()
}

// statusText 生成菜单与提示文字中的连接状态描述。
func statusText(snap app.Snapshot) string {
	switch snap.Phase {
	case app.PhaseConnected:
		name := snap.Active
		for _, p := range snap.Profiles {
			if p.ID == snap.Active {
				name = p.Name
				break
			}
		}
		if name == "" {
			return "已连接"
		}
		return "已连接 · " + name
	case app.PhaseConnecting:
		return "连接中…"
	case app.PhaseDisconnecting:
		return "断开中…"
	case app.PhaseFailed:
		if snap.Problem != nil {
			return "失败：" + snap.Problem.Message
		}
		return "连接失败"
	default:
		return "未连接"
	}
}

// toggleText 是第二个菜单项当前提供的操作。
func toggleText(snap app.Snapshot) string {
	switch snap.Phase {
	case app.PhaseConnected:
		return "断开连接"
	case app.PhaseConnecting, app.PhaseDisconnecting:
		return "请稍候…"
	default:
		return "连接"
	}
}

// setLabel 仅在文字变化时重命名菜单项。
func setLabel(item *mygo.MenuItem, label string) {
	if item == nil || item.Label == label {
		return
	}
	// MenuItem 没有 SetLabel，因此直接写字段并由托盘重建菜单。MyGO 对
	// 结构性变化的建议就是重建，文字变化也属于此类。
	item.Label = label
}

// readIcon 读取一个内嵌图标。
func readIcon(path string) ([]byte, error) {
	data, err := Icons.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("外壳：读取图标 %s：%w", path, err)
	}
	return data, nil
}
