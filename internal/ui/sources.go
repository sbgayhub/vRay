package ui

import (
	"fmt"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
)

const importBoxHeight = 112

func (r *Root) importDialog(c *ui.Context, snap app.Snapshot) {
	if !r.importOpen {
		return
	}
	r.modal(c, "添加订阅", &r.importOpen, func() { r.importCard(c, snap) })
}

func (r *Root) importResult(c *ui.Context, snap app.Snapshot) {
	switch {
	case snap.ImportBusy:
		ui.Text(c, tr(c, "正在获取订阅并导入节点…")).TextColor(c.Theme().TextMuted).FontSize(12)
	case snap.ImportProblem != "":
		ui.Text(c, snap.ImportProblem).TextColor(c.Theme().Danger).FontSize(12)
	case snap.ImportAdded > 0:
		ui.Text(c, fmt.Sprintf(tr(c, "已导入 %d 个节点"), snap.ImportAdded)).TextColor(c.Theme().Success).FontSize(12)
	}
}

func (r *Root) importCard(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	_, height := c.Size()
	if r.renderingPopup {
		height = r.popupLayoutHeight
	}
	text := strings.TrimSpace(r.importText)
	isURL := strings.HasPrefix(strings.ToLower(text), "https://") || strings.HasPrefix(strings.ToLower(text), "http://")
	completed := r.importAttempted && !snap.ImportBusy && snap.ImportAdded > 0 &&
		r.importText == r.importSubmittedText && r.importName == r.importSubmittedName
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Scroll(c).FillWidth().MaxHeight(max(240, height-150)).Children(func() {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(40, 40).Shrink(0).Radius(13).Center().Background(t.Accent.Alpha(.10)).Children(func() {
						ui.Icon(c, icon.Must("download")).Size(20, 20).TextColor(accentForeground(t))
					})
					ui.Column(c).Grow(1).Basis(0).Gap(5).Children(func() {
						ui.Text(c, tr(c, "连接你的节点来源")).Bold().FontSize(14)
						ui.Text(c, tr(c, "粘贴分享链接或订阅地址，自动识别内容格式。")).FontSize(11).MaxLines(2).TextColor(t.TextMuted)
					})
				})
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					ui.Row(c).FillWidth().Gap(6).Children(func() {
						ui.Text(c, tr(c, "订阅名称")).Label("订阅名称标题").FontSize(12)
						ui.Text(c, tr(c, "可选")).FontSize(10).TextColor(t.TextMuted)
					})
					ui.TextInput(c, &r.importName).Label("订阅名称").FillWidth().Height(34).
						Placeholder(tr(c, "留空时自动命名，仅用于订阅来源")).Disabled(snap.ImportBusy)
				})
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Text(c, tr(c, "订阅链接或分享内容")).Label("订阅内容标题").FontSize(12).Grow(1)
						if homeIconButton(c, homeActionOptions{
							label: "粘贴订阅", glyph: "clipboard", hint: "从剪贴板粘贴", disabled: snap.ImportBusy,
						}) {
							r.importText = c.ReadClipboard()
						}
					})
					ui.TextArea(c, &r.importText).Label("订阅链接或分享内容").FillWidth().Height(importBoxHeight).
						Placeholder(tr(c, "https://… 订阅地址，或 vless://… / vmess://… / trojan://… / hysteria2://…")).
						Background(t.SurfaceHover).Disabled(snap.ImportBusy)
				})
				ui.Row(c).FillWidth().Gap(6).Children(func() {
					label := "支持订阅链接、分享链接、Clash 与 Base64"
					if isURL {
						label = "将保存订阅来源，后续可更新和编辑"
					}
					ui.Icon(c, icon.Must("shield")).Size(13, 13).TextColor(t.TextMuted)
					ui.Text(c, tr(c, label)).FontSize(10).MaxLines(2).Grow(1).TextColor(t.TextMuted)
				})
				if snap.ImportBusy || r.importAttempted {
					ink, message := t.TextMuted, tr(c, "正在获取订阅并导入节点…")
					if !snap.ImportBusy && snap.ImportProblem != "" {
						ink, message = t.Warning, snap.ImportProblem
					}
					if completed {
						ink, message = t.Success, fmt.Sprintf(tr(c, "已导入 %d 个节点"), snap.ImportAdded)
					}
					if snap.ImportBusy || completed || snap.ImportProblem != "" {
						ui.Box(c).FillWidth().Radius(10).Padding(10).Background(ink.Alpha(.08)).Children(func() {
							ui.Text(c, message).FontSize(11).MaxLines(3).TextColor(ink)
						})
					}
				}
			})
		})
		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if textButton(c, "清空").Disabled(snap.ImportBusy).Clicked() {
				r.importText, r.importName, r.importAttempted = "", "", false
			}
			ui.Spacer(c)
			cancelLabel := "取消"
			if snap.ImportBusy {
				cancelLabel = "后台导入"
			}
			if textButton(c, cancelLabel).Clicked() {
				r.importOpen = false
			}
			label := "导入"
			if snap.ImportBusy {
				label = "导入中…"
			} else if completed {
				label = "完成"
			}
			opts := []buttonOption{primary()}
			if text == "" || snap.ImportBusy {
				opts = append(opts, disabled())
			}
			if textButton(c, label, opts...).Clicked() {
				if completed {
					r.importOpen, r.importAttempted = false, false
					r.importText, r.importName = "", ""
				} else {
					r.importAttempted = true
					r.importSubmittedText, r.importSubmittedName = r.importText, r.importName
					r.svc.Import(app.ImportInput{Text: r.importText, Name: r.importName})
				}
			}
		})
	})
}
