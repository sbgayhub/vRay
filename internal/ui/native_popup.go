package ui

import (
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

// 系统背景材质属于原生窗口，而不属于父窗口帧缓冲中的元素。
// 为弹窗提供独立表面，使 DWM/AppKit 能绘制这些材质。
// 此处所有状态和窗口访问都保持在 UI 线程上。
type nativePopup struct {
	window         *mygo.Window
	kind           string
	modal          bool
	material       string
	version        uint64
	anchor         mygo.Rectangle
	maxHeight      int
	height         int
	measuredHeight int
	appearance     *popupAppearance
}

func (r *Root) popupIsOpen(kind string) bool {
	switch kind {
	case "无法启用 TUN":
		return r.tunProblemOpen
	case "语言设置":
		return r.languageOpen
	case "外观设置":
		return r.lookOpen
	case "节点操作":
		return r.nodeMore
	case "编辑节点":
		return r.editF.open
	case "策略组编辑":
		return r.policyEditor.open
	case "新建规则", "编辑规则":
		return r.ruleF.open
	case "添加订阅":
		return r.importOpen
	case "编辑订阅":
		return r.sourceF.open
	case "删除订阅":
		return r.sourceDeleting != ""
	case "软件更新":
		return r.updateOpen
	case "关于 vRay":
		return r.aboutOpen
	case "免责声明与使用协议":
		return r.legalOpen || r.consentOpen
	}
	return false
}

func (r *Root) dismissPopup(kind string) {
	switch kind {
	case "无法启用 TUN":
		r.tunProblemOpen = false
	case "语言设置":
		r.languageOpen = false
	case "外观设置":
		r.lookOpen = false
	case "节点操作":
		r.nodeMore = false
	case "编辑节点":
		r.editF.open = false
	case "策略组编辑":
		r.policyEditor = policyGroupEditor{}
	case "新建规则", "编辑规则":
		r.ruleF.open = false
	case "添加订阅":
		r.importOpen = false
	case "编辑订阅":
		r.sourceF.open, r.sourceF.draft.URL = false, ""
		r.svc.CloseSourceEditor()
	case "删除订阅":
		r.sourceDeleting = ""
	case "软件更新":
		r.updateOpen = false
	case "关于 vRay":
		r.aboutOpen = false
	case "免责声明与使用协议":
		r.legalOpen = false
		r.consentOpen = r.svc.State().Snapshot().ConsentRequired
	}
}

func (r *Root) closeNativePopup() {
	if host := r.popupHost; host != nil {
		host.window.Close()
	}
}

func (r *Root) syncNativePopup(version uint64) {
	host := r.popupHost
	if host == nil {
		return
	}
	if !r.popupIsOpen(host.kind) {
		host.window.Close()
		return
	}
	if host.material != r.material {
		host.material = r.material
		host.window.SetVibrancy(materialFor(host.material))
		host.window.Invalidate()
	}
	host.appearance.setEnabled(materialFor(r.material) != mygo.VibrancyNone)
	if host.version != version {
		host.version = version
		host.window.Invalidate()
	}
}

func (r *Root) showNativePopup(c *ui.Context, kind string, anchor ui.Rect, modal bool) {
	if !r.popupIsOpen(kind) {
		return
	}
	rect := mygo.Rectangle{X: int(math.Round(float64(anchor.X))), Y: int(math.Round(float64(anchor.Y))), Width: int(math.Round(float64(anchor.W))), Height: int(math.Round(float64(anchor.H)))}
	if host := r.popupHost; host != nil {
		if host.kind != kind {
			host.window.Close()
		} else {
			if !modal && host.anchor != rect {
				host.window.SetAnchor(rect)
				host.anchor = rect
			}
			if modal && r.win.IsVisible() && !host.window.IsVisible() {
				host.window.Show()
			}
			return
		}
	}
	_, parentHeight := c.Size()
	width, height := 440, 440
	if modal {
		switch kind {
		case "无法启用 TUN":
			height = 280
		case "添加订阅":
			width, height = 500, 440
		case "策略组编辑":
			width, height = 700, 680
		case "删除订阅":
			height = 240
		case "新建规则", "编辑规则":
			height = 560
		case "软件更新":
			width, height = 520, 560
		case "关于 vRay":
			width, height = 500, 400
		case "免责声明与使用协议":
			width, height = 620, 640
		}
	} else {
		width, height = 288, 420
		if kind == "节点操作" {
			width, height = 192, 112
		}
	}
	limit := max(240, int(parentHeight)-48)
	height = min(height, limit)
	host := &nativePopup{kind: kind, modal: modal, material: r.material, anchor: rect, maxHeight: limit, height: height}
	view := ui.View(func(popupContext *ui.Context) { r.nativePopupView(popupContext, host) })
	parentWasActive := r.win.IsFocused()
	if modal {
		bounds := r.win.ContentBounds()
		host.window = mygo.NewWindow(mygo.WindowOptions{
			Title: kind, Parent: r.win, Modal: true, Width: width, Height: height, UseContentSize: true,
			X: bounds.X + (bounds.Width-width)/2, Y: bounds.Y + (bounds.Height-height)/2,
			Hidden: true, Frameless: true, Transparent: true, CornerRadius: 18, SkipTaskbar: true,
			DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
			Vibrancy: mygo.VibrancyMica, Content: view,
		})
	} else {
		host.window = mygo.NewFlyout(mygo.FlyoutOptions{
			Parent: r.win, Anchor: rect, Placement: mygo.PlacementBottomEnd, Gap: 8,
			Width: width, Height: height, Hidden: true, Focusable: true, Shadow: true,
			CornerRadius: 14, Vibrancy: mygo.VibrancyMica, Content: view,
		})
	}
	r.popupHost = host
	host.appearance = bindPopupAppearance(r.win, host.window, parentWasActive)
	host.appearance.setEnabled(materialFor(host.material) != mygo.VibrancyNone)
	// 创建时预留背景材质支持，之后即使选择“无”也应用该设置。
	host.window.SetVibrancy(materialFor(host.material))
	host.window.OnClosed(func() {
		if r.popupHost == host {
			r.popupHost = nil
			r.dismissPopup(host.kind)
			r.win.Invalidate()
		}
	})
	if r.win.IsVisible() {
		host.window.Show()
	}
	host.window.Invalidate()
}

func (r *Root) nativePopupView(c *ui.Context, host *nativePopup) {
	host.fitContent()
	snap := r.svc.State().Snapshot()
	setUILanguage(c, snap.Settings.Language)
	t, _, material := r.appearanceTheme(c, snap)
	if host.window != nil && host.material != material {
		host.material = material
		host.window.SetVibrancy(materialFor(material))
	}
	host.appearance.setEnabled(materialFor(material) != mygo.VibrancyNone)
	showsMaterial := materialFor(material) != mygo.VibrancyNone && c.Vibrancy()
	if showsMaterial {
		t = materialTheme(t)
	}
	c.SetTheme(&t)
	// SetTheme 也会绘制根元素；若使用系统背景，随后清除其背景。
	if showsMaterial {
		c.Root().Background(ui.Transparent)
	} else {
		c.Root().Background(t.Surface)
	}
	if r.lookSaving != nil {
		select {
		case <-r.lookSaving:
			r.lookSaving = nil
		default:
			c.Invalidate()
		}
	}
	previous, previousHeight := r.renderingPopup, r.popupLayoutHeight
	r.renderingPopup = true
	r.popupLayoutHeight = float32(host.maxHeight)
	if r.popupLayoutHeight == 0 {
		_, r.popupLayoutHeight = c.Size()
	}
	defer func() { r.renderingPopup, r.popupLayoutHeight = previous, previousHeight }()
	padding := float32(16)
	if host.modal {
		padding = 20
	}
	ui.Column(c).FillWidth().Shrink(0).Padding(padding).Gap(8).Label("弹窗内容").Draw(func(_ *ui.Painter, bounds ui.Rect) {
		// 在此记录完成的布局，然后在下一次构建前调整尺寸。
		// 不要在绘制原生表面帧时调整其尺寸。
		measured := int(math.Ceil(float64(bounds.H)))
		if measured != host.measuredHeight {
			host.measuredHeight = measured
			c.Invalidate()
		}
	}).Children(func() {
		switch host.kind {
		case "无法启用 TUN":
			r.tunProblemDialog(c, snap)
		case "语言设置":
			r.languagePopover(c, snap)
		case "外观设置":
			r.appearancePopover(c, snap)
		case "编辑节点":
			r.editDialog(c)
		case "策略组编辑":
			r.policyGroupEditorDialog(c, snap)
		case "新建规则", "编辑规则":
			r.ruleDialog(c)
		case "添加订阅":
			r.importDialog(c, snap)
		case "软件更新":
			r.updateDialog(c, snap)
		case "关于 vRay":
			r.aboutDialog(c, snap)
		case "免责声明与使用协议":
			r.legalDialog(c)
			r.consentDialog(c, snap)
		case "编辑订阅", "删除订阅":
			r.sourceDialog(c, snap)
		case "节点操作":
			r.nativeNodeMenu(c, snap)
		}
	})
	if host.kind != "免责声明与使用协议" && c.Shortcut(0, ui.KeyEscape) {
		r.dismissPopup(host.kind)
	}
	if host.window != nil && !r.popupIsOpen(host.kind) {
		host.window.Close()
		r.win.Invalidate()
	}
}

func (host *nativePopup) fitContent() {
	if host.measuredHeight == 0 {
		return
	}
	target := max(64, host.measuredHeight)
	if host.maxHeight > 0 {
		target = min(target, host.maxHeight)
	}
	if target == host.height {
		return
	}
	host.height = target
	if host.window == nil {
		return
	}
	if host.modal {
		bounds := host.window.ContentBounds()
		bounds.Y += (bounds.Height - target) / 2
		bounds.Height = target
		host.window.SetContentBounds(bounds)
	} else {
		width, _ := host.window.ContentSize()
		host.window.SetContentSize(width, target)
	}
}

func (r *Root) nativeNodeMenu(c *ui.Context, snap app.Snapshot) {
	for _, profile := range snap.Profiles {
		if profile.ID == r.selectedNode {
			r.nodeActions(c, snap, profile)
			return
		}
	}
	r.nodeMore = false
}
