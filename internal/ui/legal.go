package ui

import (
	"fmt"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

var legalParagraphs = []string{
	"vRay 是本地 Xray 客户端，不提供代理服务器、节点或订阅内容。你需要自行确认节点来源、配置和网络服务合法可靠。",
	"使用 vRay 时必须遵守所在国家或地区的法律法规、网络服务商条款及目标服务的使用规则。不得利用本软件从事违法、侵权、攻击、欺诈或干扰网络正常运行的活动。",
	"代理、TUN、系统代理和订阅更新会改变网络路径或系统设置。请了解相关风险，并在操作前备份重要数据。软件会记录节点和订阅配置、出口信息及测速结果到本地数据目录。",
	"更新功能会访问 GitHub 发布页，只安装与内置公钥签名匹配的版本。自动检查可由你关闭；发现新版本后仍由你决定是否安装。",
	"软件按现状提供，不承诺持续可用、无错误或适合特定用途。在法律允许的范围内，作者不对因配置错误、节点故障、网络中断、数据损失、第三方服务或违规使用造成的损失承担责任。",
	"继续使用表示你已阅读并同意本免责声明与使用协议。",
}

func (r *Root) legalDialog(c *ui.Context) {
	if !r.legalOpen {
		return
	}
	r.modal(c, "免责声明与使用协议", &r.legalOpen, func() {
		legalContent(c, false)
		ui.Divider(c)
		ui.Row(c).FillWidth().Children(func() {
			ui.Spacer(c)
			if textButton(c, "关闭", primary()).Clicked() {
				r.legalOpen = false
			}
		})
	})
}

func (r *Root) consentDialog(c *ui.Context, snap app.Snapshot) {
	if !snap.ConsentRequired {
		r.consentOpen = false
		return
	}
	r.consentOpen = true
	r.blockingModal(c, "免责声明与使用协议", &r.consentOpen, func() {
		t := c.Theme()
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(42, 42).Shrink(0).Radius(14).Center().Background(t.Warning.Alpha(.12)).Children(func() {
				ui.Icon(c, icon.Must("shield")).Size(21, 21).TextColor(t.Warning)
			})
			ui.Column(c).Grow(1).Basis(0).Gap(4).Children(func() {
				ui.Text(c, tr(c, "首次使用前请阅读并同意以下条款")).Bold().FontSize(14)
				ui.Text(c, tr(c, "同意后才能启动订阅更新、Geo 下载和应用更新检查。")).FontSize(11).TextColor(t.TextMuted)
			})
		})
		legalContent(c, true)
		if snap.Problem != nil && snap.Problem.Code == "STORE_ERROR" {
			ui.Text(c, snap.Problem.Message).FontSize(11).TextColor(t.Danger).MaxLines(2)
		}
		ui.Divider(c)
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			if textButton(c, "不同意并退出").Clicked() {
				mygo.App.Quit()
			}
			ui.Spacer(c)
			label := "同意并继续"
			opts := []buttonOption{primary()}
			if r.consentSaving != nil {
				label = "正在保存…"
				opts = append(opts, disabled())
			}
			if textButton(c, label, opts...).Clicked() {
				r.consentSaving = r.svc.AcceptTerms()
			}
		})
	})
}

func legalContent(c *ui.Context, compact bool) {
	t := c.Theme()
	height := float32(300)
	if compact {
		height = 250
	}
	if _, viewportHeight := c.Size(); viewportHeight > 0 {
		height = min(height, viewportHeight-190)
	}
	height = max(160, height)
	ui.Scroll(c).FillWidth().MaxHeight(height).Children(func() {
		ui.Column(c).FillWidth().Gap(9).Children(func() {
			ui.Text(c, fmt.Sprintf(tr(c, "协议版本 %d"), store.LegalVersion)).FontSize(10).TextColor(t.TextMuted)
			for _, paragraph := range legalParagraphs {
				ui.Text(c, tr(c, paragraph)).FontSize(11).TextColor(t.TextMuted)
			}
		})
	})
}
