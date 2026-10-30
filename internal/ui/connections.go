package ui

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/ELY-Open-Source/CrychicUI/data"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/configgen"
)

type connectionBrowser struct {
	rows                   []xcore.Connection
	filtered               []xcore.Connection
	filteredAt             time.Time
	lastQuery, lastNetwork string
	next                   time.Time
	query, network         string
	selected               uint64
}

func (r *Root) refreshConnections(c *ui.Context) {
	c.After(500 * time.Millisecond)
	if !r.connections.next.IsZero() && c.Now().Before(r.connections.next) {
		return
	}
	r.connections.next = c.Now().Add(500 * time.Millisecond)
	r.connections.rows = r.svc.Connections()
	for _, row := range r.connections.rows {
		if row.ID == r.connections.selected {
			return
		}
	}
	r.connections.selected = 0
}

func connectionOutbound(row xcore.Connection, snap app.Snapshot) string {
	if row.NodeName != "" {
		return row.NodeName
	}
	switch row.Outbound {
	case configgen.TagDirect:
		return "直连"
	case configgen.TagBlock:
		return "拦截"
	case configgen.TagDNS:
		return "DNS"
	case configgen.TagProxy:
		if row.NodeName != "" {
			return row.NodeName
		}
		if name := activeName(snap); name != "" {
			return name
		}
		return "当前节点"
	default:
		return row.Outbound
	}
}

func filterConnections(rows []xcore.Connection, query, network string, snap app.Snapshot) []xcore.Connection {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" && network == "" {
		return rows
	}
	result := make([]xcore.Connection, 0, len(rows))
	for _, row := range rows {
		if network != "" && !strings.EqualFold(row.Network, network) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(row.Destination+" "+row.Source+" "+row.Network+" "+row.Protocol+" "+row.NodeProtocol+" "+row.Inbound+" "+row.Outbound+" "+connectionOutbound(row, snap)), query) {
			continue
		}
		result = append(result, row)
	}
	return result
}

func (b *connectionBrowser) visibleRows(snap app.Snapshot) []xcore.Connection {
	if b.filtered == nil || b.filteredAt != b.next || b.lastQuery != b.query || b.lastNetwork != b.network {
		b.filtered = filterConnections(b.rows, b.query, b.network, snap)
		b.filteredAt, b.lastQuery, b.lastNetwork = b.next, b.query, b.network
	}
	return b.filtered
}

func connectionDuration(start, now time.Time) string {
	d := max(time.Duration(0), now.Sub(start))
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分 %02d 秒", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d 时 %02d 分", int(d.Hours()), int(d.Minutes())%60)
}

func (r *Root) connectionsPage(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	w, h := c.Size()
	rows := r.connections.visibleRows(snap)
	contentHeight := h - captionHeight - 48 - 20 - browserHeaderHeight(w)
	if len(rows) == 0 {
		title, hint := "暂无活动连接", "打开网页或应用后，经过 vRay 的连接会自动出现在这里。"
		if len(r.connections.rows) > 0 {
			title, hint = "没有匹配的连接", "调整搜索关键词或协议筛选。"
		} else if snap.Phase != app.PhaseConnected {
			hint = "连接节点后，在这里查看实时网络连接。"
		}
		r.emptyState(c, title, hint)
		return
	}
	detailWidth := float32(250)
	if w < 1050 {
		detailWidth = 220
	}
	var selected *xcore.Connection
	chosen := -1
	for i := range rows {
		if rows[i].ID == r.connections.selected {
			selected = &rows[i]
			chosen = i
			break
		}
	}
	ui.Row(c).Grow(1).FillWidth().Gap(18).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).Basis(0).Gap(8).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr(c, "目标 / 出口")).FontSize(11).TextColor(t.TextMuted)
				ui.Spacer(c)
				ui.Text(c, fmt.Sprintf(tr(c, "显示 %d 条 · 每 0.5 秒更新"), len(rows))).FontSize(10).TextColor(t.TextMuted)
			})
			list := data.VirtualList(c, "活动连接列表", len(rows), &chosen, func(i int) {
				row := rows[i]
				bg := t.Surface
				muted, protocolInk, downloadInk := t.TextMuted, accentForeground(t), t.Success
				if row.ID == r.connections.selected {
					bg = t.Accent.Alpha(0.12)
					muted, protocolInk, downloadInk = t.AccentText.Alpha(0.9), t.AccentText, t.AccentText
				}
				item := ui.ButtonBase(c).Label(fmt.Sprintf(tr(c, "查看连接 %d"), row.ID)).FillWidth().Padding(12).Radius(12).Background(bg).Children(func() {
					ui.Column(c).FillWidth().Gap(7).Children(func() {
						ui.Row(c).FillWidth().Gap(8).Children(func() {
							ui.Text(c, row.Destination).Bold().SingleLine().Grow(1).Basis(0).Tooltip(row.Destination)
							if w >= 1050 {
								ui.Text(c, connectionDuration(row.Started, c.Now())).FontSize(10).TextColor(muted)
							}
						})
						ui.Row(c).FillWidth().Gap(10).Children(func() {
							label := strings.ToUpper(row.Network)
							if row.Protocol != "" {
								label += " · " + strings.ToUpper(row.Protocol)
							}
							ui.Text(c, tr(c, label)).FontSize(10).TextColor(protocolInk)
							ui.Text(c, connectionOutbound(row, snap)).Grow(1).Basis(0).SingleLine().FontSize(11).TextColor(muted)
						})
						ui.Row(c).FillWidth().Gap(12).Children(func() {
							ui.Text(c, "↑ "+transferLabel(row.Upload)).FontSize(11).TextColor(muted)
							ui.Text(c, "↓ "+transferLabel(row.Download)).FontSize(11).TextColor(downloadInk)
							ui.Spacer(c)
						})
					})
				})
				if item.Clicked() {
					r.connections.selected = row.ID
				}
			}, data.VirtualListOptions{Height: max(float32(140), contentHeight-24)})
			if list.Changed() && chosen >= 0 && chosen < len(rows) {
				r.connections.selected = rows[chosen].ID
			}
		})
		ui.Column(c).Width(detailWidth).Shrink(0).Children(func() {
			ui.Scroll(c).FillWidth().Height(max(float32(180), contentHeight)).Children(func() {
				if selected == nil {
					r.emptyState(c, "连接详情", "点击左侧连接，查看地址、出口与流量。")
					return
				}
				row := *selected
				panel(c, func() {
					ui.Text(c, tr(c, "连接详情")).Bold().FontSize(16)
					host, port, err := net.SplitHostPort(row.Destination)
					if err != nil {
						host = row.Destination
					}
					ui.Text(c, host).Bold().FontSize(13).MaxLines(3)
					if port != "" {
						infoPair(c, "目标端口", port)
					}
					infoPair(c, "来源", row.Source)
					infoPair(c, "网络", strings.ToUpper(row.Network))
					if row.Protocol != "" {
						infoPair(c, "应用协议", strings.ToUpper(row.Protocol))
					}
					infoPair(c, "出口", connectionOutbound(row, snap))
					if row.Outbound == configgen.TagProxy {
						if row.NodeProtocol != "" {
							infoPair(c, "节点协议", strings.ToUpper(row.NodeProtocol))
						} else {
							for _, p := range snap.Profiles {
								if p.ID == snap.Active {
									infoPair(c, "节点协议", strings.ToUpper(p.Protocol))
									break
								}
							}
						}
					}
					inbound := row.Inbound
					if inbound == configgen.TagInSocks {
						inbound = "本地代理"
					} else if inbound == configgen.TagInTun {
						inbound = "TUN"
					}
					infoPair(c, "入口", inbound)
					infoPair(c, "开始时间", row.Started.Format("15:04:05"))
					infoPair(c, "持续时间", connectionDuration(row.Started, c.Now()))
					infoPair(c, "上传", transferLabel(row.Upload))
					infoPair(c, "下载", transferLabel(row.Download))
					ui.Text(c, tr(c, "流量随内核计数更新")).FontSize(10).TextColor(t.TextMuted).
						Tooltip(tr(c, "部分 VLESS Vision 直通连接会批量更新流量，数值可能暂时滞后。"))
					ui.Text(c, tr(c, "仅显示活动连接，结束后自动移除。")).FontSize(10).TextColor(t.TextMuted).MaxLines(3)
				}).Padding(16).Gap(12)
			})
		})
	})
}
