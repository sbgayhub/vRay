package ui

import (
	"strconv"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/form"
	"github.com/ELY-Open-Source/CrychicUI/layout"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

// settingsForm 是设置页面的可编辑副本。
//
// 它采用副本而非实时绑定：页面显示已存储的值，用户编辑草稿，保存时写入。
// 直接编辑快照会让输入到一半的端口进入重连流程。
type settingsForm struct {
	lookupKey  string
	pending    bool
	mode       string
	port       string
	logLevel   string
	probeURL   string
	closeTray  bool
	openLogin  bool
	autoRecon  bool
	autoUpdate bool

	// loaded 表示草稿已填充过一次。此后草稿归用户所有：参见 sync。
	loaded bool

	// result 是最近一次保存的结果，显示在按钮旁边。
	result string
}

// sync 仅在首次时用快照填充草稿。
//
// 过去只要快照版本变化就会重新同步，这意味着任何状态变化——流量采样、
// 日志行、托盘刷新——都会重置草稿并丢弃用户刚点击的内容。页面上的每个按钮和
// 复选框因此看起来都失效了。从第一帧起，草稿就归用户所有；`Reload` 是重新
// 拉取已存储值的明确方式。
func (f *settingsForm) sync(snap app.Snapshot) {
	if f.loaded {
		return
	}
	f.fromSnapshot(snap)
}

// Reload 将已存储的值重新拉取到草稿中，并丢弃编辑。
func (f *settingsForm) Reload(snap app.Snapshot) {
	f.fromSnapshot(snap)
}

func (f *settingsForm) fromSnapshot(snap app.Snapshot) {
	f.mode = snap.Settings.Mode
	f.port = strconv.Itoa(snap.Settings.MixedPort)
	f.logLevel = snap.Settings.LogLevel
	f.probeURL = snap.Settings.ProbeURL
	f.closeTray = snap.Settings.CloseToTray
	f.openLogin = snap.Settings.OpenAtLogin
	f.autoRecon = snap.Settings.AutoReconnect
	f.autoUpdate = snap.Settings.AutoUpdateChecks
	f.loaded = true
}

// view 按服务所需的形式返回草稿。
func (f *settingsForm) view() app.SettingsView {
	port, _ := strconv.Atoi(strings.TrimSpace(f.port))
	return app.SettingsView{
		Mode:             f.mode,
		MixedPort:        port,
		LogLevel:         f.logLevel,
		ProbeURL:         strings.TrimSpace(f.probeURL),
		CloseToTray:      f.closeTray,
		OpenAtLogin:      f.openLogin,
		AutoReconnect:    f.autoRecon,
		AutoUpdateChecks: f.autoUpdate,
	}
}

// portProblem 在端口可用时返回 ""。
func (f *settingsForm) portProblem() string {
	raw := strings.TrimSpace(f.port)
	if raw == "" {
		return "端口不能为空"
	}
	port, err := strconv.Atoi(raw)
	if err != nil {
		return "端口必须是数字"
	}
	if port <= 0 || port > 65535 {
		return "端口需在 1–65535 之间"
	}
	return ""
}

// syncSettings 在固定页面标题绘制保存和取消操作之前，
// 更新草稿的待保存结果。
func (r *Root) syncSettings(snap app.Snapshot) {
	f := &r.settingsF
	f.sync(snap)
	if f.pending && !snap.SettingsSaving {
		f.pending = false
		f.result = "已保存"
		if snap.SettingsError != "" {
			f.result = snap.SettingsError
		}
	}
}

func (r *Root) settingsSave() {
	f := &r.settingsF
	if f.pending || f.portProblem() != "" {
		return
	}
	r.svc.SaveSettings(f.view())
	f.pending = true
	f.result = ""
}

func (r *Root) settingsCancel(snap app.Snapshot) {
	f := &r.settingsF
	f.Reload(snap)
	f.result = ""
	r.dataRoot.clear()
}

// settingsPage 绘制设置页面。
func (r *Root) settingsPage(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	f := &r.settingsF

	ui.Column(c).FillWidth().Gap(t.Space(4)).Children(func() {
		section(c, "连接", func() {
			form.FieldGroup(c, tr(c, "代理模式"), form.FieldGroupOptions{
				Help: tr(c, "系统代理只影响遵守系统代理的程序；TUN 接管全部流量，需要提权"),
			}, func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					r.choiceButton(c, &f.mode, "system-proxy", "系统代理")
					r.choiceButton(c, &f.mode, "tun", "TUN")
					r.choiceButton(c, &f.mode, "direct", "直连")
				})
			})

			form.FieldGroup(c, tr(c, "本地端口"), form.FieldGroupOptions{
				Help:  tr(c, "系统代理模式下 HTTP 与 SOCKS 共用的监听端口"),
				Error: f.portProblem(),
			}, func() {
				ui.TextInput(c, &f.port).Width(120).Placeholder("2080")
			})

			ui.Checkbox(c, &f.autoRecon, tr(c, "断线后自动重连"))
		})

		section(c, "诊断", func() {
			form.FieldGroup(c, tr(c, "日志级别"), form.FieldGroupOptions{
				Help: tr(c, "统一控制内核日志采集，保存后立即生效；日志页只能进一步筛选。调试会记录更多连接细节。"),
			}, func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					for _, level := range []string{"debug", "info", "warning", "error", "none"} {
						r.choiceButton(c, &f.logLevel, level, logLevelName(level))
					}
				})
			})

			form.FieldGroup(c, tr(c, "连通性探测地址"), form.FieldGroupOptions{
				Help: tr(c, "用于判断代理是否可用，一般是返回 204 的地址"),
			}, func() {
				ui.TextInput(c, &f.probeURL).FillWidth()
			})
		})

		section(c, "窗口", func() {
			ui.Checkbox(c, &f.closeTray, tr(c, "关闭窗口时留在托盘继续运行"))
			ui.Checkbox(c, &f.openLogin, tr(c, "开机时自动启动"))
		})
		r.dataDirectorySection(c, snap)
		r.updateSettingsSection(c, snap)
		r.aboutSettingsSection(c, snap)
		section(c, "IP 信息查询", func() {
			ui.Text(c, tr(c, "可选：配置 ipapi.is 密钥以获取完整的网络类型和信誉评分。")).FontSize(12).TextColor(t.TextMuted)
			ui.TextInput(c, &f.lookupKey).Password().FillWidth().Label("IP 查询密钥").Placeholder(tr(c, "不填写时使用匿名查询"))
			ui.Row(c).Gap(8).Children(func() {
				opts := []buttonOption{}
				if strings.TrimSpace(f.lookupKey) == "" {
					opts = append(opts, disabled())
				}
				if textButton(c, "保存查询密钥", opts...).Clicked() {
					r.svc.SaveIPLookupKey(f.lookupKey)
					f.lookupKey = ""
				}
				if snap.Settings.LookupKeyConfigured {
					ui.Text(c, tr(c, "已配置")).TextColor(t.Success).FontSize(12)
					if textButton(c, "移除密钥").Clicked() {
						r.svc.SaveIPLookupKey("")
					}
				}
			})
			if snap.Problem != nil && snap.Problem.Code == "IP_KEY_ERROR" {
				ui.Text(c, snap.Problem.Message).FontSize(12).TextColor(t.Danger)
			}
		})

		r.selfCheckCard(c, snap)
	})
}

// section 是带标题的字段区块。
func section(c *ui.Context, title string, body func()) {
	layout.Card(c, func() {
		ui.Text(c, tr(c, title)).Bold()
		body()
	}).FillWidth().Padding(20).Gap(12).Radius(cardRadius).Background(c.Theme().Surface)
}

// choiceButton 是小范围单选行中的一个按钮。
//
// 使用按钮行而非下拉框：必须让所选值直接显示为已选中，而收起状态的下拉框
// 无法做到这一点。对勾重复了边框提示，因此状态不只依赖颜色。
func (r *Root) choiceButton(c *ui.Context, current *string, value, label string) {
	chosen := *current == value

	// 对勾表示“已选中”，因此作为按钮内容的一部分绘制，而不是事后添加：
	// 控件必须自行显示状态，而且仅靠颜色无法让所有人分辨。
	text := label
	if chosen {
		text = "✓ " + label
	}
	opts := []buttonOption{}
	if chosen {
		opts = append(opts, tinted())
	}
	if textButton(c, text, opts...).Clicked() {
		*current = value
	}
}

// logLevelName 返回日志级别的中文名称。
func logLevelName(level string) string {
	switch level {
	case "debug":
		return "调试"
	case "info":
		return "信息"
	case "warning":
		return "警告"
	case "error":
		return "错误"
	case "none":
		return "关闭"
	default:
		return level
	}
}

// selfCheckCard 是环境报告：每项检查一行，每行带文字说明，
// 使状态不只依赖颜色（docs/02 §8.15）。
func (r *Root) selfCheckCard(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	if snap.Checks != nil {
		r.checks = snap.Checks
	}

	ui.Column(c).
		FillWidth().
		Gap(t.Space(2)).
		Padding(t.Space(5)).
		Radius(cardRadius).
		Background(t.Surface).
		Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr(c, "环境自检")).Bold()
				ui.Spacer(c)
				if snap.Checking {
					textButton(c, "检查中…", disabled())
				} else if textButton(c, "运行自检").Clicked() {
					r.svc.RunSelfCheck()
				}
			})

			if len(r.checks) == 0 {
				ui.Text(c, tr(c, "还没有运行过自检。")).
					TextColor(t.TextMuted).FontSize(t.Rem(0.85))
				return
			}

			for _, check := range r.checks {
				ui.Row(c).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					label, colour := checkLabel(check.State, t)
					ui.Text(c, tr(c, label)).TextColor(colour).FontSize(t.Rem(0.8)).Width(32)
					ui.Column(c).Gap(0).Children(func() {
						ui.Text(c, check.Name).FontSize(t.Rem(0.85))
						if check.Detail != "" {
							ui.Text(c, check.Detail).TextColor(t.TextMuted).
								FontSize(t.Rem(0.75)).MaxLines(2)
						}
					})
				})
			}
		})
}

// checkLabel 将检查状态转换为文字和颜色。文字承载含义；
// 颜色只起强化作用。
func checkLabel(state app.CheckState, t *ui.Theme) (string, ui.Color) {
	switch state {
	case app.CheckOK:
		return "正常", t.Success
	case app.CheckWarn:
		return "注意", t.Warning
	case app.CheckFail:
		return "失败", t.Danger
	default:
		return string(state), t.TextMuted
	}
}
