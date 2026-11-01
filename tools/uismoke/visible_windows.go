package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	vui "github.com/sbgayhub/vray/internal/ui"
)

var visibleUser32 = syscall.NewLazyDLL("user32.dll")
var getDC = visibleUser32.NewProc("GetDC")
var releaseDC = visibleUser32.NewProc("ReleaseDC")
var getWindowRect = visibleUser32.NewProc("GetWindowRect")
var windowThread = visibleUser32.NewProc("GetWindowThreadProcessId")
var attachInput = visibleUser32.NewProc("AttachThreadInput")
var currentThread = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")
var visibleGDI = syscall.NewLazyDLL("gdi32.dll")
var createCompatibleDC = visibleGDI.NewProc("CreateCompatibleDC")
var createDIBSection = visibleGDI.NewProc("CreateDIBSection")
var selectObject = visibleGDI.NewProc("SelectObject")
var bitBlt = visibleGDI.NewProc("BitBlt")
var deleteObject = visibleGDI.NewProc("DeleteObject")
var deleteDC = visibleGDI.NewProc("DeleteDC")

func checkVisible(win *mygo.Window, root *vui.Root, svc *app.Service, out string) error {
	var other *mygo.Window
	mygo.RunOnMain(func() {
		focusVisibleTest(win)
		other = mygo.NewWindow(mygo.WindowOptions{Title: "vRay focus test target", Width: 160, Height: 100, X: 0, Y: 0, Hidden: true})
	})
	defer func() { mygo.RunOnMain(other.Close) }()
	for _, theme := range []string{"light", "dark", "system"} {
		for _, material := range []string{"none", "mica", "micaAlt", "acrylic", "liquidGlass"} {
			svc.State().Update(func(sn *app.Snapshot) {
				sn.Settings.Theme, sn.Settings.Material = theme, material
			})
			for _, kind := range []string{"flyout", "modal"} {
				name := theme + "-" + material + "-" + kind
				mygo.RunOnMain(func() {
					other.Hide()
					focusVisibleTest(win)
				})
				time.Sleep(500 * time.Millisecond)
				if !win.IsVisible() || !win.IsFocused() {
					return fmt.Errorf("%s: main window did not obtain real focus", name)
				}
				// 在记录基线前，通过同一视图导航。
				if _, err := clickVisibleView(win, root, "订阅"); err != nil {
					return err
				}
				time.Sleep(300 * time.Millisecond)
				before := filepath.Join(out, name+"-before.png")
				if err := captureDesktopWindow(win, before); err != nil {
					return err
				}
				label := "外观设置"
				if kind == "modal" {
					label = "添加订阅"
				}
				popup, err := clickVisibleView(win, root, label)
				if err != nil {
					return err
				}
				if popup == nil {
					return fmt.Errorf("%s: popup missing", name)
				}
				time.Sleep(500 * time.Millisecond)
				if !popup.IsVisible() || !popup.IsFocused() || win.IsFocused() {
					return fmt.Errorf("%s: popup did not retain real keyboard focus", name)
				}
				after := filepath.Join(out, name+"-popup.png")
				if err := captureDesktopWindow(win, after); err != nil {
					return err
				}
				difference, err := materialPixelDifference(before, after)
				if err != nil {
					return err
				}
				if difference > 0.5 {
					return fmt.Errorf("%s: parent backdrop visibly changed after popup: mean RGB difference %.3f/255", name, difference)
				}
				// 真实的失活通知可能在弹窗焦点回调之后到达。
				mygo.RunOnMain(func() { sendMessage.Call(win.NativeHandle(), 0x0086, 0, popup.NativeHandle()) })
				time.Sleep(300 * time.Millisecond)
				late := filepath.Join(out, name+"-late-inactive.png")
				if err := captureDesktopWindow(win, late); err != nil {
					return err
				}
				lateDifference, err := materialPixelDifference(before, late)
				if err != nil {
					return err
				}
				if lateDifference > 0.5 || !popup.IsFocused() {
					return fmt.Errorf("%s: late non-client activation changed material/focus: %.3f/255", name, lateDifference)
				}
				mygo.RunOnMain(func() { focusVisibleTest(other) })
				time.Sleep(300 * time.Millisecond)
				if !other.IsFocused() {
					return fmt.Errorf("%s: independent window did not obtain focus", name)
				}
				inactive := filepath.Join(out, name+"-external-focus.png")
				if err := captureDesktopWindow(win, inactive); err != nil {
					return err
				}
				inactiveDifference, err := materialPixelDifference(before, inactive)
				if err != nil {
					return err
				}
				if kind == "modal" {
					mygo.RunOnMain(func() { focusVisibleTest(popup) })
					time.Sleep(300 * time.Millisecond)
					if err := captureDesktopWindow(win, after); err != nil {
						return err
					}
					refocused, err := materialPixelDifference(before, after)
					if err != nil || refocused > 0.5 || !popup.IsFocused() {
						return fmt.Errorf("%s: popup refocus did not restore material: %.3f/255 (%v)", name, refocused, err)
					}
					if err := dismissWithEscape(popup); err != nil {
						return err
					}
				} else if !popup.IsDestroyed() {
					return fmt.Errorf("%s: flyout did not close on outside focus", name)
				}
				fmt.Printf("%s: real popup focus; desktop RGB mean delta=%.3f; late inactive=%.3f; external focus=%.3f/255; lifecycle OK\n", name, difference, lateDifference, inactiveDifference)
			}
		}
	}
	return nil
}

func clickVisibleView(parent *mygo.Window, root *vui.Root, label string) (*mygo.Window, error) {
	var popup *mygo.Window
	var err error
	mygo.RunOnMain(func() {
		tr := ui.NewTester(root.View, 1120, 760)
		tr.SetVibrancy(true)
		tr.Advance(time.Second)
		err = tr.Click(label)
		tr.Advance(time.Second)
		for _, child := range mygo.Windows() {
			if child.Parent() == parent {
				popup = child
			}
		}
	})
	return popup, err
}

func materialPixelDifference(before, after string) (float64, error) {
	read := func(path string) (image.Image, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return png.Decode(f)
	}
	a, err := read(before)
	if err != nil {
		return 0, err
	}
	b, err := read(after)
	if err != nil {
		return 0, err
	}
	if a.Bounds() != b.Bounds() {
		return 0, fmt.Errorf("desktop test window changed size")
	}
	// 两种弹窗下方都稳定的空白区域，排除阴影和文字。
	w, h := a.Bounds().Dx(), a.Bounds().Dy()
	var sum, samples uint64
	for y := 675 * h / 760; y < 715*h/760; y++ {
		for x := 250 * w / 1120; x < 650*w/1120; x++ {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			for _, pair := range [][2]uint32{{ar, br}, {ag, bg}, {ab, bb}} {
				sum += uint64(max(pair[0], pair[1]) - min(pair[0], pair[1]))
				samples++
			}
		}
	}
	return float64(sum) / float64(samples) / 257, nil
}

func focusVisibleTest(window *mygo.Window) {
	foreground, _, _ := getForeground.Call()
	foregroundThread, _, _ := windowThread.Call(foreground, 0)
	testThread, _, _ := currentThread.Call()
	if foregroundThread != 0 && foregroundThread != testThread {
		attached, _, _ := attachInput.Call(testThread, foregroundThread, 1)
		if attached != 0 {
			defer attachInput.Call(testThread, foregroundThread, 0)
		}
	}
	window.Show()
	// 测试工具自身以 STARTF_USESHOWWINDOW/SW_HIDE 启动。Windows 只在第一次调用
	// ShowWindow 时使用该启动设置。
	if !window.IsVisible() {
		window.Show()
	}
	window.Focus()
}

// 在 DWM 合成后，从桌面 DC 复制测试窗口的矩形区域。CapturePage 只会截取
// 我们透明的 UI 表面，不包含 DWM。
func captureDesktopWindow(window *mygo.Window, path string) error {
	return captureDesktopHandle(window.NativeHandle(), path)
}

func captureDesktopHandle(hwnd uintptr, path string) error {
	var bounds struct{ left, top, right, bottom int32 }
	var ok uintptr
	mygo.RunOnMain(func() { ok, _, _ = getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds))) })
	if ok == 0 {
		return fmt.Errorf("GetWindowRect failed")
	}
	w, h := int(bounds.right-bounds.left), int(bounds.bottom-bounds.top)
	screen, _, _ := getDC.Call(0)
	if screen == 0 {
		return fmt.Errorf("desktop DC unavailable")
	}
	defer releaseDC.Call(0, screen)
	dc, _, _ := createCompatibleDC.Call(screen)
	if dc == 0 {
		return fmt.Errorf("capture DC unavailable")
	}
	defer deleteDC.Call(dc)
	info := struct {
		size                   uint32
		width, height          int32
		planes, bits           uint16
		compression, imageSize uint32
		xPixels, yPixels       int32
		used, important        uint32
	}{size: 40, width: int32(w), height: -int32(h), planes: 1, bits: 32}
	var pixels unsafe.Pointer
	bmp, _, _ := createDIBSection.Call(screen, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bmp == 0 {
		return fmt.Errorf("capture bitmap unavailable")
	}
	defer deleteObject.Call(bmp)
	old, _, _ := selectObject.Call(dc, bmp)
	defer selectObject.Call(dc, old)
	ok, _, _ = bitBlt.Call(dc, 0, 0, uintptr(w), uintptr(h), screen, uintptr(bounds.left), uintptr(bounds.top), 0x40cc0020)
	if ok == 0 {
		return fmt.Errorf("desktop copy failed")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	raw := unsafe.Slice((*byte)(pixels), w*h*4)
	for i := 0; i < len(raw); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = raw[i+2], raw[i+1], raw[i], 255
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
