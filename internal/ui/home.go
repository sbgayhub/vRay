package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/ELY-Open-Source/CrychicUI/layout"
	"github.com/egoist/mygo/ui"
	mc "github.com/egoist/mygo/ui/chart"
	"github.com/sbgayhub/vray/internal/app"
	xcore "github.com/sbgayhub/vray/internal/core"
)

func panel(c *ui.Context, content func()) ui.Element {
	return layout.Card(c, content).FillWidth().Padding(20).Gap(12).Radius(cardRadius).Background(c.Theme().Surface)
}
func homeCard(c *ui.Context, label string, height float32, content func()) ui.Element {
	return panel(c, content).Label(label).Padding(16).Gap(10).Height(height).Radius(18)
}

const homeInfoCardHeight = 152

type homeActionOptions struct {
	label, glyph, hint string
	disabled, active   bool
}

func homeIconButton(c *ui.Context, o homeActionOptions) bool {
	t := c.Theme()
	ink := t.TextMuted
	if o.active {
		ink = accentForeground(t)
	} else if o.disabled {
		ink = ink.Alpha(0.4)
	}
	b := ui.ButtonBase(c).Label(o.label).Tooltip(tr(c, o.hint)).Size(22, 22).Center().
		Radius(7).Background(ui.Transparent).Disabled(o.disabled).Children(func() {
		ui.Icon(c, icon.Must(o.glyph)).Size(13, 13).TextColor(ink)
	})
	if b.Hovered() && !o.disabled {
		b.Background(t.SurfaceHover)
	}
	return b.Clicked()
}
func homeHeading(c *ui.Context, title, glyph string) {
	t := c.Theme()
	ui.Row(c).FillWidth().Gap(7).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon.Must(glyph)).Size(14, 14).TextColor(t.TextMuted)
		ui.Text(c, tr(c, title)).FontSize(12).TextColor(t.TextMuted)
	})
}
func (r *Root) homeLink(c *ui.Context, title, glyph string, page Page) {
	r.homeLinkAction(c, title, glyph, page, nil)
}
func (r *Root) homeLinkAction(c *ui.Context, title, glyph string, page Page, action func()) {
	t := c.Theme()
	ui.Row(c).Label(title + "卡片标题").FillWidth().Height(22).Gap(7).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon.Must(glyph)).Size(14, 14).TextColor(t.TextMuted)
		ui.Text(c, tr(c, title)).Grow(1).FontSize(12).TextColor(t.TextMuted)
		if action != nil {
			action()
		}
		if homeIconButton(c, homeActionOptions{
			label: "打开" + title, glyph: "arrow-up-right", hint: "打开" + title,
		}) {
			r.cur, r.route = page, pageRoutes[page].ID
		}
	})
}
func dashboardNode(snap app.Snapshot) (app.ProfileView, bool) {
	if snap.Settings.ActiveGroup != "" {
		if exits := dashboardPolicyExits(snap); len(exits.Nodes) > 0 {
			for _, p := range snap.Profiles {
				if p.ID == exits.Nodes[0].ID {
					return p, true
				}
			}
		}
		groups := map[string]appGroupRef{}
		for _, g := range snap.Groups {
			groups[g.ID] = appGroupRef{source: g.SourceID, kind: g.Type, selected: policySelection(g)}
		}
		root := groups[snap.Settings.ActiveGroup]
		id := snap.Settings.ActiveGroup
		for range 64 {
			if g, ok := groups[id]; ok {
				if g.kind != "select" {
					break
				}
				id = g.selected
				continue
			}
			for _, p := range snap.Profiles {
				if p.ID == id {
					return p, true
				}
			}
			break
		}
		return app.ProfileView{SourceID: root.source}, false
	}
	for _, p := range snap.Profiles {
		if p.ID == snap.Active {
			return p, true
		}
	}
	if len(snap.Profiles) == 1 {
		return snap.Profiles[0], true
	}
	return app.ProfileView{}, false
}

func dashboardPolicyExits(snap app.Snapshot) xcore.PolicyExits {
	if snap.Phase == app.PhaseConnected && snap.Dashboard.PolicySince.Equal(snap.Since) &&
		snap.Dashboard.PolicyExits.GroupID == snap.Settings.ActiveGroup {
		return snap.Dashboard.PolicyExits
	}
	return xcore.PolicyExits{}
}

type appGroupRef struct{ source, kind, selected string }

func (r *Root) home(c *ui.Context, snap app.Snapshot) {
	if exits := dashboardPolicyExits(snap); len(exits.Nodes) > 0 && r.homeInspectionID != exits.Nodes[0].ID {
		loading := false
		for _, info := range snap.NodeDetails {
			if info.Loading {
				loading = true
				break
			}
		}
		if !loading {
			r.homeInspectionID = exits.Nodes[0].ID
			if snap.NodeDetails[r.homeInspectionID].IP == "" {
				r.svc.InspectSelectedNode(r.homeInspectionID)
			}
		}
	}
	width, _ := c.Size()
	wide := width-r.sidebarWidth()-48 >= 830
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		if wide {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
				ui.Box(c).Grow(1).Basis(0).Children(func() { r.connectionCard(c, snap) })
				ui.Box(c).Grow(1).Basis(0).Children(func() {
					ui.Row(c).FillWidth().Gap(12).Children(func() {
						ui.Box(c).Grow(1).Basis(0).Children(func() { r.speedCard(c, snap, true) })
						ui.Box(c).Grow(1).Basis(0).Children(func() { r.speedCard(c, snap, false) })
					})
				})
			})
		} else {
			r.connectionCard(c, snap)
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				ui.Box(c).Grow(1).Basis(0).Children(func() { r.speedCard(c, snap, true) })
				ui.Box(c).Grow(1).Basis(0).Children(func() { r.speedCard(c, snap, false) })
			})
		}
		r.homeControls(c, snap)
		cards := []func(){func() { r.localProxyCard(c, snap) }, func() { r.subscriptionCard(c, snap) }, func() { r.qualityCard(c, snap) }, func() { r.memoryCard(c, snap) }}
		columns := 4
		if !wide {
			columns = 2
		}
		for start := 0; start < len(cards); start += columns {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
				for _, draw := range cards[start:min(start+columns, len(cards))] {
					ui.Box(c).Grow(1).Basis(0).Children(draw)
				}
			})
		}
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
			ui.Box(c).Grow(1).Basis(0).Children(func() { r.homeOverview(c, snap) })
			ui.Box(c).Grow(1).Basis(0).Children(func() { r.trafficSummary(c, snap) })
		})
		if snap.Recovered > 0 {
			ui.Text(c, fmt.Sprintf(tr(c, "上次没有正常退出，已还原 %d 处系统设置"), snap.Recovered)).FontSize(12).TextColor(c.Theme().Warning)
		}
		if snap.Problem != nil {
			ui.Text(c, "[ "+snap.Problem.Code+" ] "+snap.Problem.Message).FontSize(12).TextColor(c.Theme().Danger)
			if snap.Problem.Detail != "" {
				ui.Text(c, snap.Problem.Detail).FontSize(11).TextColor(c.Theme().TextMuted).MaxLines(2)
			}
		}
		r.geoLine(c, snap)
	})
}
func (r *Root) connectionCard(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	p, hasNode := dashboardNode(snap)
	exits := dashboardPolicyExits(snap)
	info := snap.NodeDetails[p.ID]
	groupName, groupType := "", ""
	for _, g := range snap.Groups {
		if g.ID == snap.Settings.ActiveGroup {
			groupName, groupType = g.Name, g.Type
		}
	}
	busy := snap.Phase == app.PhaseConnecting || snap.Phase == app.PhaseDisconnecting
	homeCard(c, "首页连接状态", 174, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			homeHeading(c, "连接状态", "shield")
			ui.Spacer(c)
			ink := t.TextMuted
			if snap.Phase == app.PhaseConnected {
				ink = t.Success
			} else if snap.Phase == app.PhaseFailed {
				ink = t.Danger
			}
			ui.Text(c, tr(c, phaseLabel(snap.Phase))).FontSize(11).TextColor(ink).SingleLine().Shrink(0)
		})
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Basis(0).Gap(6).Children(func() {
				name := p.Name
				if !hasNode {
					name = "选择节点，开始连接"
				}
				if info.IP != "" {
					name = info.IP
				}
				if groupName != "" && !hasNode {
					name = groupName
				}
				if snap.Settings.RoutingMode == "direct" || exits.Source == "direct" {
					name = "直连出站"
				}
				if exits.Source == "block" {
					name = "连接已拦截"
				}
				ui.Text(c, tr(c, name)).Bold().FontSize(25).SingleLine().Tooltip(name)
				subtitle := "还没有节点，请先导入订阅或分享链接"
				if hasNode {
					subtitle = strings.ToUpper(p.Protocol) + " · " + p.Name
				}
				if info.Country != "" {
					subtitle = info.Country + " · " + p.Name
				}
				if snap.Settings.RoutingMode == "direct" {
					subtitle = tr(c, "流量直接连接目标 · 当前节点 ") + p.Name
				}
				if groupName != "" && snap.Settings.RoutingMode != "direct" {
					if hasNode {
						subtitle = groupName + " · " + strings.ToUpper(p.Protocol) + " · " + p.Name
						if info.Country != "" {
							subtitle = info.Country + " · " + subtitle
						}
					} else {
						subtitle = groupName + " · " + policyType(c, groupType)
					}
					if exits.Source == "waiting" {
						subtitle = groupName + " · " + tr(c, "等待业务连接")
					}
				}
				ui.Text(c, tr(c, subtitle)).FontSize(11).SingleLine().TextColor(t.TextMuted).Tooltip(subtitle)
			})
			opts := []buttonOption{primary()}
			if busy {
				opts = append(opts, disabled())
			}
			if textButton(c, buttonLabel(snap.Phase), opts...).Height(38).Radius(14).Clicked() {
				r.svc.Toggle()
			}
		})
		ui.Spacer(c)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			label := "出口信息尚未查询"
			if info.Provider != "" {
				label = info.Provider
			} else if info.ASN != "" {
				label = info.ASN
			}
			if info.Loading {
				label = "正在查询出口信息…"
			}
			if groupName != "" && !hasNode {
				label = "按策略组分配出站"
			}
			if len(exits.Nodes) > 1 {
				label = fmt.Sprintf(tr(c, "%d 个活动节点 · %s"), len(exits.Nodes), label)
			} else if exits.Source == "recent" {
				label = tr(c, "最近使用 · ") + label
			}
			ink, tip := t.TextMuted, label
			if info.IP != "" {
				tip = strings.TrimSpace(strings.Join([]string{info.IP, info.Country, info.Region, info.City, info.ASN, info.Provider, info.Type, info.Score}, " "))
			}
			if info.Error != "" && !info.Loading {
				label, ink, tip = "出口信息查询失败", t.Warning, info.Error
			}
			ui.Text(c, tr(c, label)).FontSize(11).SingleLine().Grow(1).Basis(0).TextColor(ink).Tooltip(tip)
			if snap.Phase == app.PhaseConnected {
				ui.Text(c, tr(c, sinceLabel(snap.Since))).FontSize(11).TextColor(t.TextMuted)
			}
			opts := []buttonOption{}
			if (!hasNode && groupName == "") || info.Loading {
				opts = append(opts, disabled())
			}
			button := "查询出口"
			if info.IP != "" {
				button = "更新出口"
			}
			if groupName != "" && !hasNode {
				button = "策略组"
			}
			if len(exits.Nodes) > 1 {
				button = "查看连接"
			}
			if textButton(c, button, opts...).Height(26).FontSize(10).Clicked() {
				if len(exits.Nodes) > 1 {
					r.cur, r.route = PageConnections, pageRoutes[PageConnections].ID
				} else if groupName != "" && !hasNode {
					r.cur, r.route = PageGroups, pageRoutes[PageGroups].ID
				} else {
					r.svc.InspectNode(p.ID)
				}
			}
		})
	})
}
func (r *Root) speedCard(c *ui.Context, snap app.Snapshot, upload bool) {
	t := c.Theme()
	label, glyph, prefix, ink := "下载", "arrow-down", "↓ ", t.Accent
	rate, total := snap.Traffic.DownBps, snap.Traffic.TotalDown
	if upload {
		label, glyph, prefix, ink = "上传", "arrow-up", "↑ ", t.Success
		rate, total = snap.Traffic.UpBps, snap.Traffic.TotalUp
	}
	points := recentTraffic(snap.History)
	values := make([]float64, len(points))
	var peak int64
	for i, p := range points {
		v := p.DownBps
		if upload {
			v = p.UpBps
		}
		peak = max(peak, v)
		values[i] = float64(max(0, v))
	}
	homeCard(c, "首页"+label+"速度", 174, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, icon.Must(glyph)).Size(14, 14).TextColor(ink)
			ui.Text(c, tr(c, label)).FontSize(12).Grow(1).TextColor(t.TextMuted)
			peakText := "峰值 —"
			if snap.Phase == app.PhaseConnected {
				peakText = tr(c, "峰值 ") + rateLabel(peak)
			}
			ui.Text(c, tr(c, peakText)).FontSize(9).SingleLine().TextColor(t.TextMuted).Tooltip(tr(c, "最近 30 秒的实测峰值"))
		})
		value := "—"
		if snap.Phase == app.PhaseConnected {
			value = prefix + rateLabel(rate)
		}
		ui.Text(c, tr(c, value)).Bold().FontSize(25).SingleLine().Tooltip(value)
		ui.Row(c).FillWidth().Children(func() {
			ui.Text(c, tr(c, "本次 ")+transferLabel(total)).Grow(1).FontSize(10).TextColor(t.TextMuted)
			ui.Text(c, "30 s").FontSize(9).TextColor(t.TextMuted).Tooltip(tr(c, "最近 30 秒，每秒更新"))
		})
		ui.Spacer(c)
		ui.Box(c).FillWidth().Height(48).Label(label + "实时曲线").Draw(func(p *ui.Painter, rect ui.Rect) {
			if len(values) < 2 {
				path := new(ui.Path).MoveTo(rect.X, rect.Y+rect.H-1).LineTo(rect.X+rect.W, rect.Y+rect.H-1)
				p.StrokePath(path, 1, ink.Alpha(0.25))
				return
			}
			xs, ys := make([]float32, len(values)), make([]float32, len(values))
			for i, v := range values {
				end := points[len(points)-1].At
				xs[i] = 1 - float32(end.Sub(points[i].At))/float32(app.TrafficHistoryWindow)
				ys[i] = float32(v/math.Max(1, float64(peak))) * 0.9
			}
			p.FillPath(mc.Area(rect, xs, nil, ys, mc.CurveMonotone), ink.Alpha(0.12))
			p.StrokePath(mc.Line(rect, xs, ys, mc.CurveMonotone), 1.6, ink)
		})
	})
}

func recentTraffic(points []app.TrafficPoint) []app.TrafficPoint {
	if len(points) == 0 {
		return points
	}
	cutoff := points[len(points)-1].At.Add(-app.TrafficHistoryWindow)
	first := 0
	for first < len(points) && points[first].At.Before(cutoff) {
		first++
	}
	return points[first:]
}
func (r *Root) homeControls(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	busy := snap.RoutingSaving || snap.Phase == app.PhaseConnecting || snap.Phase == app.PhaseDisconnecting
	routingMode := snap.Settings.RoutingMode
	if routingMode == "" {
		routingMode = "rule"
	}
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Box(c).Grow(1).Basis(0).Children(func() {
			homeCard(c, "首页出站方式", 86, func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					homeHeading(c, "出站方式", "git-branch")
					if snap.RoutingSaving {
						ui.Text(c, tr(c, "切换中…")).FontSize(10).SingleLine().Shrink(0).TextColor(t.TextMuted)
					}
				})
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					for _, mode := range []struct{ label, value, hint string }{
						{"规则", "rule", "按用户规则和 Geo 规则分流，未匹配的流量使用当前节点"},
						{"全局", "global", "除本地网络外，全部流量使用当前节点"},
						{"直连", "direct", "流量直接连接目标；保留代理入口和当前节点配置"},
					} {
						opts := []buttonOption{}
						if routingMode == mode.value {
							opts = append(opts, tinted())
						}
						if busy {
							opts = append(opts, disabled())
						}
						if textButton(c, mode.label, opts...).Height(28).Radius(14).FontSize(11).
							Tooltip(mode.hint+"；连接中切换将重新连接").Clicked() && routingMode != mode.value {
							r.svc.SetRoutingMode(mode.value)
						}
					}
				})
			}).Padding(12, 16).Gap(8)
		})
		ui.Box(c).Grow(1).Basis(0).Children(func() {
			homeCard(c, "首页代理方式", 86, func() {
				homeHeading(c, "代理方式", "network")
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					for _, mode := range []struct{ name, value string }{{"系统代理", "system-proxy"}, {"TUN", "tun"}} {
						opts := []buttonOption{}
						if snap.Mode == mode.value {
							opts = append(opts, tinted())
						}
						if busy {
							opts = append(opts, disabled())
						}
						if textButton(c, mode.name, opts...).Height(28).Radius(14).FontSize(11).
							Clicked() && snap.Mode != mode.value {
							r.svc.SetMode(mode.value)
						}
					}
					ui.Spacer(c)
					if textButton(c, "代理设置").Height(28).FontSize(11).Clicked() {
						r.cur, r.route = PageSettings, pageRoutes[PageSettings].ID
					}
				})
			}).Padding(12, 16).Gap(8)
		})
	})
}
func (r *Root) localProxyCard(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	homeCard(c, "首页本地代理", homeInfoCardHeight, func() {
		if snap.Mode == "tun" {
			r.homeLink(c, "虚拟网卡", "network", PageSettings)
			ui.Text(c, "TUN").Label("本地代理主要信息").Bold().FontSize(22).Height(30).SingleLine()
			ui.Text(c, tr(c, "通过虚拟网卡接管流量")).FontSize(11).Height(16).SingleLine().TextColor(t.TextMuted)
			ui.Spacer(c)
			state := "未运行"
			if snap.Phase == app.PhaseConnected {
				state = "虚拟网卡运行中"
			}
			ui.Text(c, tr(c, state)).Height(14).FontSize(10).SingleLine().TextColor(t.TextMuted)
			return
		}
		port := snap.Settings.MixedPort
		copied := c.Now().Before(r.homeCopiedUntil)
		r.homeLinkAction(c, "本地代理", "wifi", PageSettings, func() {
			glyph, hint := "copy", "复制本地 HTTP / SOCKS 代理地址"
			if copied {
				glyph, hint = "check", "已复制代理地址"
			}
			if homeIconButton(c, homeActionOptions{
				label: "复制地址", glyph: glyph, hint: hint, disabled: port <= 0, active: copied,
			}) {
				c.WriteClipboard(fmt.Sprintf("127.0.0.1:%d", port))
				r.homeCopiedUntil = c.Now().Add(2 * time.Second)
			}
		})
		ui.Text(c, "127.0.0.1").Label("本地代理主要信息").Bold().FontSize(22).Height(30).SingleLine()
		if port == 0 {
			ui.Text(c, tr(c, "端口未设置")).FontSize(11).Height(16).SingleLine().TextColor(t.TextMuted)
		} else {
			ui.Text(c, fmt.Sprintf(tr(c, "HTTP / SOCKS 端口 %d"), port)).FontSize(11).Height(16).SingleLine().TextColor(t.TextMuted)
		}
		ui.Spacer(c)
		ui.Row(c).FillWidth().Height(14).AlignItems(ui.Center).Children(func() {
			state := "未监听"
			if snap.Phase == app.PhaseConnected {
				state = tr(c, "已运行 · ") + tr(c, modeLabel(snap.Mode))
			}
			ui.Text(c, tr(c, state)).FontSize(10).Grow(1).SingleLine().TextColor(t.TextMuted)
			if copied {
				ui.Text(c, tr(c, "已复制")).FontSize(10).TextColor(accentForeground(t))
			}
		})
	}).Gap(8)
}
func (r *Root) subscriptionCard(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	node, hasNode := dashboardNode(snap)
	var source *app.SourceView
	for i := range snap.Sources {
		if snap.Sources[i].ID == node.SourceID {
			source = &snap.Sources[i]
			break
		}
	}
	homeCard(c, "首页订阅概览", homeInfoCardHeight, func() {
		r.homeLinkAction(c, "当前订阅", "file-text", PageSources, func() {
			updating := source != nil && source.Updating
			hint := "更新当前节点所属的订阅"
			if source != nil {
				if !source.LastFetchedAt.IsZero() {
					hint += "\n上次更新于 " + source.LastFetchedAt.Local().Format("01-02 15:04")
				}
				if source.LastError != "" {
					hint += "\n上次更新失败：" + source.LastError
				}
			}
			if homeIconButton(c, homeActionOptions{
				label: "更新当前订阅", glyph: "refresh-cw", hint: hint,
				disabled: source == nil || updating, active: updating,
			}) && source != nil {
				r.svc.RefreshSource(source.ID)
			}
		})
		if source == nil {
			label := "本地节点"
			if !hasNode {
				label = "尚未选择节点"
			}
			if len(snap.Profiles) == 0 {
				label = "尚未添加订阅"
			}
			ui.Text(c, tr(c, label)).Bold().FontSize(22).SingleLine().Tooltip(label)
			ui.Text(c, fmt.Sprintf(tr(c, "共 %d 个订阅来源"), len(snap.Sources))).FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			hint := "当前节点为手动导入"
			if !hasNode {
				hint = "在订阅页添加订阅或选择节点"
			}
			ui.Text(c, tr(c, hint)).FontSize(10).SingleLine().TextColor(t.TextMuted)
			return
		}
		ui.Text(c, source.Name).Bold().FontSize(22).SingleLine().Tooltip(source.Name)
		expiry := "未提供到期时间"
		if !source.ExpireAt.IsZero() {
			days := int(math.Ceil(source.ExpireAt.Sub(c.Now()).Hours() / 24))
			expiry = fmt.Sprintf(tr(c, "剩余 %d 天"), max(0, days))
			if days <= 0 {
				expiry = "已到期"
			}
		}
		ui.Text(c, tr(c, expiry)).FontSize(11).TextColor(t.TextMuted)
		ui.Spacer(c)
		quota := "订阅未提供流量信息"
		if source.Total > 0 {
			used := max(0, source.Upload) + max(0, source.Download)
			fraction := float64(used) / float64(source.Total)
			quota = fmt.Sprintf(tr(c, "%.0f%% 已用 · %s / %s"), fraction*100, transferLabel(used), transferLabel(source.Total))
		}
		status, detail, ink := quota, quota, t.TextMuted
		if source.Updating {
			status = "正在更新订阅…"
		} else if source.LastError != "" {
			status, detail, ink = "订阅更新失败", source.LastError+"\n"+quota, t.Warning
		}
		ui.Text(c, tr(c, status)).FontSize(10).SingleLine().TextColor(ink).Tooltip(detail)
	}).Gap(8)
}
func (r *Root) qualityCard(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	node, hasNode := dashboardNode(snap)
	latency := r.svc.Latency(node.ID)
	homeCard(c, "首页节点质量", homeInfoCardHeight, func() {
		r.homeLinkAction(c, "节点质量", "gauge", PageNodes, func() {
			if homeIconButton(c, homeActionOptions{
				label: "检测节点", glyph: "activity", hint: "检测当前节点的 TCP 建连延迟",
				disabled: !hasNode || r.svc.Testing(), active: latency.State == app.LatencyPending,
			}) {
				r.svc.TestNode(node.ID)
			}
		})
		value := app.LatencyLabel(latency)
		if value == "" {
			value = "未测速"
		}
		if latency.State == app.LatencyPending {
			value = "检测中…"
		}
		ui.Text(c, tr(c, value)).Bold().FontSize(25).Tooltip(tr(c, "当前节点的 TCP 建连耗时"))
		ui.Text(c, tr(c, "TCP 建连延迟")).FontSize(11).TextColor(t.TextMuted)
		ui.Spacer(c)
		speed := snap.NodeSpeeds[node.ID]
		label := "下载速度未测试"
		if !speed.CheckedAt.IsZero() {
			label = tr(c, "下载 ") + rateLabel(speed.BytesPerSecond)
		}
		if speed.Running {
			label = "正在测试下载速度…"
		}
		ui.Text(c, tr(c, label)).FontSize(11).SingleLine().TextColor(t.TextMuted)
	}).Gap(8)
}
func (r *Root) memoryCard(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	data := snap.Dashboard
	ready := !data.At.IsZero()
	bytes, label := data.Memory.WorkingSetBytes, "进程工作集"
	if bytes == 0 {
		bytes, label = data.Memory.RuntimeUsedBytes, "Go 运行时内存"
	}
	released := !data.MemoryReleasedAt.IsZero() && c.Now().Sub(data.MemoryReleasedAt) < 2*time.Second
	homeCard(c, "首页运行内存", homeInfoCardHeight, func() {
		r.homeLinkAction(c, "运行内存", "memory-stick", PageLogs, func() {
			glyph, hint := "eraser", "执行 GC 并将空闲 Go 堆内存归还系统；仍在使用的内存会保留"
			if data.MemoryReleasing {
				glyph, hint = "refresh-cw", "正在释放内存…"
			} else if released {
				glyph, hint = "check", "已完成内存释放"
			}
			if homeIconButton(c, homeActionOptions{
				label: "释放内存", glyph: glyph, hint: hint,
				disabled: data.MemoryReleasing, active: data.MemoryReleasing || released,
			}) {
				r.svc.ReleaseMemory()
			}
		})
		value := "—"
		if ready {
			value = transferLabel(int64(bytes))
		}
		ui.Text(c, tr(c, value)).Bold().FontSize(26).SingleLine()
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(4).Children(func() {
			ui.Text(c, tr(c, label)).Grow(1).SingleLine().FontSize(10).TextColor(t.TextMuted)
			if data.MemoryReleasing {
				ui.Text(c, tr(c, "释放中…")).FontSize(10).TextColor(accentForeground(t))
			} else if released {
				ui.Text(c, tr(c, "已释放")).FontSize(10).TextColor(accentForeground(t))
			}
		})
		ui.Spacer(c)
		if ready {
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, tr(c, "Go 堆 ")+transferLabel(int64(data.Memory.HeapAllocBytes))).FontSize(10).TextColor(t.TextMuted)
				ui.Spacer(c)
				ui.Text(c, fmt.Sprintf(tr(c, "%d 个协程"), data.Memory.Goroutines)).FontSize(10).TextColor(t.TextMuted)
			})
		} else {
			ui.Text(c, tr(c, "正在读取运行状态…")).FontSize(10).TextColor(t.TextMuted)
		}
	}).Gap(8)
}
func (r *Root) homeOverview(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	rules := 0
	for _, rule := range snap.Rules {
		if rule.Enabled {
			rules++
		}
	}
	homeCard(c, "首页运行概览", 126, func() {
		homeHeading(c, "运行概览", "activity")
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			for _, stat := range []struct {
				title string
				value int
				page  Page
			}{{"实时连接", snap.Dashboard.Connections, PageConnections}, {"节点总数", len(snap.Profiles), PageNodes}, {"启用规则", rules, PageRules}} {
				button := ui.ButtonBase(c).Label("查看" + stat.title).Grow(1).Basis(0).Padding(8).Radius(10).Background(ui.Transparent).Children(func() {
					ui.Column(c).FillWidth().Gap(6).Children(func() {
						value := fmt.Sprint(stat.value)
						if stat.page == PageConnections && snap.Dashboard.At.IsZero() {
							value = "—"
						}
						ui.Text(c, tr(c, value)).FontSize(23).Bold()
						ui.Text(c, tr(c, stat.title)).FontSize(10).SingleLine().TextColor(t.TextMuted)
					})
				})
				if button.Hovered() {
					button.Background(t.SurfaceHover)
				}
				if button.Clicked() {
					r.cur, r.route = stat.page, pageRoutes[stat.page].ID
				}
			}
		})
	})
}
func (r *Root) trafficSummary(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	var upload, download int64
	for _, u := range snap.Usage {
		upload += u.Upload
		download += u.Download
	}
	homeCard(c, "首页累计流量", 126, func() {
		homeHeading(c, "流量统计", "layers")
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			for _, v := range []struct {
				label string
				value int64
			}{{"累计上传", upload}, {"累计下载", download}} {
				ui.Column(c).Grow(1).Basis(0).Gap(5).Children(func() {
					ui.Text(c, tr(c, v.label)).FontSize(10).TextColor(t.TextMuted)
					ui.Text(c, transferLabel(v.value)).FontSize(19).Bold().SingleLine()
				})
			}
		})
		ui.Text(c, tr(c, "本次连接 ")+transferLabel(snap.Traffic.TotalUp+snap.Traffic.TotalDown)).FontSize(10).TextColor(t.TextMuted)
	})
}
func transferLabel(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(max(0, n))
	i := 0
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", value, units[i])
	}
	return fmt.Sprintf("%.2f %s", value, units[i])
}
func infoPair(c *ui.Context, name, value string) {
	t := c.Theme()
	if value == "" {
		value = "—"
	}
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Start).Children(func() {
		ui.Text(c, tr(c, name)).FontSize(12).TextColor(t.TextMuted).Shrink(0)
		ui.Spacer(c)
		ui.Text(c, value).FontSize(12).MaxLines(3)
	})
}
