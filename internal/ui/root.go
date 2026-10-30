// ui 包包含 vRay 的原生视图。
//
// 这里的每个函数都是一个视图：在主线程上每帧运行一次，
// 不得执行 IO 或阻塞。用户意图交由 app.Service 处理；参见 docs/02 §5.9。
package ui

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ELY-Open-Source/CrychicUI/icon"

	"github.com/ELY-Open-Source/CrychicUI/navigation"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/core/logs"
)

// Page 是侧边栏中的一项。
type Page int

// 主窗口页面，按侧边栏顺序排列。
const (
	PageHome Page = iota
	PageNodes
	PageConnections
	PageSources
	PageRules
	PageSettings
	PageLogs
	PageGroups
)

// pageRoutes 将页面映射到其侧边栏 ID、图标和标签。
var pageRoutes = []struct {
	ID    string
	Icon  string
	Label string
}{
	{"home", "house", "首页"},
	{"nodes", "server", "节点"},
	{"connections", "network", "实时连接"},
	{"sources", "download", "订阅"},
	{"rules", "git-branch", "规则"},
	{"settings", "settings", "设置"},
	{"logs", "scroll-text", "日志"},
	{"groups", "layers", "策略组"},
}

var navigationItems = func() []navigation.NavItem {
	items := make([]navigation.NavItem, len(pageRoutes))
	for i, route := range pageRoutes {
		items[i] = navigation.NavItem{Label: route.Label, Icon: route.Icon}
	}
	return items
}()

// Root 表示整个窗口：标题栏、侧边栏和当前页面。
type Root struct {
	languageOpen                        bool
	languageAnchor                      ui.Element
	languageSaving                      <-chan struct{}
	appliedLanguage                     string
	connections                         connectionBrowser
	win                                 *mygo.Window
	svc                                 *app.Service
	cur                                 Page
	route                               string
	appearance                          string
	material                            string
	materialVisible                     bool
	popupHost                           *nativePopup
	renderingPopup                      bool
	popupLayoutHeight                   float32
	lookOpen                            bool
	lookTheme, lookMaterial, lookAccent string
	lookAnchor                          ui.Element
	lookSaving                          <-chan struct{}
	selectedNode                        string
	nodeMore                            bool
	chartIndex                          int
	homeCopiedUntil                     time.Time
	homeInspectionID                    string
	tunProblemReported                  bool
	navCollapsed                        bool
	selectedGroup                       string
	groups                              policyBrowser
	policyEditor                        policyGroupEditor
	sourceF                             sourceForm
	sourceDeleting                      string
	sourceRemoveNodes                   bool

	// search、nodeSel、importText 和日志筛选条件均为视图本地 UI 状态：
	// 输入时不会访问服务（docs/02 §5.9）。
	search                                   string
	nodeSel                                  int
	importText                               string
	importOpen                               bool
	importName                               string
	importAttempted                          bool
	importSubmittedText, importSubmittedName string
	tunProblemOpen                           bool
	tunProblemSeen                           time.Time
	updateOpen                               bool
	aboutOpen                                bool
	legalOpen                                bool
	consentOpen                              bool
	consentSaving                            <-chan struct{}

	// 页面筛选只能收窄已配置的采集阈值。logRaw 用于在中文描述与
	// 保留的核心消息之间切换。
	logQuery string
	logLevel logs.Level
	logRaw   bool

	// deleting 是等待确认删除的节点 ID；没有时为 ""。一次只处理一个，
	// 以免与另一行的确认混淆。
	deleting string

	// editF 是节点编辑对话框的状态；参见 edit.go。
	editF editForm
	// ruleF 是规则编辑器的状态；参见 rules.go。
	ruleF ruleForm
	// settingsF 是设置草稿；checks 是最近一次自检结果。
	settingsF settingsForm
	checks    []app.Check

	// 数据目录选择器只在工作 goroutine 中等待原生对话框，结果通过
	// dataRootResult 回到主线程，避免视图读取跨线程可变字段。
	dataRootBusy   atomic.Bool
	dataRootResult chan dataRootOutcome
	pickDirectory  func(parent *mygo.Window, current, title, button string) (string, error)
	relaunch       func()
	dataRoot       dataRootForm
}

// NewRoot 返回基于服务的根视图，初始位于首页。
func NewRoot(svc *app.Service) *Root {
	r := &Root{
		svc: svc, cur: PageHome, route: pageRoutes[PageHome].ID, nodeSel: -1, chartIndex: -1,
		consentOpen:    svc.State().Snapshot().ConsentRequired,
		dataRootResult: make(chan dataRootOutcome, 1),
		pickDirectory:  openDataDirectory,
		relaunch:       mygo.App.Relaunch,
	}
	return r
}

// Attach 将视图要控制的窗口交给它，供标题栏按钮使用。
func (r *Root) Attach(w *mygo.Window) {
	r.win = w
	w.OnHide(func() { r.closeNativePopup() })
}

// View 绘制一帧。
func (r *Root) View(c *ui.Context) {
	if r.cur == PageHome {
		c.After(time.Second)
		r.svc.RefreshDashboard()
	}
	snap := r.svc.State().Snapshot()
	r.pollDataRootOutcome()
	if r.cur == PageSettings {
		r.syncSettings(snap)
	}
	r.syncPolicyEditor(snap)
	r.consentOpen = snap.ConsentRequired
	if snap.Phase == app.PhaseFailed && snap.Problem != nil && snap.Problem.Code == "TUN_UNAVAILABLE" {
		if !r.tunProblemReported || !r.tunProblemSeen.Equal(snap.Since) {
			r.tunProblemSeen, r.tunProblemOpen = snap.Since, true
			r.tunProblemReported = true
		}
	}
	r.applyLanguage(c, snap)
	if r.cur == PageConnections {
		r.refreshConnections(c)
	} else {
		r.connections.next = time.Time{}
		r.connections.rows = nil
		r.connections.filtered = nil
	}
	if !r.sourceF.open && snap.SourceDraft != nil {
		r.svc.CloseSourceEditor()
		r.sourceF.draft.URL = ""
	}
	if r.lookSaving != nil {
		select {
		case <-r.lookSaving:
			r.lookSaving = nil
		default:
		}
	}
	r.applyAppearance(c, snap)

	ui.Column(c).Fill().Children(func() {
		r.titlebar(c, snap)
		ui.Row(c).Grow(1).AlignItems(ui.Stretch).Children(func() {
			r.sidebar(c)
			ui.Column(c).Grow(1).Basis(0).Children(func() {
				if r.cur == PageNodes || r.cur == PageConnections || r.cur == PageGroups || r.cur == PageSettings {
					ui.Column(c).Fill().Padding(24).Gap(20).Children(func() {
						r.pageHeader(c, snap)
						if r.cur == PageConnections {
							r.connectionsPage(c, snap)
						} else if r.cur == PageGroups {
							r.groupsPage(c, snap)
						} else if r.cur == PageSettings {
							ui.Scroll(c).Grow(1).FillWidth().Children(func() {
								ui.Column(c).FillWidth().Children(func() {
									r.settingsPage(c, snap)
								})
							})
						} else {
							r.nodes(c, snap)
						}
					})
					return
				}
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					ui.Column(c).FillWidth().Padding(c.Theme().Space(6)).Children(func() {
						r.drawPage(c, snap)
					})
				})
			})
		})
	})

	// 最后绘制，使对话框覆盖页面而不是位于其下方。
	r.editDialog(c)
	r.ruleDialog(c)
	r.policyGroupEditorDialog(c, snap)
	r.sourceDialog(c, snap)
	r.importDialog(c, snap)
	r.appearancePopover(c, snap)
	r.languagePopover(c, snap)
	r.tunProblemDialog(c, snap)
	r.updateDialog(c, snap)
	r.aboutDialog(c, snap)
	r.legalDialog(c)
	r.consentDialog(c, snap)
	r.syncNativePopup(snap.Version)
}

// titlebar 包含品牌、更新入口、锚定的外观选择器和窗口控件。
// 连接状态与流量信息保留在内容区。
func (r *Root) titlebar(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	// 按钮延伸到窗口右上角，与系统标题栏按钮一致，因此这一行只在左侧留白，
	// 按钮高度等于整个标题栏。
	ui.Row(c).
		Height(captionHeight).
		FillWidth().
		Padding(0, 0).
		AlignItems(ui.Center).
		DragWindow().
		Children(func() {
			ui.Row(c).Padding(0, t.Space(3)).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon.Must("shield")).Size(16, 16)
				ui.Text(c, "vRay").Bold()
			})
			ui.Spacer(c)
			r.updateTitleButton(c, snap)
			r.languageButton(c)
			r.lookAnchor = ui.ButtonBase(c).Label("外观设置").Tooltip(tr(c, "主题、材质与强调色")).Size(40, captionHeight).
				Background(ui.Transparent).Center().Children(func() {
				ui.Icon(c, icon.Must("palette")).Size(17, 17).TextColor(t.TextMuted)
			})
			if r.lookAnchor.Hovered() {
				r.lookAnchor.Background(t.SurfaceHover)
			}
			if r.lookAnchor.Clicked() {
				r.lookOpen = !r.lookOpen
				if r.lookOpen {
					settings := r.svc.State().Snapshot().Settings
					r.lookTheme, r.lookMaterial, r.lookAccent = settings.Theme, settings.Material, settings.Accent
					if r.lookTheme == "" {
						r.lookTheme = "system"
					}
					if r.lookMaterial == "" {
						r.lookMaterial = "none"
					}
					if r.lookAccent == "" {
						r.lookAccent = "system"
					}
				}
			}
			r.windowButtons(c)
		})
}

// windowButtons 绘制最小化、最大化或还原，以及关闭按钮。
//
// 图标使用 ui.SVG 而非文本：Unicode 方框绘制字符有各自的字重和基线，
// 导致三者始终无法对齐，也无法匹配 Windows 绘制的细一像素线条。参见 captions.go。
func (r *Root) windowButtons(c *ui.Context) {
	if r.win == nil {
		return
	}

	if r.captionButton(c, "minimise", "最小化", false) {
		r.win.Minimize()
	}

	glyph, name := "maximise", "最大化"
	if r.win.IsMaximized() {
		glyph, name = "restore", "还原"
	}
	if r.captionButton(c, glyph, name, false) {
		r.win.ToggleMaximize()
	}

	// 关闭是唯一在悬停时变红的按钮，与 Windows 的行为一致：
	// 它执行破坏性操作，用户也期待这一警示。
	if r.captionButton(c, "close", "关闭", true) {
		if r.svc.State().Snapshot().ConsentRequired {
			mygo.App.Quit()
		} else {
			r.win.Close()
		}
	}
}

// captionButton 的尺寸取自 Windows 自带的标题栏按钮：宽 46，
// 高度等于整个标题栏，无圆角，因此悬停填充是直达窗口边缘的
// 普通矩形。
const (
	captionWidth  = 46
	captionHeight = 36
)

// captionButton 是一个标题栏按钮：包含图标、供辅助技术读取的名称、
// 悬停填充，关闭按钮还使用红色填充。
func (r *Root) captionButton(c *ui.Context, glyph, name string, destructive bool) bool {
	t := c.Theme()
	btn := ui.ButtonBase(c).
		Label(name).
		Width(captionWidth).
		Height(captionHeight).
		Background(ui.Color{})

	clicked := false
	btn.Children(func() {
		clicked = btn.Clicked()
		ink := t.Text
		if btn.Hovered() && destructive {
			ink = ui.RGB(255, 255, 255)
		}
		ui.Icon(c, captionGlyphs[glyph]).Size(15, 15).TextColor(ink)
	})

	switch {
	case btn.Hovered() && destructive:
		btn.Background(closeHover)
	case btn.Hovered():
		btn.Background(t.SurfaceHover)
	}
	return clicked
}

// closeHover 是 Windows 关闭按钮背后的红色。它固定不变而不是
// 取自主题，因为在浅色和深色模式下都必须传达“破坏性”，
// 而主题中没有对应颜色。
var closeHover = ui.RGB(196, 43, 28)

// sidebar 绘制导航。
func (r *Root) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Label("侧边导航栏").Width(r.sidebarWidth()).Shrink(0).FillHeight().
		Padding(12, 6).Children(func() {
		ui.Column(c).Fill().Radius(18).Background(t.Surface).Border(1, t.Border).
			Padding(12, 8).Gap(8).Children(func() {
			name, glyph := "折叠导航栏", "panel-left-close"
			if r.navCollapsed {
				name, glyph = "展开导航栏", "panel-left-open"
			}
			toggle := ui.ButtonBase(c).Label(name).Tooltip(tr(c, name)).Size(36, 32).Center().
				Radius(8).Background(ui.Transparent).Expanded(!r.navCollapsed).Children(func() {
				ui.Icon(c, icon.Must(glyph)).Size(17, 17).TextColor(t.TextMuted)
			})
			if toggle.Hovered() {
				toggle.Background(t.SurfaceHover)
			}
			if toggle.Clicked() {
				r.navCollapsed = !r.navCollapsed
				c.After(time.Millisecond)
			}
			active := int(r.cur)
			if r.navCollapsed {
				ui.Column(c).Label("主导航").FillWidth().Gap(3).FocusGroup(ui.Vertical).Children(func() {
					for i, route := range pageRoutes {
						c.Key(route.ID)
						ink, bg := t.TextMuted, ui.Transparent
						if i == active {
							ink, bg = t.Accent, t.Accent.Alpha(0.12)
						}
						button := ui.ButtonBase(c).Label(tr(c, route.Label)).Tooltip(tr(c, route.Label)).Checked(i == active).
							FillWidth().Height(36).Center().Radius(10).Background(bg).Children(func() {
							ui.Icon(c, icon.Must(route.Icon)).Size(16, 16).TextColor(ink)
						})
						if button.Hovered() && i != active {
							button.Background(t.SurfaceHover)
						}
						if button.Clicked() {
							r.cur, r.route = Page(i), route.ID
						}
					}
				})
			} else {
				items := make([]navigation.NavItem, len(navigationItems))
				for i, item := range navigationItems {
					items[i] = navigation.NavItem{Label: tr(c, item.Label), Icon: item.Icon}
				}
				if navigation.SideNav(c, "主导航", items, &active).FillWidth().Padding(0).Changed() {
					r.cur, r.route = Page(active), pageRoutes[active].ID
				}
			}
			ui.Spacer(c)
			footer := ui.Row(c).FillWidth().Padding(10).Gap(8).AlignItems(ui.Center)
			if r.navCollapsed {
				footer.Padding(10, 0).Center()
			}
			footer.Children(func() {
				ui.Icon(c, icon.Must("shield")).Size(16, 16).TextColor(t.Accent)
				if !r.navCollapsed {
					ui.Text(c, tr(c, "原生 · 轻盈 · 自由")).FontSize(11).TextColor(t.TextMuted)
				}
			})
		})
	})
}

func (r *Root) sidebarWidth() float32 {
	if r.navCollapsed {
		return 64
	}
	return 172
}

// drawPage 绘制当前页面的内容。
func (r *Root) drawPage(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(t.Space(4)).Children(func() {
		r.pageHeader(c, snap)
		switch r.cur {
		case PageHome:
			r.home(c, snap)
		case PageNodes:
			r.nodes(c, snap)
		case PageConnections:
			r.connectionsPage(c, snap)
		case PageSources:
			r.sources(c, snap)
		case PageRules:
			r.rulesPage(c, snap)
		case PageSettings:
			r.settingsPage(c, snap)
		case PageLogs:
			r.logsPage(c, snap)
		case PageGroups:
			r.groupsPage(c, snap)
		}
	})
}

func pageDescription(p Page) string {
	switch p {
	case PageGroups:
		return "查看订阅策略组，选择默认出站或组内节点。"
	case PageHome:
		return "连接概览，一切尽在掌握。"
	case PageNodes:
		return "选择下一站，找到适合你的连接。"
	case PageConnections:
		return "查看正在经过 vRay 的 TCP 连接与 UDP 会话。"
	case PageSources:
		return "你的节点来源，在这里统一管理。"
	case PageRules:
		return "让每一次连接，都走合适的路线。"
	case PageSettings:
		return "按你的习惯，打造舒适的使用体验。"
	case PageLogs:
		return "查看运行记录，了解连接情况。"
	default:
		return ""
	}
}

// geoLine 显示地理数据的当前状态。下载大小为 26 MB，慢速连接下可能
// 需要十分钟，若没有提示会让人以为程序卡住。
func (r *Root) geoLine(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	switch snap.GeoState {
	case "ready":
		if !snap.GeoReady {
			ui.Text(c, tr(c, "地理数据就绪")).TextColor(t.TextMuted).FontSize(t.Rem(0.8))
		}
	case "downloading":
		ui.Text(c, tr(c, "正在下载规则库  ")+bytesLabel(snap.GeoDone, snap.GeoTotal)).
			TextColor(t.TextMuted).FontSize(t.Rem(0.8))
	case "failed":
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, snap.GeoError).TextColor(t.Warning).FontSize(t.Rem(0.8))
			if textButton(c, "重试").Clicked() {
				r.svc.RetryGeo()
			}
		})
	default:
		if !snap.GeoReady {
			ui.Text(c, tr(c, "规则库未就绪：geoip/geosite 规则暂不可用")).
				TextColor(t.TextMuted).FontSize(t.Rem(0.8))
		}
	}
}

// bytesLabel 以 MB 显示“已下载 / 总量”；总量未知时（服务器未发送
// Content-Length）只显示已下载部分。
func bytesLabel(done, total int64) string {
	const mb = 1024 * 1024
	if total <= 0 {
		return fmt.Sprintf("%.1f MB", float64(done)/mb)
	}
	return fmt.Sprintf("%.1f / %.1f MB", float64(done)/mb, float64(total)/mb)
}

// activeName 返回所选节点的显示名称（如有）。
func activeName(snap app.Snapshot) string {
	for _, p := range snap.Profiles {
		if p.ID == snap.Active {
			return p.Name
		}
	}
	if len(snap.Profiles) == 1 {
		return snap.Profiles[0].Name
	}
	return ""
}

// phaseLabel 返回人类可读的阶段名称。
func phaseLabel(p app.Phase) string {
	switch p {
	case app.PhaseConnecting:
		return "连接中…"
	case app.PhaseConnected:
		return "已连接"
	case app.PhaseDisconnecting:
		return "断开中…"
	case app.PhaseFailed:
		return "连接失败"
	default:
		return "未连接"
	}
}

// buttonLabel 返回主按钮在指定阶段显示的文本。
func buttonLabel(p app.Phase) string {
	switch p {
	case app.PhaseConnecting:
		return "正在连接…"
	case app.PhaseConnected:
		return "断开连接"
	case app.PhaseDisconnecting:
		return "正在断开…"
	default:
		return "连接"
	}
}

// modeLabel 返回代理模式的名称。
func modeLabel(mode string) string {
	switch mode {
	case "tun":
		return "TUN（全局）"
	case "direct":
		return "直连"
	default:
		return "系统代理"
	}
}

// sinceLabel 渲染连接的持续时间。
func sinceLabel(since time.Time) string {
	if since.IsZero() {
		return ""
	}
	d := time.Since(since)
	if d < time.Minute {
		return fmt.Sprintf("%d 秒", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分 %d 秒", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d 时 %d 分", int(d.Hours()), int(d.Minutes())%60)
}

// rateLabel 按人们阅读速率的方式渲染每秒字节数：B/s、KB/s、
// MB/s；数值较大时带小数。
func rateLabel(bps int64) string {
	switch {
	case bps <= 0:
		return "0 B/s"
	case bps < 1024:
		return fmt.Sprintf("%d B/s", bps)
	case bps < 1024*1024:
		return fmt.Sprintf("%.1f KB/s", float64(bps)/1024)
	default:
		return fmt.Sprintf("%.2f MB/s", float64(bps)/(1024*1024))
	}
}
