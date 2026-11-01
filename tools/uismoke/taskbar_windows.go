package main

import (
	"fmt"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
)

func checkTaskbar(window *mygo.Window, out string) error {
	if window.IsVisible() {
		return fmt.Errorf("taskbar test window was displayed before assigning its icon")
	}
	taskbarName, err := syscall.UTF16PtrFromString("Shell_TrayWnd")
	if err != nil {
		return err
	}
	mygo.RunOnMain(func() { focusVisibleTest(window) })
	time.Sleep(800 * time.Millisecond)
	if !window.IsVisible() || !window.IsFocused() {
		return fmt.Errorf("taskbar check requires the test window to be visible and focused")
	}
	var small, big, owner, styles, taskbar uintptr
	mygo.RunOnMain(func() {
		hwnd := window.NativeHandle()
		small, _, _ = sendMessage.Call(hwnd, 0x007f, 0, 0)                                 // WM_GETICON：小图标
		big, _, _ = sendMessage.Call(hwnd, 0x007f, 1, 0)                                   // WM_GETICON：大图标
		owner, _, _ = visibleUser32.NewProc("GetWindow").Call(hwnd, 4)                     // GW_OWNER：所有者窗口
		styles, _, _ = visibleUser32.NewProc("GetWindowLongPtrW").Call(hwnd, ^uintptr(19)) // GWL_EXSTYLE=-20：扩展样式
		taskbar, _, _ = visibleUser32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(taskbarName)), 0)
	})
	fmt.Printf(
		"visible main window: hwnd=%#x smallIcon=%#x bigIcon=%#x owner=%#x exStyle=%#x\n",
		window.NativeHandle(),
		small,
		big,
		owner,
		styles,
	)
	if taskbar == 0 {
		return fmt.Errorf("未找到资源管理器任务栏")
	}
	if err := captureDesktopHandle(taskbar, filepath.Join(out, "taskbar.png")); err != nil {
		return err
	}
	if small == 0 || big == 0 {
		return fmt.Errorf("main window has no runtime application icon")
	}
	if owner != 0 || styles&0x80 != 0 { // WS_EX_TOOLWINDOW：工具窗口
		return fmt.Errorf("main window is owned or marked as a tool window")
	}
	fmt.Println("main window has small/big icons and is eligible for a taskbar button")
	mygo.RunOnMain(window.Hide)
	time.Sleep(200 * time.Millisecond)
	if window.IsVisible() {
		return fmt.Errorf("close-to-tray hide did not hide the window")
	}
	mygo.RunOnMain(func() { focusVisibleTest(window) })
	time.Sleep(500 * time.Millisecond)
	var restoredSmall, restoredBig uintptr
	mygo.RunOnMain(func() {
		restoredSmall, _, _ = sendMessage.Call(window.NativeHandle(), 0x007f, 0, 0)
		restoredBig, _, _ = sendMessage.Call(window.NativeHandle(), 0x007f, 1, 0)
	})
	if !window.IsVisible() || restoredSmall != small || restoredBig != big {
		return fmt.Errorf("restoring the window lost its application icons")
	}
	if err := captureDesktopHandle(taskbar, filepath.Join(out, "taskbar-restored.png")); err != nil {
		return err
	}
	mygo.RunOnMain(window.Minimize)
	time.Sleep(300 * time.Millisecond)
	if !window.IsMinimized() {
		return fmt.Errorf("test window did not minimize")
	}
	if err := captureDesktopHandle(taskbar, filepath.Join(out, "taskbar-minimized.png")); err != nil {
		return err
	}
	fmt.Println("hide/show retains both icon handles; minimized taskbar screenshot captured")
	return nil
}
