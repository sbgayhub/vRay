package ui

import "github.com/egoist/mygo"

// 此结构仅保存父窗口的视觉激活状态。键盘焦点和原生
// 模态输入仍由弹窗掌控。所有回调都在 UI 线程运行。
type popupAppearance struct {
	enabled, focused, closed bool
	apply                    func(bool)
	off                      []func()
}

func bindPopupAppearance(parent, popup *mygo.Window, initiallyActive bool) *popupAppearance {
	hold := &popupAppearance{apply: parentAppearanceSetter(parent), focused: initiallyActive}
	hold.off = []func(){
		popup.OnFocus(func() { hold.setFocused(true) }),
		popup.OnBlur(func() { hold.setFocused(false) }),
		popup.OnClosed(hold.close),
	}
	return hold
}

func (hold *popupAppearance) setEnabled(value bool) {
	if hold == nil || hold.closed {
		return
	}
	hold.enabled = value
	hold.apply(hold.enabled && hold.focused)
}

func (hold *popupAppearance) setFocused(value bool) {
	if hold == nil || hold.closed {
		return
	}
	hold.focused = value
	hold.apply(hold.enabled && hold.focused)
}

func (hold *popupAppearance) close() {
	if hold == nil || hold.closed {
		return
	}
	hold.closed = true
	hold.apply(false)
	for _, off := range hold.off {
		off()
	}
	hold.off = nil
}
