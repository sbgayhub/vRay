// Command uismoke 在隔离的隐藏窗口中检查原生外观变化。它从不启动 Xray，也不
// 更改系统代理或桌面主题。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/ELY-Open-Source/CrychicUI/sound"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	xcore "github.com/sbgayhub/vray/internal/core"
	vui "github.com/sbgayhub/vray/internal/ui"
	"github.com/sbgayhub/vray/resources"
)

var getAttribute = syscall.NewLazyDLL("dwmapi.dll").NewProc("DwmGetWindowAttribute")
var sendMessage = syscall.NewLazyDLL("user32.dll").NewProc("SendMessageW")
var findChild = syscall.NewLazyDLL("user32.dll").NewProc("FindWindowExW")
var getForeground = syscall.NewLazyDLL("user32.dll").NewProc("GetForegroundWindow")

func main() {
	out := flag.String("out", "build/native-ui", "directory for rendered PNGs")
	visible := flag.Bool(
		"visible",
		false,
		"check real activation and desktop-composited pixels in isolated visible windows",
	)
	taskbar := flag.Bool("taskbar", false, "check a visible main window's taskbar icon and capture the taskbar")
	flag.Parse()
	if err := run(*out, *visible || *taskbar, *taskbar); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out string, visible, taskbar bool) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "vray-ui-smoke-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	svc, _ := app.NewFake(temp)
	if err := svc.Load(); err != nil {
		return err
	}
	sound.SetEnabled(false)
	result := make(chan error, 1)
	mygo.App.WhenReady(func() {
		root := vui.NewRoot(svc)
		win := mygo.NewWindow(mygo.WindowOptions{
			Title: "vRay UI smoke", Width: 1120, Height: 760, Hidden: true,
			X: 80, Y: 60,
			TitleBarStyle: mygo.TitleBarHidden, HideWindowButtons: true,
			Vibrancy: mygo.VibrancyMica, Content: ui.View(root.View),
		})
		if err := win.SetIcon(resources.Icon); err != nil {
			result <- fmt.Errorf("set main window icon: %w", err)
			mygo.App.Quit()
			return
		}
		root.Attach(win)
		svc.State().SetOnChange(win.Invalidate)
		go func() {
			if taskbar {
				result <- checkTaskbar(win, out)
			} else if visible {
				result <- checkVisible(win, root, svc, out)
			} else {
				result <- check(win, root, svc, out)
			}
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		return err
	}
	return <-result
}

func check(win *mygo.Window, root *vui.Root, svc *app.Service, out string) error {
	prefs := mygo.Theme.Preferences()
	fmt.Printf("system: reduceTransparency=%t highContrast=%t\n", prefs.ReduceTransparency, prefs.HighContrast)
	for _, theme := range []string{"light", "dark", "system"} {
		for _, material := range []string{"none", "mica", "micaAlt", "acrylic", "liquidGlass"} {
			svc.State().Update(func(sn *app.Snapshot) {
				sn.Settings.Theme, sn.Settings.Material = theme, material
			})
			// 隐藏窗口被遮挡后会停止绘制。在 UI 线程上驱动同一视图；
			// 其附加的真实窗口仍会接收原生 API。
			var rendered bytes.Buffer
			var renderErr error
			var flyout *mygo.Window
			mygo.RunOnMain(func() {
				tr := ui.NewTester(root.View, 1120, 760)
				tr.SetVibrancy(material != "none" && !prefs.ReduceTransparency && !prefs.HighContrast)
				tr.SetDark(mygo.Theme.IsDark())
				if prefs.Accent.A > 0 {
					tr.SetPreferences(ui.Preferences{Accent: ui.RGB(prefs.Accent.R, prefs.Accent.G, prefs.Accent.B)})
				}
				tr.Frame()
				tr.Advance(time.Second)
				if err := tr.Click("外观设置"); err != nil {
					renderErr = err
					return
				}
				tr.Advance(time.Second)
				for _, child := range mygo.Windows() {
					if child.Parent() == win && child.IsFlyout() {
						flyout = child
					}
				}
				if flyout == nil {
					renderErr = fmt.Errorf("appearance did not create a native flyout")
					return
				}
				renderErr = png.Encode(&rendered, tr.Image())
			})
			if renderErr != nil {
				return renderErr
			}
			if mygo.Theme.Source() != mygo.ThemeSource(theme) {
				return fmt.Errorf("theme %s was not applied", theme)
			}
			var backdrop uint32
			var hr uintptr
			mygo.RunOnMain(func() {
				hr, _, _ = getAttribute.Call(win.NativeHandle(), 38, uintptr(unsafe.Pointer(&backdrop)), 4)
			})
			if int32(hr) < 0 {
				return fmt.Errorf("DWM backdrop query failed: %#x", hr)
			}
			want := map[string]uint32{"none": 1, "mica": 2, "micaAlt": 4, "acrylic": 3, "liquidGlass": 3}[material]
			if prefs.ReduceTransparency || prefs.HighContrast {
				want = 1
			}
			if backdrop != want {
				return fmt.Errorf("%s/%s backdrop=%d, want %d", theme, material, backdrop, want)
			}
			popupBackdrop, err := backdropOf(flyout)
			if err != nil {
				return err
			}
			if popupBackdrop != want {
				return fmt.Errorf("%s/%s flyout backdrop=%d, want %d", theme, material, popupBackdrop, want)
			}
			if err := verifyParentActivation(win, flyout, true, want); err != nil {
				return err
			}
			popupPNG, err := captureFittedPopup(flyout)
			if err != nil {
				return fmt.Errorf("capture native flyout: %w", err)
			}
			if err := verifyPopupAlpha(popupPNG, want != 1); err != nil {
				return err
			}
			if err := verifyPopupWhitespace(popupPNG); err != nil {
				return err
			}
			if err := dismissWithEscape(flyout); err != nil {
				return err
			}
			var modal *mygo.Window
			mygo.RunOnMain(func() {
				tr := ui.NewTester(root.View, 1120, 760)
				tr.SetVibrancy(material != "none" && !prefs.ReduceTransparency && !prefs.HighContrast)
				tr.Advance(time.Second)
				if err := tr.Click("订阅"); err != nil {
					renderErr = err
					return
				}
				if err := tr.Click("添加订阅"); err != nil {
					renderErr = err
					return
				}
				tr.Advance(time.Second)
				_, _, _, alpha := tr.Image().At(900, 650).RGBA()
				if want != 1 && alpha != 0 {
					renderErr = fmt.Errorf("modal covers parent material with a dimming layer: alpha=%d", alpha)
					return
				}
				for _, child := range mygo.Windows() {
					if child.Parent() == win && !child.IsFlyout() {
						modal = child
					}
				}
				if modal == nil {
					renderErr = fmt.Errorf("subscription did not create a native modal")
				}
			})
			if renderErr != nil {
				return renderErr
			}
			modalBackdrop, err := backdropOf(modal)
			if err != nil {
				return err
			}
			if modalBackdrop != want {
				return fmt.Errorf("%s/%s modal backdrop=%d, want %d", theme, material, modalBackdrop, want)
			}
			if err := verifyParentActivation(win, modal, true, want); err != nil {
				return err
			}
			if err := verifyParentActivation(win, modal, false, want); err != nil {
				return err
			}
			if err := verifyParentActivation(win, modal, true, want); err != nil {
				return err
			}
			modalPNG, err := captureFittedPopup(modal)
			if err != nil {
				return fmt.Errorf("capture native modal: %w", err)
			}
			if err := verifyPopupAlpha(modalPNG, want != 1); err != nil {
				return err
			}
			if err := verifyPopupWhitespace(modalPNG); err != nil {
				return err
			}
			if err := dismissWithEscape(modal); err != nil {
				return err
			}
			name := theme + "-" + material
			if err := os.WriteFile(filepath.Join(out, name+".png"), rendered.Bytes(), 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, name+"-flyout.png"), popupPNG, 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, name+"-modal.png"), modalPNG, 0o644); err != nil {
				return err
			}
			fmt.Printf(
				"%s: native backdrop=%d flyout=%d modal=%d; "+
					"popup focus/blur leaves parent backdrop and foreground unchanged, rendered %d bytes\n",
				name,
				backdrop,
				popupBackdrop,
				modalBackdrop,
				rendered.Len(),
			)
		}
	}
	if err := checkLanguagePopup(win, root, out); err != nil {
		return err
	}
	return checkTunPermission(win, root, svc, out)
}

func checkTunPermission(parent *mygo.Window, root *vui.Root, svc *app.Service, out string) error {
	problem := xcore.New().CheckMode("tun")
	if problem == nil {
		fmt.Println("TUN warning: skipped, process already has permission and driver")
		return nil
	}
	svc.State().SetPhase(app.PhaseFailed, &app.Problem{Code: "TUN_UNAVAILABLE", Message: problem.Error()})
	var popup *mygo.Window
	mygo.RunOnMain(func() {
		tester := ui.NewTester(root.View, 1120, 760)
		tester.Frame()
		for _, window := range mygo.Windows() {
			if window.Parent() == parent && !window.IsFlyout() {
				popup = window
			}
		}
	})
	if popup == nil {
		return fmt.Errorf("real TUN preflight error did not create a native dialog")
	}
	backdrop, err := backdropOf(parent)
	if err != nil {
		return err
	}
	modalBackdrop, err := backdropOf(popup)
	if err != nil {
		return err
	}
	if backdrop != modalBackdrop {
		return fmt.Errorf("TUN dialog material differs from parent")
	}
	raw, err := captureFittedPopup(popup)
	if err != nil {
		return err
	}
	if err := verifyPopupAlpha(raw, backdrop != 1); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "tun-permission.png"), raw, 0644); err != nil {
		return err
	}
	if err := dismissWithEscape(popup); err != nil {
		return err
	}
	fmt.Println("TUN warning: real preflight error, native modal material and content fit verified")
	return nil
}

func checkLanguagePopup(parent *mygo.Window, root *vui.Root, out string) error {
	var popup *mygo.Window
	var renderErr error
	mygo.RunOnMain(func() {
		tester := ui.NewTester(root.View, 1120, 760)
		tester.Frame()
		if renderErr = tester.Click("切换语言"); renderErr != nil {
			return
		}
		tester.Frame()
		for _, child := range mygo.Windows() {
			if child.Parent() == parent && child.IsFlyout() {
				popup = child
			}
		}
	})
	if renderErr != nil {
		return renderErr
	}
	if popup == nil {
		return fmt.Errorf("language settings did not create a native flyout")
	}
	parentBackdrop, err := backdropOf(parent)
	if err != nil {
		return err
	}
	popupBackdrop, err := backdropOf(popup)
	if err != nil {
		return err
	}
	if parentBackdrop != popupBackdrop {
		return fmt.Errorf("language flyout lost the parent's material")
	}
	raw, err := captureFittedPopup(popup)
	if err != nil {
		return err
	}
	if err := verifyPopupAlpha(raw, parentBackdrop != 1); err != nil {
		return err
	}
	if err := verifyParentActivation(parent, popup, true, parentBackdrop); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "language-flyout.png"), raw, 0644); err != nil {
		return err
	}
	if err := dismissWithEscape(popup); err != nil {
		return err
	}
	fmt.Println("language: native material and content fit verified")
	return nil
}

func verifyParentActivation(parent, popup *mygo.Window, active bool, backdrop uint32) error {
	var before, after uintptr
	mygo.RunOnMain(func() {
		before, _, _ = getForeground.Call()
		var state uintptr
		if active {
			state = 1
			// 不显示隐藏辅助窗口，复现所有者窗口失活。
			sendMessage.Call(parent.NativeHandle(), 0x0006, 0, popup.NativeHandle())
		}
		sendMessage.Call(popup.NativeHandle(), 0x0006, state, 0)
		after, _, _ = getForeground.Call()
	})
	if before != after {
		return fmt.Errorf("popup appearance changed the foreground window")
	}
	value, err := backdropOf(parent)
	if err != nil {
		return err
	}
	if value != backdrop {
		return fmt.Errorf("popup activation changed parent backdrop from %d to %d", backdrop, value)
	}
	return nil
}

func backdropOf(window *mygo.Window) (uint32, error) {
	var value uint32
	var hr uintptr
	mygo.RunOnMain(func() { hr, _, _ = getAttribute.Call(window.NativeHandle(), 38, uintptr(unsafe.Pointer(&value)), 4) })
	if int32(hr) < 0 {
		return 0, fmt.Errorf("native popup DWM query failed: %#x", hr)
	}
	return value, nil
}

func verifyPopupAlpha(raw []byte, material bool) error {
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	_, _, _, alpha := img.At(4, 4).RGBA()
	if material && alpha > 6553 {
		return fmt.Errorf("native material is covered by an opaque popup background: alpha=%d", alpha)
	}
	if !material && alpha != 65535 {
		return fmt.Errorf("plain popup background should be opaque: alpha=%d", alpha)
	}
	return nil
}

func captureFittedPopup(window *mygo.Window) ([]byte, error) {
	// 隐藏辅助窗口需要显式帧，才能让布局后的尺寸稳定下来。
	deadline := time.Now().Add(3 * time.Second)
	var lastError error
	surfaceName, err := syscall.UTF16PtrFromString("MyGoSurface")
	if err != nil {
		return nil, err
	}
	for time.Now().Before(deadline) {
		mygo.RunOnMain(func() {
			surface, _, _ := findChild.Call(window.NativeHandle(), 0, uintptr(unsafe.Pointer(surfaceName)), 0)
			sendMessage.Call(surface, 0x0200, 0, 0)
			sendMessage.Call(surface, 0x000f, 0, 0)
		})
		raw, err := window.CapturePage()
		if err != nil {
			return nil, err
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		width, height := window.ContentSize()
		lastError = nil
		if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
			lastError = fmt.Errorf("popup scene size %v does not match native content %dx%d", img.Bounds(), width, height)
		} else {
			lastError = verifyPopupWhitespace(raw)
		}
		if lastError == nil {
			return raw, nil
		}
		// 让排队的尺寸调整和显示就绪消息在帧之间执行。
		time.Sleep(10 * time.Millisecond)
	}
	return nil, lastError
}

func verifyPopupWhitespace(raw []byte) error {
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	r, g, b, a := img.At(4, 4).RGBA()
	base := [4]uint32{r, g, b, a}
	for y := img.Bounds().Dy() - 1; y >= 0; y-- {
		for x := 16; x < img.Bounds().Dx()-16; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			for channel, value := range [4]uint32{r, g, b, a} {
				if max(value, base[channel])-min(value, base[channel]) > 514 {
					if gap := img.Bounds().Dy() - 1 - y; gap > 28 {
						return fmt.Errorf("popup has %d pixels of unused bottom space", gap)
					}
					return nil
				}
			}
		}
	}
	return fmt.Errorf("popup content is blank")
}

func dismissWithEscape(window *mygo.Window) error {
	var surface uintptr
	surfaceName, err := syscall.UTF16PtrFromString("MyGoSurface")
	if err != nil {
		return err
	}
	mygo.RunOnMain(func() {
		surface, _, _ = findChild.Call(window.NativeHandle(), 0, uintptr(unsafe.Pointer(surfaceName)), 0)
		if surface != 0 {
			sendMessage.Call(surface, 0x0100, 0x1b, 0x00010001)
			// 隐藏的 HWND 不会从桌面合成器接收绘制。
			// 显式刷新输入帧，但不显示辅助窗口。
			sendMessage.Call(surface, 0x000f, 0, 0)
			if !window.IsDestroyed() {
				sendMessage.Call(surface, 0x0101, 0x1b, 0xc0010001)
			}
		}
	})
	if surface == 0 {
		return fmt.Errorf("native popup input surface was not found")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if window.IsDestroyed() {
			return nil
		}
		// 隐藏窗口会延迟帧；截图会处理待处理的输入。
		_, _ = window.CapturePage()
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("按下 Escape 后原生弹窗仍未关闭")
}
