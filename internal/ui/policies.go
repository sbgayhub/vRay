package ui

import (
	"fmt"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

func policyType(c *ui.Context, kind string) string {
	switch kind {
	case "select":
		return tr(c, "手动选择")
	case "url-test":
		return tr(c, "自动测速")
	case "fallback":
		return tr(c, "故障转移")
	case "load-balance":
		return tr(c, "负载均衡")
	}
	return kind
}

func policyGlyph(kind string) string {
	switch kind {
	case "select":
		return "mouse-pointer-2"
	case "url-test":
		return "gauge"
	case "fallback":
		return "shield"
	case "load-balance":
		return "network"
	}
	return "layers"
}

func policyDescription(kind string) string {
	switch kind {
	case "select":
		return "点击成员即可选择并保存；组内箭头可进入下级分组。"
	case "url-test":
		return "按 HTTP 健康检测的延迟自动选择节点，无需手动切换。"
	case "fallback":
		return "优先使用靠前的健康节点；延迟排序仅改变显示顺序。"
	case "load-balance":
		return "新连接按成员轮询分配，适合同时使用多条线路。"
	}
	return ""
}

func policyBadge(c *ui.Context, label string, ink ui.Color) {
	ui.Box(c).Padding(3, 7).Radius(6).Background(ink.Alpha(.10)).Shrink(0).Children(func() {
		ui.Text(c, tr(c, label)).FontSize(10).SingleLine().TextColor(ink)
	})
}

func policyIconButton(c *ui.Context, options homeActionOptions) bool {
	t := c.Theme()
	ink, bg := t.TextMuted, ui.Transparent
	if options.active {
		ink, bg = accentForeground(t), t.Accent.Alpha(.10)
	}
	b := ui.ButtonBase(c).Label(options.label).Tooltip(tr(c, options.hint)).
		Size(32, 32).Radius(9).Center().Background(bg).Disabled(options.disabled).
		Children(func() { ui.Icon(c, icon.Must(options.glyph)).Size(15, 15).TextColor(ink) })
	if b.Hovered() && !options.disabled {
		b.Background(t.SurfaceHover)
	}
	return b.Clicked()
}

func (r *Root) groupsPage(c *ui.Context, snap app.Snapshot) {
	if len(snap.Groups) == 0 {
		r.emptyState(c, "暂无策略组", "可新建自定义策略组，或导入包含 proxy-groups 的 Clash 订阅。")
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if textButton(c, "前往订阅").Clicked() {
				r.cur, r.route = PageSources, pageRoutes[PageSources].ID
			}
			ui.Spacer(c)
		})
		return
	}
	r.refreshPolicyHealth(c, snap)
	index := r.policiesIndex(snap)
	g, ok := index.groups[r.selectedGroup]
	if !ok {
		r.selectedGroup = snap.Settings.ActiveGroup
		g, ok = index.groups[r.selectedGroup]
		if !ok {
			g = snap.Groups[0]
			r.selectedGroup = g.ID
		}
		r.groups.trail = nil
	}
	width, height := c.Size()
	available := max(float32(260), height-captionHeight-48-20-60)
	listWidth := float32(224)
	if width-r.sidebarWidth() < 820 {
		listWidth = 180
	}
	latencies := r.svc.Latencies()
	ui.Row(c).Label("策略组工作区").FillWidth().Height(available).Gap(16).
		AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(listWidth).Shrink(0).Children(func() {
			r.policyNavigation(c, snap, index)
		})
		ui.Column(c).Grow(1).Basis(0).Children(func() {
			r.policyDetail(c, snap, index, g, latencies)
		})
	})
}

func (r *Root) policyNavigation(c *ui.Context, snap app.Snapshot, index policyIndex) {
	t := c.Theme()
	panel(c, func() {
		searchInput(c, "搜索策略组", &r.groups.groupQuery, "分组 / 订阅")
		groups := index.filterGroups(snap.Groups, r.groups.groupQuery)
		queryChanged := r.groups.lastGroupQuery != r.groups.groupQuery
		if queryChanged {
			r.groups.navigation.ScrollTo(0, ui.Start)
			r.groups.lastGroupQuery = r.groups.groupQuery
		}
		if queryChanged || r.groups.navigationGroup != r.selectedGroup {
			for i, g := range groups {
				if g.ID == r.selectedGroup {
					r.groups.navigation.ScrollIntoView(i)
					break
				}
			}
			r.groups.navigationGroup = r.selectedGroup
		}
		r.groups.navigation.Key = func(i int) any { return groups[i].ID }
		r.groups.navigation.Label = func(i int) string { return groups[i].Name }
		ui.List(c, &r.groups.navigation, len(groups), func(i int) {
			g := groups[i]
			selected := r.selectedGroup == g.ID
			bg, border := ui.Transparent, ui.Transparent
			if selected {
				bg, border = t.Accent.Alpha(.10), t.Accent.Alpha(.45)
			}
			b := ui.ButtonBase(c).Label("查看策略组 "+g.Name).FillWidth().Height(73).
				Margin(0, 0, 6, 0).Padding(11).Radius(11).Background(bg).Border(1, border).
				Checked(selected).Children(func() {
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					ui.Row(c).FillWidth().Gap(7).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icon.Must(policyGlyph(g.Type))).Size(13, 13).
							TextColor(accentForeground(t))
						ui.Text(c, g.Name).Grow(1).Basis(0).FontSize(13).Bold().
							SingleLine().Tooltip(g.Name)
						if snap.Settings.ActiveGroup == g.ID {
							ui.Icon(c, icon.Must("check")).Size(13, 13).TextColor(accentForeground(t))
						}
					})
					ui.Row(c).FillWidth().Children(func() {
						ui.Text(c, policyType(c, g.Type)).FontSize(10).TextColor(t.TextMuted).SingleLine()
						ui.Spacer(c)
						ui.Text(c, fmt.Sprint(len(g.Members))).FontSize(10).TextColor(t.TextMuted).
							Tooltip(fmt.Sprintf(tr(c, "%d 个成员"), len(g.Members)))
					})
					name := policySourceName(c, index, g)
					ui.Text(c, name).FontSize(10).TextColor(t.TextMuted).SingleLine().Tooltip(name)
				})
			})
			if b.Hovered() && !selected {
				b.Background(t.SurfaceHover)
			}
			if b.Clicked() {
				r.openPolicy(g.ID, false)
			}
		}).Label("策略组列表").Grow(1).Basis(0)
		if len(groups) == 0 {
			ui.Text(c, tr(c, "没有匹配的分组")).FontSize(11).TextColor(t.TextMuted)
		}
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon.Must("layers")).Size(13, 13).TextColor(t.TextMuted)
			label := tr(c, "单节点出站")
			if active, ok := index.groups[snap.Settings.ActiveGroup]; ok {
				label = active.Name
			}
			ui.Text(c, tr(c, label)).FontSize(10).SingleLine().Grow(1).Basis(0).TextColor(t.TextMuted).
				Tooltip(tr(c, "当前默认出站") + " · " + label)
		})
	}).FillHeight().Padding(12).Gap(10).Radius(18).Border(1, t.Border)
}

func (r *Root) policyDetail(
	c *ui.Context,
	snap app.Snapshot,
	index policyIndex,
	g store.PolicyGroup,
	latencies map[string]app.Latency,
) {
	t := c.Theme()
	_, windowHeight := c.Size()
	compact := windowHeight < 600
	panel(c, func() {
		if n := len(r.groups.trail); n > 0 {
			parent := r.groups.trail[n-1]
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if policyIconButton(c, homeActionOptions{label: "返回上级分组", glyph: "arrow-left", hint: "返回上级分组"}) {
					r.openPolicy(parent, true)
				}
				ui.Text(c, index.name(parent)).FontSize(11).SingleLine().TextColor(t.TextMuted)
			})
		}
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(36, 36).Shrink(0).Radius(12).Center().Background(t.Accent.Alpha(.10)).Children(func() {
				ui.Icon(c, icon.Must(policyGlyph(g.Type))).Size(18, 18).TextColor(accentForeground(t))
			})
			ui.Column(c).Grow(1).Basis(0).Gap(5).Children(func() {
				ui.Text(c, g.Name).FontSize(18).Bold().SingleLine().
					Tooltip(g.Name + "\n" + tr(c, policyDescription(g.Type)))
				name := policySourceName(c, index, g)
				ui.Text(c, policyType(c, g.Type)+" · "+name).FontSize(10).SingleLine().TextColor(t.TextMuted)
			})
			if snap.Settings.ActiveGroup == g.ID {
				label := "默认"
				if snap.Phase == app.PhaseConnected && snap.Settings.RoutingMode != "direct" {
					label = "使用中"
				}
				policyBadge(c, label, accentForeground(t))
			} else if index.used[g.ID] && snap.Phase == app.PhaseConnected && snap.Settings.RoutingMode != "direct" {
				policyBadge(c, "关联出站", accentForeground(t))
			}
			if policyGroupOwned(g) {
				if textButton(c, "编辑策略组").Disabled(snap.GroupBusy).Height(28).FontSize(10).
					Tooltip(tr(c, "修改名称、类型、成员顺序或按出口信息重新组合节点")).Clicked() {
					r.openEditPolicyGroup(g)
				}
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			label := tr(c, "自动分配出口")
			if g.Type == "select" {
				ui.Text(c, tr(c, "已选")).FontSize(10).TextColor(t.TextMuted).Shrink(0)
				label = strings.Join(index.path(g.ID), "  →  ")
				if label == "" {
					label = tr(c, "暂无可选成员")
				}
			}
			ui.Text(c, tr(c, label)).FontSize(12).SingleLine().Grow(1).Basis(0).Tooltip(label)
			active := snap.Settings.ActiveGroup == g.ID
			buttonLabel := "使用策略组"
			opts := []buttonOption{primary()}
			if active {
				buttonLabel = "已设为默认"
				opts = []buttonOption{tinted(), disabled()}
			}
			if snap.GroupBusy {
				buttonLabel = "应用中…"
				opts = append(opts, disabled())
			}
			if len(g.Members) == 0 {
				opts = append(opts, disabled())
			}
			if textButton(c, buttonLabel, opts...).Height(30).FontSize(11).
				Tooltip(tr(c, "修改当前出站会重新连接；未连接时只保存选择")).Clicked() {
				r.svc.SetPolicy(g.ID, "", true)
			}
		})
		description := tr(c, policyDescription(g.Type))
		ink := t.TextMuted
		if snap.GroupBusy {
			description, ink = tr(c, "正在应用策略…"), accentForeground(t)
		} else if snap.GroupResult != "" {
			description, ink = tr(c, snap.GroupResult), t.Warning
		}
		if !compact || snap.GroupResult != "" {
			ui.Text(c, description).FontSize(11).SingleLine().TextColor(ink).Tooltip(description)
		}
		ui.Divider(c)
		r.policyMemberToolbar(c, snap, g)
		ids := r.groups.filterMembers(index, g, latencies)
		ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
			for _, filter := range []struct{ name, kind string }{
				{name: "全部", kind: ""}, {name: "节点", kind: "nodes"}, {name: "分组", kind: "groups"},
			} {
				opts := []buttonOption{}
				if r.groups.memberKind == filter.kind {
					opts = append(opts, tinted())
				}
				if textButton(c, filter.name, opts...).Label("筛选成员 " + filter.kind).Height(25).FontSize(10).Clicked() {
					r.groups.memberKind = filter.kind
				}
			}
			ui.Spacer(c)
			ui.Text(c, fmt.Sprintf("%d / %d", len(ids), len(g.Members))).FontSize(10).TextColor(t.TextMuted)
		})
		if r.groups.lastMemberQuery != r.groups.memberQuery || r.groups.lastMemberKind != r.groups.memberKind {
			r.groups.members.ScrollTo(0, ui.Start)
			r.groups.lastMemberQuery, r.groups.lastMemberKind = r.groups.memberQuery, r.groups.memberKind
		}
		r.groups.members.Key = func(i int) any { return ids[i] }
		r.groups.members.Label = func(i int) string { return index.name(ids[i]) }
		ui.List(c, &r.groups.members, len(ids), func(i int) {
			r.policyMemberRow(c, snap, index, g, ids[i], latencies)
		}).Label("策略组成员").Grow(1).Basis(0)
		if len(ids) == 0 {
			ui.Text(c, tr(c, "没有匹配的成员，请调整搜索或筛选。")).FontSize(11).TextColor(t.TextMuted)
		}
		status := "选择会自动保存，连接后生效"
		if snap.Phase == app.PhaseConnected {
			status = "选择自动保存；仅更改当前出站时重新连接"
		}
		if snap.Settings.RoutingMode == "direct" {
			status = "直连模式已开启，策略组将在规则或全局模式下使用"
		}
		if !compact {
			ui.Text(c, tr(c, status)).FontSize(10).SingleLine().TextColor(t.TextMuted).Tooltip(tr(c, status))
		}
	}).FillHeight().Padding(16).Gap(8).Radius(18).Border(1, t.Border)
}

func policySourceName(c *ui.Context, index policyIndex, g store.PolicyGroup) string {
	if strings.HasPrefix(g.ID, "custom:") || strings.HasPrefix(g.SourceID, "custom:") {
		return tr(c, "自定义策略组")
	}
	if name := index.sources[g.SourceID]; name != "" {
		return name
	}
	return tr(c, "本地分组")
}

func (r *Root) policyMemberToolbar(c *ui.Context, snap app.Snapshot, g store.PolicyGroup) {
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Grow(1).Basis(0).Children(func() {
			searchInput(c, "搜索策略组成员", &r.groups.memberQuery, "搜索成员或协议")
		})
		if policyIconButton(c, homeActionOptions{
			label: "按延迟排序", glyph: "arrow-down-up", hint: "按延迟排序；再次点击恢复订阅顺序",
			active: r.groups.sortLatency,
		}) {
			r.groups.sortLatency = !r.groups.sortLatency
		}
		if r.svc.Testing() {
			if policyIconButton(c, homeActionOptions{label: "停止组内检测", glyph: "square", hint: "停止延迟检测"}) {
				r.svc.CancelTest()
			}
		} else if policyIconButton(c, homeActionOptions{
			label: "检测组内节点", glyph: "activity", hint: "检测本组及下级分组的 TCP 节点，不切换出站",
			disabled: snap.GroupBusy,
		}) {
			r.svc.TestPolicy(g.ID)
		}
	})
}

func (r *Root) policyMemberRow(
	c *ui.Context,
	snap app.Snapshot,
	index policyIndex,
	g store.PolicyGroup,
	id string,
	latencies map[string]app.Latency,
) {
	t := c.Theme()
	p, isNode := index.nodes[id]
	child, isGroup := index.groups[id]
	name := index.name(id)
	selectable := isNode || isGroup || id == "DIRECT" || id == "REJECT" || id == "REJECT-DROP"
	selected := g.Type == "select" && policySelection(g) == id
	bg, border := ui.Transparent, ui.Transparent
	if selected {
		bg, border = t.Accent.Alpha(.09), t.Accent.Alpha(.4)
	}
	ui.Row(c).FillWidth().Height(65).Margin(0, 0, 5, 0).Padding(4).
		Radius(11).Background(bg).Border(1, border).AlignItems(ui.Center).Gap(3).Children(func() {
		label := "选择成员 " + name
		if g.Type != "select" {
			label = "查看成员 " + name
		}
		b := ui.ButtonBase(c).Label(label).Grow(1).Basis(0).FillHeight().Padding(5).
			Radius(8).Background(ui.Transparent).Disabled(!selectable || snap.GroupBusy).
			Checked(selected).Children(func() {
			ui.Row(c).FillWidth().Gap(9).AlignItems(ui.Center).Children(func() {
				glyph := "server"
				if isGroup {
					glyph = "layers"
				} else if !isNode {
					glyph = "network"
				}
				ui.Box(c).Size(30, 30).Shrink(0).Center().Radius(9).
					Background(t.Accent.Alpha(.08)).Children(func() {
					ui.Icon(c, icon.Must(glyph)).Size(15, 15).TextColor(accentForeground(t))
				})
				ui.Column(c).Grow(1).Basis(0).Gap(5).Children(func() {
					ui.Text(c, name).Bold().FontSize(12).SingleLine().Tooltip(name)
					meta := tr(c, "直连，不经过代理")
					if isNode {
						meta = strings.ToUpper(p.Protocol)
						if source := index.sources[p.SourceID]; source != "" {
							meta += " · " + source
						}
					} else if isGroup {
						meta = policyType(c, child.Type) + " · " + fmt.Sprintf(tr(c, "%d 个成员"), len(child.Members))
					} else if id != "DIRECT" {
						meta = tr(c, "拦截连接")
					}
					ui.Text(c, meta).FontSize(10).SingleLine().TextColor(t.TextMuted).Tooltip(meta)
				})
				if isNode {
					r.policyLatency(c, id, p.Protocol, latencies)
				}
				if selected {
					ui.Icon(c, icon.Must("check")).Size(14, 14).TextColor(accentForeground(t))
				}
			})
		})
		if g.Type == "select" {
			hint := "仅保存选择，不影响当前连接"
			if index.used[g.ID] && snap.Phase == app.PhaseConnected && snap.Settings.RoutingMode != "direct" {
				hint = "此分组参与当前出站，切换成员会重新连接"
			}
			b.Tooltip(tr(c, hint))
		}
		if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
		if b.Clicked() {
			switch {
			case g.Type == "select" && !selected:
				r.svc.SetPolicy(g.ID, id, false)
			case g.Type != "select" && isGroup:
				r.openPolicy(id, true)
			case g.Type != "select" && isNode:
				r.openPolicyNode(id)
			}
		}
		if isGroup {
			if policyIconButton(c, homeActionOptions{label: "打开分组 " + name, glyph: "chevron-right", hint: "进入分组查看成员"}) {
				r.openPolicy(id, true)
			}
		} else if isNode {
			if policyIconButton(c, homeActionOptions{label: "查看节点 " + name, glyph: "arrow-up-right", hint: "查看节点详情"}) {
				r.openPolicyNode(id)
			}
		}
	})
}

func (r *Root) openPolicyNode(id string) {
	r.selectedNode = id
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	r.svc.InspectSelectedNode(id)
}

func (r *Root) policyLatency(c *ui.Context, id, protocol string, latencies map[string]app.Latency) {
	t := c.Theme()
	value, kind, ink := tr(c, "未检测"), "TCP", t.TextMuted
	if protocol == "hysteria2" || protocol == "hysteria" {
		kind = "UDP"
	}
	if health, ok := r.groups.health[id]; ok {
		kind = "HTTP"
		value, ink = tr(c, "不可用"), t.Warning
		if health.Alive {
			value, ink = fmt.Sprintf("%d ms", health.Delay), t.Success
		}
	} else if kind == "TCP" {
		result := latencies[id]
		switch result.State {
		case app.LatencyOK:
			value, ink = fmt.Sprintf("%d ms", result.MS), t.Success
		case app.LatencyPending:
			value = tr(c, "检测中…")
		case app.LatencyFailed:
			value = tr(c, "连接失败")
		case app.LatencySkipped:
			value = tr(c, "已取消")
		}
	}
	ui.Column(c).Width(58).Shrink(0).Gap(4).AlignItems(ui.End).Children(func() {
		ui.Text(c, tr(c, value)).FontSize(11).SingleLine().TextColor(ink)
		ui.Text(c, kind).FontSize(9).TextColor(t.TextMuted).
			Tooltip(tr(c, "HTTP 为代理健康检测，TCP 为端口建连延迟"))
	})
}
