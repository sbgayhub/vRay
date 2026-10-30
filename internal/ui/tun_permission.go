package ui

import (
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
)

func (r *Root) tunProblemDialog(c *ui.Context, snap app.Snapshot) {
	if !r.tunProblemOpen {
		return
	}
	r.modal(c, "无法启用 TUN", &r.tunProblemOpen, func() {
		t := c.Theme()
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(42, 42).Shrink(0).Radius(14).Center().Background(t.Warning.Alpha(.12)).Children(func() {
				ui.Icon(c, icon.Must("shield")).Size(22, 22).TextColor(t.Warning)
			})
			message := "TUN 需要管理员权限"
			if snap.Problem != nil {
				message = snap.Problem.Message
			}
			ui.Text(c, tr(c, message)).Grow(1).Basis(0).Bold().FontSize(14).MaxLines(3)
		})
		instruction := "请先退出 vRay，在程序或快捷方式上点击右键，选择以管理员身份运行，然后重新连接。"
		if snap.Problem != nil && strings.Contains(snap.Problem.Message, "wintun.dll") {
			instruction = "请将便携包中的 wintun.dll 放在程序旁边，确认驱动架构与程序一致，然后重新连接。"
		}
		ui.Text(c, tr(c, instruction)).
			FontSize(12).TextColor(t.TextMuted).MaxLines(4)
		ui.Text(c, tr(c, "系统代理模式无需管理员权限，可直接切换使用。")).FontSize(11).TextColor(t.TextMuted).MaxLines(2)
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if textButton(c, "改用系统代理").Clicked() {
				r.svc.SetMode("system-proxy")
				r.tunProblemOpen = false
			}
			ui.Spacer(c)
			if textButton(c, "知道了", primary()).Clicked() {
				r.tunProblemOpen = false
			}
		})
	})
}
