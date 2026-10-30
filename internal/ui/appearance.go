package ui

import (
	"fmt"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

func (r *Root) appearancePopover(c *ui.Context, snap app.Snapshot) {
	if !r.lookOpen {
		return
	}
	t := c.Theme()
	_, height := c.Size()
	maxHeight := height - 115
	if r.renderingPopup {
		maxHeight = r.popupLayoutHeight - 32
	}
	r.popover(c, r.lookAnchor, &r.lookOpen, func() {
		ui.Scroll(c).Width(256).MaxHeight(maxHeight).Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Text(c, tr(c, "个性化外观")).Bold().FontSize(16)
					ui.Spacer(c)
					if textButton(c, "完成").Clicked() {
						r.lookOpen = false
					}
				})
				ui.Text(c, tr(c, "主题")).TextColor(t.TextMuted).FontSize(12)
				ui.Row(c).Gap(6).Children(func() {
					r.lookChoice(c, &r.lookTheme, "system", "系统")
					r.lookChoice(c, &r.lookTheme, "light", "浅色")
					r.lookChoice(c, &r.lookTheme, "dark", "深色")
				})
				ui.Divider(c)
				ui.Text(c, tr(c, "窗口材质")).TextColor(t.TextMuted).FontSize(12)
				ui.Row(c).Gap(6).Children(func() {
					r.lookChoice(c, &r.lookMaterial, "none", "纯色")
					r.lookChoice(c, &r.lookMaterial, "mica", "Mica")
					r.lookChoice(c, &r.lookMaterial, "micaAlt", "Mica Alt")
				})
				ui.Row(c).Gap(6).Children(func() {
					r.lookChoice(c, &r.lookMaterial, "acrylic", "Acrylic")
					r.lookChoice(c, &r.lookMaterial, "liquidGlass", "Liquid Glass")
				})
				ui.Divider(c)
				ui.Text(c, tr(c, "强调色")).TextColor(t.TextMuted).FontSize(12)
				r.lookChoice(c, &r.lookAccent, "system", "系统强调色")
				colors := []struct{ name, value string }{
					{name: "蓝色", value: "#2563eb"}, {name: "紫色", value: "#7166c5"},
					{name: "绿色", value: "#187348"}, {name: "玫瑰色", value: "#b92b64"},
					{name: "青色", value: "#087f8c"}, {name: "蓝绿色", value: "#0f766e"},
					{name: "橙色", value: "#c75b12"}, {name: "琥珀色", value: "#a66b08"},
					{name: "红色", value: "#c93647"}, {name: "靛蓝色", value: "#5146be"},
					{name: "石板色", value: "#526477"}, {name: "洋红色", value: "#a02e9a"},
				}
				for start := 0; start < len(colors); start += 6 {
					ui.Row(c).Gap(12).Children(func() {
						for _, color := range colors[start:min(start+6, len(colors))] {
							b := ui.ButtonBase(c).Label(color.name).Tooltip(tr(c, color.name)).Size(28, 28).
								Radius(14).Background(ui.Hex(color.value)).Center().Disabled(r.lookSaving != nil)
							b.Children(func() {
								if r.lookAccent == color.value {
									ui.Icon(c, icon.Must("check")).Size(16, 16).TextColor(ui.RGB(255, 255, 255))
								}
							})
							if b.Clicked() {
								r.lookAccent = color.value
								r.saveLook()
							}
						}
					})
				}
				ui.Text(c, tr(c, "即时生效，自动保存")).FontSize(11).TextColor(t.TextMuted)
				if snap.Problem != nil && (snap.Problem.Code == "APPEARANCE_INVALID" || snap.Problem.Message == "无法保存外观") {
					ui.Text(c, snap.Problem.Message).TextColor(t.Danger).FontSize(11)
				}
			})
		})
	})
}

func (r *Root) lookChoice(c *ui.Context, selected *string, value, label string) {
	opts := []buttonOption{}
	if *selected == value {
		opts = append(opts, tinted())
		label = "✓ " + label
	}
	if r.lookSaving != nil {
		opts = append(opts, disabled())
	}
	if textButton(c, label, opts...).Clicked() {
		*selected = value
		r.saveLook()
	}
}

func (r *Root) saveLook() {
	r.lookSaving = r.svc.SaveAppearance(r.lookTheme, r.lookMaterial, r.lookAccent)
}

func (r *Root) pageHeader(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	w, _ := c.Size()
	browser := r.cur == PageNodes || r.cur == PageConnections
	compact := w < 1100
	header := ui.Column(c).FillWidth().Gap(5).Label("页面标题栏")
	if browser {
		header.Height(browserHeaderHeight(w))
	} else if r.cur == PageGroups || r.cur == PageSettings {
		header.Height(60)
	}
	header.Children(func() {
		ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(c, pageRoutes[r.cur].Label)).Bold().FontSize(t.Rem(1.65)).SingleLine().Shrink(0)
			switch r.cur {
			case PageConnections:
				ui.Text(c, fmt.Sprintf(tr(c, "共 %d 条连接"), len(r.connections.rows))).FontSize(12).TextColor(t.TextMuted)
			case PageNodes:
				ui.Text(c, tr(c, countLabel(len(filterProfiles(snap.Profiles, r.search)), len(snap.Profiles)))).FontSize(12).TextColor(t.TextMuted)
			case PageSources:
				ui.Text(c, fmt.Sprintf(tr(c, "共 %d 个订阅"), len(snap.Sources))).FontSize(12).TextColor(t.TextMuted)
			case PageGroups:
				ui.Text(c, fmt.Sprintf(tr(c, "共 %d 个策略组"), len(snap.Groups))).FontSize(12).TextColor(t.TextMuted)
			}
			ui.Spacer(c)
			if browser && !compact {
				ui.Box(c).Width(240).Shrink(0).Children(func() { r.browserSearch(c) })
			}
			switch r.cur {
			case PageGroups:
				if textButton(c, "新建策略组", primary()).Disabled(snap.GroupBusy).
					Tooltip(tr(c, "选择节点或已有分组，创建自动选择、故障转移或负载均衡策略")).Clicked() {
					r.openNewPolicyGroup(snap)
				}
				if snap.Settings.ActiveGroup != "" && textButton(c, "使用单节点").Disabled(snap.GroupBusy).
					Tooltip(tr(c, "修改当前出站会重新连接；未连接时只保存选择")).Clicked() {
					r.svc.SetPolicy("", "", true)
				}
			case PageSettings:
				f := &r.settingsF
				if snap.SettingsSaving || f.pending {
					textButton(c, "保存中…", disabled())
				} else if f.portProblem() != "" {
					textButton(c, "保存", disabled()).Tooltip(f.portProblem())
				} else if textButton(c, "保存", primary()).
					Tooltip(tr(c, "改模式或端口后需要重新连接才生效")).Clicked() {
					r.settingsSave()
				}
				if textButton(c, "取消").Disabled(snap.SettingsSaving || f.pending).Clicked() {
					r.settingsCancel(snap)
				}
			case PageConnections:
				if !compact {
					r.connectionFilters(c)
				}
			case PageNodes:
				if r.svc.Testing() {
					if textButton(c, "取消延迟测速").Clicked() {
						r.svc.CancelTest()
					}
				} else if textButton(c, "延迟测速").Clicked() {
					r.svc.TestAll()
				}
				running := false
				for _, test := range snap.NodeSpeeds {
					running = running || test.Running
				}
				if running {
					if textButton(c, "停止下载测速").Clicked() {
						r.svc.CancelDownload()
					}
				} else {
					id := r.selectedNode
					if id == "" && len(snap.Profiles) > 0 {
						id = snap.Profiles[0].ID
					}
					opts := []buttonOption{}
					if id == "" {
						opts = append(opts, disabled())
					}
					if textButton(c, "下载测速", opts...).Tooltip(tr(c, "测试当前选中节点的单连接下载速度，最多使用 32 MiB 下载流量")).Clicked() {
						r.svc.TestDownload(id)
					}
				}
			case PageSources:
				if textButton(c, "添加订阅", primary()).Clicked() {
					r.importOpen = true
					r.importAttempted = false
				}
			}
		})
		if browser && compact {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Grow(1).Basis(0).Children(func() { r.browserSearch(c) })
				if r.cur == PageConnections {
					r.connectionFilters(c)
				}
			})
		}
		description := tr(c, pageDescription(r.cur))
		descriptionColor := t.TextMuted
		if r.cur == PageSettings && r.settingsF.result != "" {
			description = tr(c, r.settingsF.result)
			descriptionColor = t.Success
			if snap.SettingsError != "" {
				descriptionColor = t.Danger
			}
		}
		ui.Text(c, description).TextColor(descriptionColor).FontSize(12).SingleLine().
			Tooltip(description)
	})
}

// 第二行标题栏让控件在最小窗口宽度下仍可使用。
func browserHeaderHeight(width float32) float32 {
	if width < 1100 {
		return 104
	}
	return 60
}

func (r *Root) browserSearch(c *ui.Context) {
	if r.cur == PageNodes {
		r.searchBox(c)
		return
	}
	searchInput(c, "搜索连接", &r.connections.query, "搜索目标、来源、节点或协议")
}

func (r *Root) connectionFilters(c *ui.Context) {
	for _, mode := range []struct{ label, value string }{{"全部", ""}, {"TCP", "tcp"}, {"UDP", "udp"}} {
		opts := []buttonOption{}
		if r.connections.network == mode.value {
			opts = append(opts, tinted())
		}
		if textButton(c, mode.label, opts...).Height(32).Clicked() {
			r.connections.network = mode.value
		}
	}
}
