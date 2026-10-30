package ui

import (
	"runtime"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

const projectURL = "https://github.com/sbgayhub/vray"

func (r *Root) aboutSettingsSection(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	section(c, "关于与支持", func() {
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(38, 38).Shrink(0).Radius(12).Center().Background(t.Accent.Alpha(.10)).Children(func() {
				ui.Icon(c, icon.Must("shield")).Size(19, 19).TextColor(accentForeground(t))
			})
			ui.Column(c).Grow(1).Basis(0).Gap(3).Children(func() {
				ui.Text(c, "vRay "+appVersion()).Bold().FontSize(13)
				ui.Text(c, tr(c, "原生、轻量的 Xray 客户端")).FontSize(11).TextColor(t.TextMuted)
			})
		})
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if textButton(c, "检查更新").Clicked() {
				r.updateOpen = true
				r.svc.CheckForUpdates()
			}
			if textButton(c, "关于 vRay").Clicked() {
				r.aboutOpen = true
			}
			if textButton(c, "免责声明与使用协议").Clicked() {
				r.legalOpen = true
			}
			ui.Spacer(c)
			if textButton(c, "GitHub").Clicked() {
				c.OpenURL(projectURL)
			}
		})
	})
}

func (r *Root) aboutDialog(c *ui.Context, snap app.Snapshot) {
	if !r.aboutOpen {
		return
	}
	r.modal(c, "关于 vRay", &r.aboutOpen, func() {
		t := c.Theme()
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(48, 48).Shrink(0).Radius(15).Center().Background(t.Accent.Alpha(.12)).Children(func() {
				ui.Icon(c, icon.Must("shield")).Size(24, 24).TextColor(accentForeground(t))
			})
			ui.Column(c).Grow(1).Basis(0).Gap(4).Children(func() {
				ui.Text(c, "vRay "+appVersion()).Bold().FontSize(19)
				ui.Text(c, tr(c, "原生、轻量的 Xray 客户端")).FontSize(12).TextColor(t.TextMuted)
			})
		})
		ui.Text(c, tr(c, "vRay 将 Xray 内核嵌入本地应用，提供节点、订阅、规则和策略组管理。")).
			FontSize(12).TextColor(t.TextMuted).MaxLines(3)
		infoPair(c, "运行平台", runtime.GOOS+"/"+runtime.GOARCH)
		infoPair(c, "Xray 内核", fallback(snap.CoreVer, "未启动"))
		infoPair(c, "发布者", "sbgayhub")
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(c, "数据目录")).FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, snap.DataRoot).FontSize(11).Grow(1).SingleLine().Tooltip(snap.DataRoot)
		})
		ui.Link(c, projectURL, projectURL)
		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if textButton(c, "检查更新", primary()).Clicked() {
				r.aboutOpen = false
				r.updateOpen = true
				r.svc.CheckForUpdates()
			}
			if textButton(c, "免责声明与使用协议").Clicked() {
				r.aboutOpen = false
				r.legalOpen = true
			}
			ui.Spacer(c)
			if textButton(c, "关闭").Clicked() {
				r.aboutOpen = false
			}
		})
	})
}

func fallback(value, otherwise string) string {
	if value != "" {
		return value
	}
	return otherwise
}
