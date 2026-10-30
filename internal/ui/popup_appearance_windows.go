//go:build windows

package ui

import (
	"syscall"

	"github.com/egoist/mygo"
)

const (
	wmNCActivate         = 0x0086
	wmNCDestroy          = 0x0082
	appearanceSubclassID = 0x76526179
)

var appearanceComctl32 = syscall.NewLazyDLL("comctl32.dll")
var setAppearanceSubclass = appearanceComctl32.NewProc("SetWindowSubclass")
var removeAppearanceSubclass = appearanceComctl32.NewProc("RemoveWindowSubclass")
var defAppearanceSubclass = appearanceComctl32.NewProc("DefSubclassProc")
var sendAppearanceMessage = syscall.NewLazyDLL("user32.dll").NewProc("SendMessageW")
var appearanceCallback uintptr

// 仅在 UI 线程访问。使用进程生命周期的单一回调，避免
// 为每个弹窗分配 Windows 回调跳板。
var heldParentAppearances = make(map[uintptr]bool)

func parentAppearanceProc(hwnd, message, wp, lp, id, _ uintptr) uintptr {
	if message == wmNCActivate && heldParentAppearances[hwnd] {
		// DWM 根据非客户区激活状态绘制背景。WM_ACTIVATE 以及
		// 所有键盘和输入消息仍会完整到达原始窗口过程。
		wp = 1
	}
	if message == wmNCDestroy {
		delete(heldParentAppearances, hwnd)
		removeAppearanceSubclass.Call(hwnd, appearanceCallback, id)
	}
	result, _, _ := defAppearanceSubclass.Call(hwnd, message, wp, lp)
	return result
}

func parentAppearanceSetter(parent *mygo.Window) func(bool) {
	if appearanceCallback == 0 {
		appearanceCallback = syscall.NewCallback(parentAppearanceProc)
	}
	hwnd := parent.NativeHandle()
	return func(keep bool) {
		if parent.IsDestroyed() || heldParentAppearances[hwnd] == keep {
			return
		}
		if keep {
			ok, _, _ := setAppearanceSubclass.Call(hwnd, appearanceCallback, appearanceSubclassID, 0)
			if ok == 0 {
				return
			}
			heldParentAppearances[hwnd] = true
		} else {
			delete(heldParentAppearances, hwnd)
			removeAppearanceSubclass.Call(hwnd, appearanceCallback, appearanceSubclassID)
		}
		var active uintptr
		if keep || parent.IsFocused() {
			active = 1
		}
		// 立即刷新 DWM，同时处理随后真实发生的
		// WM_NCACTIVATE(FALSE)。此操作绝不会激活或聚焦父窗口。
		sendAppearanceMessage.Call(hwnd, wmNCActivate, active, 0)
	}
}
