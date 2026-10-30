package ui

import (
	"fmt"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/data"
	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

func (r *Root) nodes(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	w, h := c.Size()
	rows := filterProfiles(snap.Profiles, r.search)
	latencies := r.svc.Latencies()
	if r.selectedNode == "" && len(snap.Profiles) > 0 {
		r.selectedNode = snap.Profiles[0].ID
		if snap.Active != "" {
			r.selectedNode = snap.Active
		}
		r.svc.InspectSelectedNode(r.selectedNode)
	}
	detailWidth := float32(280)
	if w < 1050 {
		detailWidth = 224
	}
	contentHeight := h - captionHeight - 48 - 20 - browserHeaderHeight(w)
	listHeight := max(float32(160), contentHeight)
	if w >= 1100 {
		listHeight = max(float32(160), contentHeight-44)
	}
	ui.Row(c).FillWidth().Grow(1).Gap(20).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).Basis(0).Gap(12).Children(func() {
			if len(rows) == 0 {
				title, hint := "没有匹配的节点", "换个关键词试试，或者清空搜索框。"
				if len(snap.Profiles) == 0 {
					title, hint = "还没有节点", "在「订阅」页粘贴分享链接或订阅地址即可导入。"
				}
				r.emptyState(c, title, hint)
				return
			}
			if r.nodeSel >= len(rows) {
				r.nodeSel = -1
			}
			ui.Column(c).FillWidth().Radius(cardRadius).Background(t.Surface).Children(func() {
				if w >= 1100 {
					nodeTableHeader(c)
				}
				list := data.VirtualList(c, "节点列表", len(rows), &r.nodeSel, func(i int) {
					r.nodeRow(c, snap, rows[i], latencies[rows[i].ID])
				}, data.VirtualListOptions{Height: listHeight})
				if list.Changed() && r.nodeSel >= 0 && r.nodeSel < len(rows) {
					r.selectedNode = rows[r.nodeSel].ID
					r.svc.InspectSelectedNode(r.selectedNode)
				}
			})
		})
		ui.Column(c).Width(detailWidth).Shrink(0).Children(func() {
			ui.Scroll(c).FillWidth().Height(max(float32(180), contentHeight)).Children(func() {
				r.nodeDetails(c, snap, latencies)
			})
		})
	})
}

func nodeTableHeader(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(12, 16).Gap(10).Children(func() {
		ui.Text(c, tr(c, "节点名称")).Grow(1).Basis(0).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, tr(c, "协议")).Width(54).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, tr(c, "所属订阅")).Width(80).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, tr(c, "出口 IP")).Width(116).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, tr(c, "延迟")).Width(54).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, tr(c, "下载速度")).Width(80).FontSize(11).TextColor(t.TextMuted)
	})
}

func nodeSourceName(c *ui.Context, snap app.Snapshot, p app.ProfileView) string {
	for _, source := range snap.Sources {
		if source.ID == p.SourceID && source.ID != "" {
			return source.Name
		}
	}
	if p.SourceName != "" && p.SourceName != "手动导入" {
		return p.SourceName
	}
	return tr(c, "手动导入")
}

func (r *Root) nodeRow(c *ui.Context, snap app.Snapshot, p app.ProfileView, latency app.Latency) {
	t := c.Theme()
	w, _ := c.Size()
	background := ui.Transparent
	if p.ID == r.selectedNode {
		background = t.Accent.Alpha(0.1)
	}
	row := ui.ButtonBase(c).Label(p.Name).FillWidth().Padding(12, 4).Radius(10).
		Background(background).TextColor(t.Text)
	address := tr(c, nodeExitLabel(snap.NodeDetails[p.ID]))
	source := nodeSourceName(c, snap, p)
	label := app.LatencyLabel(latency)
	speed := tr(c, nodeDownloadLabel(snap.NodeSpeeds[p.ID]))
	if label == "" {
		label = "未测速"
	}
	row.Children(func() {
		if w >= 1100 {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, p.Name).Bold().SingleLine().Grow(1).Basis(0)
				ui.Text(c, strings.ToUpper(p.Protocol)).Width(54).FontSize(10).SingleLine().TextColor(accentForeground(t))
				ui.Text(c, source).Width(80).FontSize(11).SingleLine().TextColor(t.TextMuted)
				ui.Text(c, address).Width(116).FontSize(11).SingleLine().Tooltip(address).TextColor(t.TextMuted)
				ui.Text(c, tr(c, label)).Width(54).FontSize(11).SingleLine().TextColor(latencyColor(t, latency))
				ui.Text(c, speed).Width(80).FontSize(11).SingleLine().TextColor(t.TextMuted)
			})
		} else {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					ui.Text(c, p.Name).Bold().SingleLine().Grow(1).Basis(0)
					ui.Text(c, strings.ToUpper(p.Protocol)).FontSize(10).TextColor(accentForeground(t))
					ui.Text(c, tr(c, label)).FontSize(10).TextColor(latencyColor(t, latency))
				})
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					ui.Text(c, source).Grow(1).Basis(0).FontSize(11).SingleLine().TextColor(t.TextMuted)
					ui.Text(c, speed).FontSize(11).SingleLine().TextColor(t.TextMuted)
				})
				ui.Text(c, address).FontSize(11).SingleLine().TextColor(t.TextMuted)
			})
		}
	})
	if row.Clicked() {
		r.selectedNode = p.ID
		r.svc.InspectSelectedNode(p.ID)
	}
}

func nodeDownloadLabel(result app.DownloadTest) string {
	if result.Running {
		if result.CurrentBytesPerSecond > 0 {
			return rateLabel(result.CurrentBytesPerSecond)
		}
		return "测速中…"
	}
	if !result.CheckedAt.IsZero() {
		return rateLabel(result.BytesPerSecond)
	}
	if result.Error != "" {
		return "未完成"
	}
	return "未测速"
}

func nodeExitLabel(info app.IPDetails) string {
	if info.IP != "" {
		return info.IP
	}
	if info.Loading {
		return "查询中…"
	}
	if info.Error != "" {
		return "查询失败"
	}
	return "未查询"
}

func latencyColor(t *ui.Theme, latency app.Latency) ui.Color {
	if latency.State == app.LatencyOK {
		return t.Success
	}
	if latency.State == app.LatencyFailed {
		return t.Danger
	}
	return t.TextMuted
}

func (r *Root) nodeDetails(c *ui.Context, snap app.Snapshot, latencies map[string]app.Latency) {
	t := c.Theme()
	var selected *app.ProfileView
	for i := range snap.Profiles {
		if snap.Profiles[i].ID == r.selectedNode {
			selected = &snap.Profiles[i]
			break
		}
	}
	if selected == nil {
		panel(c, func() {
			ui.Text(c, tr(c, "节点详情")).Bold()
			ui.Text(c, tr(c, "选择节点，查看连接与 IP 信息")).FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	p := *selected
	connected := snap.Phase == app.PhaseConnected && snap.Active == p.ID
	busy := snap.Phase == app.PhaseConnecting || snap.Phase == app.PhaseDisconnecting
	panel(c, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(c, "节点详情")).Bold()
			ui.Spacer(c)
			anchor := textButton(c, "更多")
			if anchor.Clicked() {
				r.nodeMore = !r.nodeMore
			}
			r.popover(c, anchor, &r.nodeMore, func() {
				r.nodeActions(c, snap, p)
			})
		})
		ui.Box(c).Size(48, 48).Radius(16).Center().Background(t.Accent.Alpha(0.12)).Children(func() {
			ui.Icon(c, icon.Must("server")).Size(24, 24).TextColor(t.Accent)
		})
		ui.Text(c, p.Name).Bold().FontSize(18).MaxLines(2)
		infoPair(c, "协议类型", strings.ToUpper(p.Protocol))
		source := nodeSourceName(c, snap, p)
		infoPair(c, "所属订阅", source)
		infoPair(c, "服务器地址", p.Address)
		if p.IP != "" {
			infoPair(c, "服务器 IP", p.IP)
		}
		infoPair(c, "端口", fmt.Sprint(p.Port))
		latency := app.LatencyLabel(latencies[p.ID])
		if latency == "" {
			latency = "未测速"
		}
		infoPair(c, "延迟时间", tr(c, latency))
		ui.Divider(c)
		ui.Text(c, tr(c, "节点已用流量")).Bold().FontSize(12)
		usage := snap.Usage[p.ID]
		infoPair(c, "上传", transferLabel(usage.Upload))
		infoPair(c, "下载", transferLabel(usage.Download))
		if connected {
			if textButton(c, "断开连接").FillWidth().Clicked() {
				r.svc.Disconnect()
			}
		} else if busy {
			textButton(c, "连接处理中…", disabled()).FillWidth()
		} else if textButton(c, "连接此节点", primary()).FillWidth().Clicked() {
			r.svc.UseProfile(p.ID)
		}
		if r.deleting == p.ID {
			ui.Text(c, tr(c, "确定删除此节点？")).TextColor(t.Danger).FontSize(12)
			ui.Row(c).Gap(8).Children(func() {
				if textButton(c, "确认删除", dangerous()).Clicked() {
					r.svc.DeleteProfiles([]string{p.ID})
					r.deleting = ""
					r.selectedNode = ""
				}
				if textButton(c, "取消").Clicked() {
					r.deleting = ""
				}
			})
		}
	}).Padding(18).Gap(10)
	ui.Box(c).Height(16)
	r.nodeDownloadDetails(c, snap, p.ID)
	ui.Box(c).Height(16)
	panel(c, func() {
		ui.Text(c, tr(c, "出口 IP 信息")).Bold()
		info := snap.NodeDetails[p.ID]
		infoPair(c, "出口 IP", info.IP)
		infoPair(c, "国家 / 地区", strings.TrimSpace(info.Country+" "+info.Region))
		infoPair(c, "城市", info.City)
		infoPair(c, "ASN", info.ASN)
		infoPair(c, "提供商", info.Provider)
		infoPair(c, "网络类型", info.Type)
		score := info.Score
		if score == "" {
			score = "数据源未提供"
		}
		infoPair(c, "风险评分", tr(c, score))
		if info.Loading {
			textButton(c, "查询中…", disabled())
		} else {
			label := "查询出口 IP"
			if info.IP != "" {
				label = "更新出口 IP 信息"
			}
			if textButton(c, label).Clicked() {
				r.svc.InspectNode(p.ID)
			}
		}
		if !info.CheckedAt.IsZero() {
			infoPair(c, "上次更新", info.CheckedAt.Local().Format("2006-01-02 15:04"))
		}
		ui.Text(c, tr(c, "首次查询后保存在本地，之后仅在手动更新时重新查询。")).FontSize(10).TextColor(t.TextMuted).MaxLines(3)
		if info.Error != "" {
			ui.Text(c, info.Error).FontSize(11).TextColor(t.Warning).MaxLines(3)
			if info.IP != "" {
				ui.Text(c, tr(c, "已保留上次成功查询的记录")).FontSize(10).TextColor(t.TextMuted)
			}
		}
	}).Padding(18).Gap(8)
}

func (r *Root) nodeDownloadDetails(c *ui.Context, snap app.Snapshot, id string) {
	t := c.Theme()
	result := snap.NodeSpeeds[id]
	panel(c, func() {
		ui.Text(c, tr(c, "下载测速")).Bold()
		ui.Text(c, nodeDownloadLabel(result)).FontSize(21).Bold().TextColor(accentForeground(t))
		if result.Running {
			ui.Progress(c, min(1, float64(result.CurrentBytes)/float64(app.DownloadTestMaxBytes))).FillWidth().Height(5)
			infoPair(c, "已下载", transferLabel(result.CurrentBytes))
			if textButton(c, "停止本次测速").FillWidth().Clicked() {
				r.svc.CancelDownload()
			}
		} else {
			label := "开始下载测速"
			if !result.CheckedAt.IsZero() {
				label = "重新测试下载速度"
			}
			if textButton(c, label).FillWidth().Clicked() {
				r.svc.TestDownload(id)
			}
		}
		if !result.CheckedAt.IsZero() {
			infoPair(c, "上次平均", rateLabel(result.BytesPerSecond))
			infoPair(c, "带宽换算", fmt.Sprintf("%.2f Mbps", float64(result.BytesPerSecond)*8/1e6))
			infoPair(c, "测试下载量", transferLabel(result.Bytes))
			infoPair(c, "测试时间", result.CheckedAt.Local().Format("2006-01-02 15:04"))
		}
		ui.Text(c, tr(c, "单连接测量，下载阶段最长 8 秒 / 最多 32 MiB；结果保存在本地，仅手动重测时更新。")).FontSize(10).TextColor(t.TextMuted).MaxLines(4)
		if result.Error != "" {
			ui.Text(c, result.Error).FontSize(11).TextColor(t.Warning).MaxLines(3)
			if !result.CheckedAt.IsZero() {
				ui.Text(c, tr(c, "已保留上次成功测速结果")).FontSize(10).TextColor(t.TextMuted)
			}
		}
	}).Padding(18).Gap(8)
}

func (r *Root) nodeActions(c *ui.Context, snap app.Snapshot, p app.ProfileView) {
	if textButton(c, "编辑节点").Clicked() {
		r.openEdit(p.ID)
		r.nodeMore = false
	}
	opts := []buttonOption{dangerous()}
	if snap.Phase == app.PhaseConnected && snap.Active == p.ID {
		opts = append(opts, disabled())
	}
	if textButton(c, "删除节点", opts...).Tooltip(tr(c, "正在使用的节点需先断开")).Clicked() {
		r.deleting = p.ID
		r.nodeMore = false
	}
}
