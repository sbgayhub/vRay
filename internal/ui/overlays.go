package ui

import (
	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/ELY-Open-Source/CrychicUI/kit"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// 仅模糊背景；控件和文字保持清晰。
var popupFrost = shader.MustCompile(`package main
var Tint vec4
func Fragment(dst vec4, pos vec2) vec4 {
	return mix(backdropSample(pos), vec4(Tint.rgb, 1), Tint.a)
}`)

type popupMaterial struct{ tint ui.Color }

func (m popupMaterial) PaintMaterial(p *ui.Painter, box ui.Rect, radii [4]float32) {
	popupFrost.Draw(p, box, shader.Options{BackdropBlur: 28, Radius: radii[0], Uniforms: map[string]any{"Tint": m.tint}})
}

func (r *Root) popupSurface(c *ui.Context, panel ui.Element) {
	t := c.Theme()
	panel.Background(t.Surface).Border(1, t.Border).Shadow(0, 12, 32, 0, ui.RGB(0, 0, 0).Alpha(0.2))
	if r.materialVisible {
		// Surface 本身已带有页面透明度，Alpha 会再乘到该值上；因此单独设置弹窗色调，以保证文字可读。
		tint := t.Surface
		tint.A = 240
		if t.Dark {
			tint.A = 245
		}
		panel.Material(popupMaterial{tint: tint})
	}
}

func (r *Root) modal(c *ui.Context, title string, open *bool, body func()) {
	r.modalWithClose(c, title, open, true, body)
}

func (r *Root) blockingModal(c *ui.Context, title string, open *bool, body func()) {
	r.modalWithClose(c, title, open, false, body)
}

func (r *Root) modalWithClose(c *ui.Context, title string, open *bool, closable bool, body func()) {
	if open == nil || !*open {
		return
	}
	if r.renderingPopup {
		r.modalBody(c, title, open, closable, body)
		return
	}
	if r.win != nil {
		r.showNativePopup(c, title, ui.Rect{}, true)
		return
	}
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		kit.Arrive(backdrop.Background(ui.RGB(0, 0, 0).Alpha(0.32)), ui.Pose{Fade: 1}, 0)
		panel.FillWidth().MaxWidth(440).Padding(20).Radius(18).Label(title)
		if title == "添加订阅" {
			panel.MaxWidth(500)
		} else if title == "策略组编辑" {
			panel.MaxWidth(700)
		}
		r.popupSurface(c, panel)
		kit.Arrive(panel, ui.Pose{Scale: 0.95, Fade: 1}, 0)
		r.modalBody(c, title, open, closable, body)
	})
}

func (r *Root) modalBody(c *ui.Context, title string, open *bool, closable bool, body func()) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Text(c, tr(c, title)).Bold().FontSize(17).Grow(1)
			if closable {
				if ui.ButtonBase(c).Label("关闭").Tooltip(tr(c, "关闭")).Size(30, 30).Radius(8).Center().Background(t.SurfaceHover).Children(func() {
					ui.Icon(c, icon.Must("x")).Size(16, 16)
				}).Clicked() {
					*open = false
				}
			}
		})
		body()
	})
}

func (r *Root) popover(c *ui.Context, anchor ui.Element, open *bool, body func()) {
	if open == nil || !*open {
		return
	}
	if r.renderingPopup {
		ui.Column(c).FillWidth().Gap(8).Children(body)
		return
	}
	if r.win != nil {
		kind := "节点操作"
		if open == &r.lookOpen {
			kind = "外观设置"
		}
		if open == &r.languageOpen {
			kind = "语言设置"
		}
		r.showNativePopup(c, kind, anchor.Bounds(), false)
		return
	}
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.Margin(10, 0, 0, 0).Width(288).Padding(16).Radius(14)
		r.popupSurface(c, panel)
		kit.Arrive(panel, ui.Pose{Scale: 0.95, Fade: 1}, 0)
		ui.Column(c).FillWidth().Gap(8).Children(body)
	})
}
