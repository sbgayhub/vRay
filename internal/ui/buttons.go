package ui

import (
	"github.com/ELY-Open-Source/CrychicUI/button"
	"github.com/egoist/mygo/ui"
)

// textButton 使用 CrychicUI 共用的几何、动画和交互。
func textButton(c *ui.Context, label string, opts ...buttonOption) ui.Element {
	label = tr(c, label)
	t := c.Theme()

	tone, ink, border := t.Surface, t.Text, t.Border
	off, tint, danger, filled := false, false, false, false
	for _, o := range opts {
		switch o {
		case optPrimary:
			filled = true
		case optTinted:
			tint = true
		case optDanger:
			danger = true
		case optDisabled:
			off = true
		}
	}
	switch {
	case filled:
		tone, ink, border = t.Accent, t.AccentText, ui.Color{}
	case tint:
		border = t.Accent
		ink = accentForeground(t)
	case danger:
		border = t.Danger
		ink = t.Danger
	}
	if off {
		tone, ink, border = t.Surface, t.TextMuted, t.Border
	}

	var btn ui.Element
	switch {
	case filled:
		btn = button.Solid(c, label)
	case tint:
		btn = button.Soft(c, label)
	default:
		btn = button.Outline(c, label)
	}
	btn.Shrink(0)
	if !filled && !tint && !off {
		background := t.Surface
		if btn.Pressed() {
			background = t.SurfacePressed
		} else if btn.Hovered() {
			background = t.SurfaceHover
		}
		btn.Background(btn.AnimateColor("app.surface", background, ui.Snappy))
	}
	if danger || tint {
		btn.TextColor(ink)
	}
	if off {
		btn.Background(tone).TextColor(ink)
	}
	if border.A > 0 && (danger || tint || off) {
		btn.Border(1, border)
	}
	btn.Disabled(off)

	return btn
}

// buttonOption 描述 textButton 的外观。
type buttonOption int

const (
	// optPrimary 是填充强调色的按钮，用于页面的主要操作。
	optPrimary buttonOption = iota + 1
	// optTinted 用强调色描边按钮，用于表示已选项。
	optTinted
	// optDanger 用危险色绘制按钮，用于破坏性操作。
	optDanger
	// optDisabled 将按钮置灰并停止响应。
	optDisabled
)

// primary 使按钮填充强调色，用于页面的主要操作。
func primary() buttonOption { return optPrimary }

// tinted 用强调色绘制按钮但不填充，用于已选中的选项。
func tinted() buttonOption { return optTinted }

// dangerous 用危险色绘制按钮，用于破坏性操作。
func dangerous() buttonOption { return optDanger }

// disabled 将按钮置灰并停止响应。
func disabled() buttonOption { return optDisabled }
