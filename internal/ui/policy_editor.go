package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

// policyGroupEditor 保存视图局部状态；仅当用户保存时，服务才会收到一份完整且已校验的编辑。
type policyGroupEditor struct {
	open          bool
	id            string
	name          string
	kind          string
	selected      string
	query         string
	memberKind    string
	probeURL      string
	interval      string
	members       []string
	queriedOnly   bool
	selectedOnly  bool
	confirmDelete bool
	problem       string
	action        string
	saving        <-chan struct{}

	membersList                   ui.ListState
	lastQuery, lastKind           string
	lastQueried, lastSelectedOnly bool
}

func policyGroupOwned(g store.PolicyGroup) bool {
	return strings.HasPrefix(g.ID, "custom:") || strings.HasPrefix(g.SourceID, "local:")
}

func (r *Root) openNewPolicyGroup(snap app.Snapshot) {
	f := &r.policyEditor
	*f = policyGroupEditor{
		open: true, kind: "select", probeURL: snap.Settings.ProbeURL, interval: "300",
	}
}

func (r *Root) openEditPolicyGroup(g store.PolicyGroup) {
	f := &r.policyEditor
	*f = policyGroupEditor{
		open: true, id: g.ID, name: g.Name, kind: g.Type, selected: policySelection(g),
		probeURL: g.URL, interval: strconv.Itoa(max(30, g.Interval)), members: append([]string(nil), g.Members...),
	}
	if f.interval == "30" && g.Interval == 0 {
		f.interval = "300"
	}
}

func (r *Root) syncPolicyEditor(snap app.Snapshot) {
	f := &r.policyEditor
	if f.saving == nil {
		return
	}
	select {
	case <-f.saving:
		f.saving = nil
		if snap.GroupResult != "" {
			f.problem = snap.GroupResult
			f.action = ""
			return
		}
		action, id, name := f.action, f.id, f.name
		f.open, f.confirmDelete = false, false
		f.action = ""
		if action == "delete" {
			if r.selectedGroup == id {
				r.selectedGroup = ""
			}
			return
		}
		if id != "" {
			r.selectedGroup = id
			return
		}
		for _, g := range snap.Groups {
			if g.Name == name && policyGroupOwned(g) {
				r.selectedGroup = g.ID
				break
			}
		}
	default:
	}
}

func (f *policyGroupEditor) edit() app.PolicyGroupEdit {
	interval, _ := strconv.Atoi(strings.TrimSpace(f.interval))
	return app.PolicyGroupEdit{
		ID: f.id, Name: strings.TrimSpace(f.name), Type: f.kind, URL: strings.TrimSpace(f.probeURL),
		Interval: interval, Members: append([]string(nil), f.members...), Selected: f.selected,
	}
}

func (f *policyGroupEditor) localProblem() string {
	if strings.TrimSpace(f.name) == "" {
		return "请填写策略组名称"
	}
	minimum := 1
	if f.kind != "select" {
		minimum = 2
	}
	if len(f.members) < minimum {
		if minimum == 2 {
			return "自动策略组至少需要两个成员"
		}
		return "策略组至少需要一个成员"
	}
	if len(f.members) > 256 {
		return "策略组成员过多，最多支持 256 个"
	}
	if f.kind != "select" {
		interval, err := strconv.Atoi(strings.TrimSpace(f.interval))
		if err != nil || interval < 30 || interval > 86400 {
			return "探测间隔需在 30–86400 秒之间"
		}
	}
	return ""
}

func (f *policyGroupEditor) contains(id string) bool {
	return slices.Contains(f.members, id)
}

func (f *policyGroupEditor) memberIndex(id string) int {
	return slices.Index(f.members, id)
}

func (f *policyGroupEditor) toggleMember(id string) {
	if at := f.memberIndex(id); at >= 0 {
		f.members = slices.Delete(f.members, at, at+1)
		if f.selected == id {
			f.selected = ""
			if len(f.members) > 0 {
				f.selected = f.members[0]
			}
		}
		return
	}
	if len(f.members) >= 256 {
		f.problem = "策略组成员过多，最多支持 256 个"
		return
	}
	f.members = append(f.members, id)
	if f.kind == "select" && f.selected == "" {
		f.selected = id
	}
}

func (f *policyGroupEditor) moveMember(id string, delta int) {
	at := f.memberIndex(id)
	to := at + delta
	if at < 0 || to < 0 || to >= len(f.members) {
		return
	}
	f.members[at], f.members[to] = f.members[to], f.members[at]
}

func (f *policyGroupEditor) addResults(ids []string) {
	added := 0
	for _, id := range ids {
		if f.contains(id) {
			continue
		}
		if len(f.members) >= 256 {
			f.problem = "策略组成员过多，最多支持 256 个"
			break
		}
		f.members = append(f.members, id)
		added++
	}
	if f.kind == "select" && f.selected == "" && len(f.members) > 0 {
		f.selected = f.members[0]
	}
	if added > 0 {
		f.problem = ""
	}
}

func (f *policyGroupEditor) removeResults(ids []string) {
	remove := make(map[string]bool, len(ids))
	for _, id := range ids {
		remove[id] = true
	}
	f.members = slices.DeleteFunc(f.members, func(id string) bool { return remove[id] })
	if !f.contains(f.selected) {
		f.selected = ""
		if len(f.members) > 0 {
			f.selected = f.members[0]
		}
	}
}

func (f *policyGroupEditor) resetListScroll() {
	if f.lastQuery != f.query || f.lastKind != f.memberKind ||
		f.lastQueried != f.queriedOnly || f.lastSelectedOnly != f.selectedOnly {
		f.membersList.ScrollTo(0, ui.Start)
		f.lastQuery, f.lastKind = f.query, f.memberKind
		f.lastQueried, f.lastSelectedOnly = f.queriedOnly, f.selectedOnly
	}
}

func (f *policyGroupEditor) options(snap app.Snapshot, index policyIndex) []string {
	ids := make([]string, 0, len(snap.Profiles)+len(snap.Groups)+3)
	for _, profile := range snap.Profiles {
		ids = append(ids, profile.ID)
	}
	for _, group := range snap.Groups {
		if group.ID != f.id {
			ids = append(ids, group.ID)
		}
	}
	ids = append(ids, "DIRECT", "REJECT")
	q := strings.ToLower(strings.TrimSpace(f.query))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		_, isNode := index.nodes[id]
		if f.memberKind == "nodes" && !isNode {
			continue
		}
		if f.memberKind == "groups" && isNode {
			continue
		}
		if f.selectedOnly && !f.contains(id) {
			continue
		}
		info := snap.NodeDetails[id]
		if f.queriedOnly && (!isNode || info.IP == "") {
			continue
		}
		search := index.name(id)
		if profile, ok := index.nodes[id]; ok {
			search += " " + profile.Protocol + " " + profile.Address + " " + index.sources[profile.SourceID]
			search += " " + info.IP + " " + info.Country + " " + info.Region + " " + info.City + " " + info.ASN + " " + info.Provider
		} else if group, ok := index.groups[id]; ok {
			search += " " + policyTypeText(group.Type) + " " + index.sources[group.SourceID]
		}
		if q == "" || strings.Contains(strings.ToLower(search), q) {
			result = append(result, id)
		}
	}
	return result
}

// policyTypeText 是编辑器匹配所用的源语言名称。UI 渲染仍通过 policyType 和当前目录进行。
func policyTypeText(kind string) string {
	switch kind {
	case "select":
		return "手动选择"
	case "url-test":
		return "自动测速"
	case "fallback":
		return "故障转移"
	case "load-balance":
		return "负载均衡"
	default:
		return kind
	}
}

type policyExitFilter struct {
	label string
	query string
}

func policyExitFilters(snap app.Snapshot) []policyExitFilter {
	seen := map[string]bool{}
	out := make([]policyExitFilter, 0, 4)
	add := func(label, query string) {
		if query == "" || seen[query] || len(out) >= 4 {
			return
		}
		seen[query] = true
		out = append(out, policyExitFilter{label: label, query: query})
	}
	for _, profile := range snap.Profiles {
		info := snap.NodeDetails[profile.ID]
		add("IP "+info.IP, info.IP)
	}
	for _, profile := range snap.Profiles {
		info := snap.NodeDetails[profile.ID]
		region := strings.TrimSpace(info.Country + " " + info.Region)
		add(strings.TrimSpace(info.Country+" · "+info.Region), region)
	}
	return out
}

func (r *Root) policyGroupEditorDialog(c *ui.Context, snap app.Snapshot) {
	f := &r.policyEditor
	if !f.open {
		return
	}
	r.modal(c, "策略组编辑", &f.open, func() {
		if f.confirmDelete {
			r.policyGroupDeleteConfirmation(c, snap)
			return
		}
		r.policyGroupEditorBody(c, snap)
	})
}

func (r *Root) policyGroupDeleteConfirmation(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	f := &r.policyEditor
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Box(c).FillWidth().Padding(14).Radius(12).Background(t.Danger.Alpha(.08)).
			Border(1, t.Danger.Alpha(.24)).Children(func() {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, tr(c, "删除策略组？")).Bold().FontSize(14).TextColor(t.Danger)
				ui.Text(c, fmt.Sprintf(tr(c, "“%s”及其嵌套引用将被移除。正在使用的连接会在需要时重新连接。"), f.name)).
					FontSize(11).TextColor(t.TextMuted).MaxLines(3)
			})
		})
		if f.problem != "" || snap.GroupResult != "" {
			problem := f.problem
			if problem == "" {
				problem = snap.GroupResult
			}
			ui.Text(c, tr(c, problem)).FontSize(11).TextColor(t.Danger).MaxLines(3)
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if textButton(c, "返回编辑").Disabled(f.saving != nil).Clicked() {
				f.confirmDelete = false
				f.problem = ""
			}
			ui.Spacer(c)
			label := "确认删除"
			if f.saving != nil {
				label = "正在删除…"
			}
			opts := []buttonOption{dangerous()}
			if f.saving != nil {
				opts = append(opts, disabled())
			}
			if textButton(c, label, opts...).Clicked() {
				f.problem, f.action = "", "delete"
				f.saving = r.svc.DeletePolicyGroup(f.id)
			}
		})
	})
}

func (r *Root) policyGroupEditorBody(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	f := &r.policyEditor
	index := r.policiesIndex(snap)
	_, height := c.Size()
	listHeight := float32(260)
	if height < 700 {
		listHeight = 190
	}
	if height < 560 {
		listHeight = 135
	}
	scrollHeight := max(float32(250), height-150)

	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Scroll(c).FillWidth().MaxHeight(scrollHeight).Children(func() {
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Stretch).Children(func() {
					for _, kind := range []string{"select", "url-test", "fallback", "load-balance"} {
						r.policyGroupTypeCard(c, kind)
					}
				})

				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.End).Children(func() {
					ui.Column(c).Grow(1).Basis(0).Gap(5).Children(func() {
						ui.Text(c, tr(c, "策略组名称")).FontSize(10).TextColor(t.TextMuted)
						ui.TextInput(c, &f.name).Label(tr(c, "策略组名称")).FillWidth().Height(34).
							Placeholder(tr(c, "例如：日本固定出口"))
					})
					if f.kind != "select" {
						ui.Column(c).Grow(1).Basis(0).Gap(5).Children(func() {
							ui.Text(c, tr(c, "探测地址")).FontSize(10).TextColor(t.TextMuted)
							ui.TextInput(c, &f.probeURL).Label(tr(c, "探测地址")).FillWidth().Height(34).
								Placeholder("https://www.gstatic.com/generate_204")
						})
						ui.Column(c).Width(110).Gap(5).Children(func() {
							ui.Text(c, tr(c, "间隔（秒）")).FontSize(10).TextColor(t.TextMuted)
							ui.TextInput(c, &f.interval).Label(tr(c, "探测间隔（秒）")).Width(110).Height(34)
						})
					} else {
						ui.Column(c).Width(220).Gap(5).Children(func() {
							ui.Text(c, tr(c, "当前默认成员")).FontSize(10).TextColor(t.TextMuted)
							selected := index.name(f.selected)
							if selected == "成员已移除" {
								selected = tr(c, "尚未选择")
							}
							ui.Text(c, selected).Bold().FontSize(12).SingleLine().Tooltip(selected)
						})
					}
				})

				ids := f.options(snap, index)
				ui.Row(c).FillWidth().Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Grow(1).Basis(0).Children(func() {
						searchInput(c, "搜索可选成员", &f.query, "名称、协议、订阅、出口 IP 或地区")
					})
					for _, filter := range []struct{ label, kind string }{
						{label: "全部", kind: ""}, {label: "节点", kind: "nodes"}, {label: "分组", kind: "groups"},
					} {
						opts := []buttonOption{}
						if f.memberKind == filter.kind {
							opts = append(opts, tinted())
						}
						if textButton(c, filter.label, opts...).Height(30).FontSize(10).
							Label(fmt.Sprintf(tr(c, "成员类型 %s"), filter.kind)).Clicked() {
							f.memberKind = filter.kind
						}
					}
				})

				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					opts := []buttonOption{}
					if f.queriedOnly {
						opts = append(opts, tinted())
					}
					if textButton(c, "已查询出口", opts...).Height(30).FontSize(10).Clicked() {
						f.queriedOnly = !f.queriedOnly
						if f.queriedOnly {
							f.memberKind = "nodes"
						}
					}
					opts = nil
					if f.selectedOnly {
						opts = append(opts, tinted())
					}
					if textButton(c, "只看已选", opts...).Height(30).FontSize(10).Clicked() {
						f.selectedOnly = !f.selectedOnly
					}
					if textButton(c, "选择结果").Height(30).FontSize(10).
						Disabled(len(ids) == 0).Clicked() {
						f.addResults(ids)
					}
					if textButton(c, "取消结果").Height(30).FontSize(10).
						Disabled(len(ids) == 0).Clicked() {
						f.removeResults(ids)
					}
					ui.Spacer(c)
					ui.Text(c, tr(c, "批量操作仅作用于当前筛选结果")).FontSize(9).TextColor(t.TextMuted)
				})

				filters := policyExitFilters(snap)
				if len(filters) > 0 {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, icon.Must("globe")).Size(13, 13).TextColor(t.TextMuted)
						ui.Text(c, tr(c, "按持久化出口筛选")).FontSize(10).TextColor(t.TextMuted)
						for _, filter := range filters {
							opts := []buttonOption{}
							if f.query == filter.query {
								opts = append(opts, tinted())
							}
							if textButton(c, filter.label, opts...).Height(26).FontSize(10).Clicked() {
								f.query, f.queriedOnly, f.memberKind = filter.query, true, "nodes"
							}
						}
						if f.query != "" {
							if textButton(c, "清除筛选").Height(26).FontSize(10).Clicked() {
								f.query = ""
							}
						}
						ui.Spacer(c)
						ui.Icon(c, icon.Must("shield")).Size(12, 12).TextColor(t.TextMuted).
							Tooltip(tr(c, "出口来自节点页最近一次持久化查询"))
					})
				}

				f.resetListScroll()
				f.membersList.Key = func(i int) any { return ids[i] }
				f.membersList.Label = func(i int) string { return index.name(ids[i]) }
				ui.List(c, &f.membersList, len(ids), func(i int) {
					r.policyGroupEditorMemberRow(c, snap, index, ids[i])
				}).Label("可选策略组成员").Height(listHeight).
					Border(1, t.Border).Radius(12).Background(t.Background.Alpha(.35))
				if len(ids) == 0 {
					ui.Text(c, tr(c, "没有匹配的节点或分组。请调整搜索或先查询节点出口信息。")).FontSize(10).
						TextColor(t.TextMuted).MaxLines(2)
				}
			})
		})

		ui.Divider(c)
		problem := f.localProblem()
		if f.problem != "" {
			problem = f.problem
		}
		if f.problem != "" || (problem != "" && (strings.TrimSpace(f.name) != "" || len(f.members) > 0)) {
			ui.Text(c, tr(c, problem)).FontSize(10).TextColor(t.Danger).SingleLine().Tooltip(problem)
		} else if problem != "" {
			ui.Text(c, tr(c, "填写名称并选择成员后即可保存")).FontSize(10).TextColor(t.TextMuted)
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			if f.id != "" {
				if textButton(c, "删除策略组", dangerous()).Disabled(f.saving != nil || snap.GroupBusy).Clicked() {
					f.confirmDelete, f.problem = true, ""
				}
			}
			ui.Text(c, fmt.Sprintf(tr(c, "已选 %d / 256"), len(f.members))).FontSize(10).
				TextColor(t.TextMuted).Tooltip(tr(c, "选择顺序用于故障转移和负载均衡；可在每行上移或下移"))
			ui.Spacer(c)
			if textButton(c, "取消").Disabled(f.saving != nil).Clicked() {
				f.open = false
			}
			label := "保存策略组"
			if f.saving != nil {
				label = "正在保存…"
			}
			opts := []buttonOption{primary()}
			if problem != "" || f.saving != nil {
				opts = append(opts, disabled())
			}
			if textButton(c, label, opts...).Clicked() {
				f.problem, f.action = "", "save"
				f.saving = r.svc.SavePolicyGroup(f.edit())
			}
		})
	})
}

func (r *Root) policyGroupTypeCard(c *ui.Context, kind string) {
	t := c.Theme()
	f := &r.policyEditor
	chosen := f.kind == kind
	ink, background, border := t.Text, t.Background.Alpha(.35), t.Border
	if chosen {
		ink, background, border = accentForeground(t), t.Accent.Alpha(.11), t.Accent.Alpha(.55)
	}
	b := ui.ButtonBase(c).Label(fmt.Sprintf(tr(c, "选择策略组类型 %s"), kind)).Grow(1).Basis(0).Height(52).
		Padding(9).Radius(11).Background(background).Border(1, border).Checked(chosen).
		Children(func() {
			ui.Row(c).FillWidth().Gap(7).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(27, 27).Shrink(0).Center().Radius(8).Background(t.Accent.Alpha(.09)).
					Children(func() {
						ui.Icon(c, icon.Must(policyGlyph(kind))).Size(14, 14).TextColor(accentForeground(t))
					})
				ui.Column(c).Grow(1).Basis(0).Gap(2).Children(func() {
					ui.Text(c, policyType(c, kind)).Bold().FontSize(10).SingleLine().TextColor(ink)
					ui.Text(c, tr(c, policyEditorTypeHint(kind))).FontSize(8).SingleLine().TextColor(t.TextMuted)
				})
				if chosen {
					ui.Icon(c, icon.Must("check")).Size(13, 13).TextColor(accentForeground(t))
				}
			})
		})
	if b.Hovered() && !chosen {
		b.Background(t.SurfaceHover)
	}
	if b.Clicked() {
		f.kind = kind
		f.problem = ""
		if kind == "select" && f.selected == "" && len(f.members) > 0 {
			f.selected = f.members[0]
		}
	}
}

func policyEditorTypeHint(kind string) string {
	switch kind {
	case "select":
		return "手动固定"
	case "url-test":
		return "最低延迟"
	case "fallback":
		return "顺序容灾"
	case "load-balance":
		return "轮询分流"
	default:
		return ""
	}
}

func (r *Root) policyGroupEditorMemberRow(c *ui.Context, snap app.Snapshot, index policyIndex, id string) {
	t := c.Theme()
	f := &r.policyEditor
	profile, isNode := index.nodes[id]
	group, isGroup := index.groups[id]
	name := index.name(id)
	selectedAt := f.memberIndex(id)
	selected := selectedAt >= 0
	background, border := ui.Transparent, ui.Transparent
	if selected {
		background, border = t.Accent.Alpha(.08), t.Accent.Alpha(.35)
	}
	ui.Row(c).FillWidth().Height(59).Margin(0, 0, 5, 0).Padding(4).Radius(10).
		Background(background).Border(1, border).Gap(3).AlignItems(ui.Center).Children(func() {
		action := "添加成员 "
		if selected {
			action = "移除成员 "
		}
		row := ui.ButtonBase(c).Label(fmt.Sprintf(tr(c, "%s%s"), tr(c, action), name)).Grow(1).Basis(0).FillHeight().Padding(5).
			Radius(8).Background(ui.Transparent).Checked(selected).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				glyph := "network"
				if isNode {
					glyph = "server"
				} else if isGroup {
					glyph = "layers"
				}
				ui.Box(c).Size(30, 30).Shrink(0).Center().Radius(9).Background(t.SurfaceHover).
					Children(func() {
						ui.Icon(c, icon.Must(glyph)).Size(15, 15).TextColor(accentForeground(t))
					})
				ui.Column(c).Grow(1).Basis(0).Gap(3).Children(func() {
					ui.Text(c, name).Bold().FontSize(11).SingleLine().Tooltip(name)
					meta := ""
					if isNode {
						meta = strings.ToUpper(profile.Protocol)
						if source := index.sources[profile.SourceID]; source != "" {
							meta += " · " + source
						}
						info := snap.NodeDetails[id]
						if info.IP == "" {
							meta += " · " + tr(c, "出口未查询")
						} else {
							location := strings.TrimSpace(info.Country + " " + info.Region)
							if location == "" {
								location = tr(c, "地区未知")
							}
							meta += " · " + tr(c, "出口 ") + info.IP + " · " + location
						}
					} else if isGroup {
						meta = policyType(c, group.Type) + " · " + fmt.Sprintf(tr(c, "%d 个成员"), len(group.Members))
					} else if id == "DIRECT" {
						meta = tr(c, "直连，不经过代理")
					} else {
						meta = tr(c, "拦截连接")
					}
					ui.Text(c, meta).FontSize(9).SingleLine().TextColor(t.TextMuted).Tooltip(meta)
				})
				if selected {
					ui.Text(c, fmt.Sprintf("#%d", selectedAt+1)).FontSize(10).Bold().
						TextColor(accentForeground(t))
				}
			})
		})
		if row.Hovered() {
			row.Background(t.SurfaceHover)
		}
		if row.Clicked() {
			f.toggleMember(id)
		}
		if selected {
			if policyIconButton(c, homeActionOptions{
				label: "上移成员 " + name, glyph: "arrow-up", hint: "提前此成员的策略顺序", disabled: selectedAt == 0,
			}) {
				f.moveMember(id, -1)
			}
			if policyIconButton(c, homeActionOptions{
				label: "下移成员 " + name, glyph: "arrow-down", hint: "后移此成员的策略顺序", disabled: selectedAt == len(f.members)-1,
			}) {
				f.moveMember(id, 1)
			}
			if f.kind == "select" && policyIconButton(c, homeActionOptions{
				label: "设为默认 " + name, glyph: "check", hint: "设为手动策略组当前使用的成员",
				active: f.selected == id,
			}) {
				f.selected = id
			}
		}
	})
}
