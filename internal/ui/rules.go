package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/form"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/store"
)

// ruleForm 是规则编辑器自身的状态，由根视图持有。
//
// 三个列表字段以每行一项的文本编辑：若改用标签控件，则需为配置中的
// 普通字符串数组维护逐项校验与删除状态。逐行文本如实表达了该结构，
// 并且能在输入时逐行校验。
type ruleForm struct {
	open bool

	id       string
	name     string
	domains  string
	ips      string
	ports    string
	process  string
	tcp      bool
	udp      bool
	outbound string
	enabled  bool

	// problems 每帧重新计算，字段修正后错误会立即消失。
	problems []configgen.RuleProblem
}

// loadRule 从已存储的规则填充表单。
func (f *ruleForm) loadRule(r store.Rule) {
	f.id = r.ID
	f.name = r.Name
	f.domains = strings.Join(r.Domains, "\n")
	f.ips = strings.Join(r.IPs, "\n")
	f.ports = r.Ports
	f.process = strings.Join(r.Process, "\n")
	f.tcp, f.udp = false, false
	for _, n := range r.Networks {
		switch n {
		case "tcp":
			f.tcp = true
		case "udp":
			f.udp = true
		}
	}
	f.outbound = r.Outbound
	if f.outbound == "" {
		f.outbound = "proxy"
	}
	f.enabled = r.Enabled
	f.open = true
}

// blankRule 为新规则初始化表单：启用、走代理、尚无匹配条件。
func (f *ruleForm) blankRule() {
	*f = ruleForm{outbound: "proxy", enabled: true, open: true}
}

// lines 将每行一项的文本拆分为值，并丢弃空行。
//
// 空行会被丢弃而非报告：粘贴列表末尾的空行是笔误，
// 并非用户有意写入的规则。
func lines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// rule 返回表单的当前值。
func (f *ruleForm) rule() store.Rule {
	var networks []string
	if f.tcp {
		networks = append(networks, "tcp")
	}
	if f.udp {
		networks = append(networks, "udp")
	}
	return store.Rule{
		ID:       f.id,
		Name:     f.name,
		Enabled:  f.enabled,
		Domains:  lines(f.domains),
		IPs:      lines(f.ips),
		Ports:    strings.TrimSpace(f.ports),
		Process:  lines(f.process),
		Networks: networks,
		Outbound: f.outbound,
	}
}

// problemFor 返回某个字段的错误消息，没有则返回 ""。
func (f *ruleForm) problemFor(field string) string {
	for _, p := range f.problems {
		if p.Field == field {
			return p.Value + "：" + p.Message
		}
	}
	return ""
}

// hasMatcher 报告表单能否生成 Xray 可用的规则。
func (f *ruleForm) hasMatcher() bool {
	r := f.rule()
	return len(r.Domains) > 0 || len(r.IPs) > 0 || r.Ports != "" || len(r.Process) > 0 || len(r.Networks) > 0
}

// nextRuleID 生成一个未被使用的 ID。
func nextRuleID(rules []app.RuleView) string {
	used := make(map[string]bool, len(rules))
	for _, r := range rules {
		used[r.ID] = true
	}
	for i := 1; ; i++ {
		id := "rule-" + strconv.Itoa(i)
		if !used[id] {
			return id
		}
	}
}

// rulesPage 是路由页面：按路由器读取顺序排列的规则列表，
// 以及编辑器。
func (r *Root) rulesPage(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()

	ui.Column(c).FillWidth().Gap(t.Space(3)).Children(func() {
		r.geoRules(c, snap)
		ui.Row(c).FillWidth().Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			if textButton(c, "新建规则", primary()).Clicked() {
				r.ruleF.blankRule()
			}
			ui.Spacer(c)
			ui.Text(c, fmt.Sprintf(tr(c, "共 %d 条，从上到下依次匹配"), len(snap.Rules))).
				TextColor(t.TextMuted).FontSize(t.Rem(0.85))
		})
		ui.Text(c, tr(c, "修改规则后重新连接生效；从上到下匹配，自定义规则优先于内置国内域名直连。")).FontSize(11).TextColor(t.TextMuted)

		if len(snap.Rules) == 0 {
			r.emptyState(c, "还没有规则",
				"尚无自定义规则，仍按上方内置路由分流。可以从 Geo 模板开始，或新建自己的规则。")
			return
		}

		ui.Column(c).
			FillWidth().
			Radius(cardRadius).
			Background(t.Surface).
			Children(func() {
				for i, rule := range snap.Rules {
					r.ruleRow(c, i, len(snap.Rules), rule, snap.GeoReady)
					if i < len(snap.Rules)-1 {
						ui.Divider(c)
					}
				}
			})
	})
}

// ruleRow 表示一条规则：顺序、状态、匹配条件及其操作。
func (r *Root) ruleRow(c *ui.Context, idx, total int, rule app.RuleView, geoReady bool) {
	t := c.Theme()

	ui.Row(c).FillWidth().Gap(t.Space(3)).Padding(t.Space(2), t.Space(3)).
		AlignItems(ui.Center).Children(func() {
		// 显示位置，因为路由器按首个匹配项处理：这个数字体现规则含义，
		// 并非装饰。
		ui.Text(c, strconv.Itoa(idx+1)).TextColor(t.TextMuted).
			FontSize(t.Rem(0.8)).Width(18)

		// Enabled 是复选框，因此一次点击即可切换一个状态。
		on := rule.Enabled
		if ui.Checkbox(c, &on, "").Changed() {
			r.svc.SetRuleEnabled(rule.ID, on)
		}

		ui.Column(c).Gap(1).Children(func() {
			ui.Text(c, rule.Name).Bold()
			ui.Text(c, ruleSummary(rule)).TextColor(t.TextMuted).FontSize(t.Rem(0.8)).
				MaxLines(2)
		})

		ui.Spacer(c)

		if rule.Problem != "" {
			ui.Text(c, rule.Problem).TextColor(t.Danger).FontSize(t.Rem(0.8)).MaxLines(2)
		}
		if rule.Enabled && !geoReady && configgen.RuleNeedsGeo(configgen.Rule{Domains: rule.Domains, IPs: rule.IPs}) {
			ui.Text(c, tr(c, "等待规则库")).FontSize(11).TextColor(t.Warning)
		}

		ui.Text(c, outboundLabel(rule.Outbound)).TextColor(accentForeground(t)).
			FontSize(t.Rem(0.8))

		upOptions, downOptions := []buttonOption{}, []buttonOption{}
		if idx == 0 {
			upOptions = append(upOptions, disabled())
		}
		if idx == total-1 {
			downOptions = append(downOptions, disabled())
		}
		if textButton(c, "↑", upOptions...).Clicked() {
			r.svc.MoveRule(rule.ID, true)
		}
		if textButton(c, "↓", downOptions...).Clicked() {
			r.svc.MoveRule(rule.ID, false)
		}
		if textButton(c, "编辑").Clicked() {
			r.editRule(rule)
		}
		if textButton(c, "删除").Clicked() {
			r.svc.DeleteRule(rule.ID)
		}
	})
}

// editRule 将存储中的规则载入编辑器。
func (r *Root) editRule(view app.RuleView) {
	// 视图携带编辑器所需的全部内容，也包括 Enabled 的存储形式；
	// 每次点击读取一次存储，使这里与节点编辑器保持一致。
	stored := store.Rule{
		ID: view.ID, Name: view.Name, Enabled: view.Enabled,
		Domains: view.Domains, IPs: view.IPs, Ports: view.Ports,
		Process: view.Process, Networks: view.Networks, Outbound: view.Outbound,
	}
	r.ruleF.loadRule(stored)
}

// ruleSummary 用一行描述规则匹配的内容。
func ruleSummary(rule app.RuleView) string {
	var parts []string
	if len(rule.Domains) > 0 {
		parts = append(parts, "域名 "+firstN(rule.Domains, 2))
	}
	if len(rule.IPs) > 0 {
		parts = append(parts, "IP "+firstN(rule.IPs, 2))
	}
	if rule.Ports != "" {
		parts = append(parts, "端口 "+rule.Ports)
	}
	if len(rule.Process) > 0 {
		parts = append(parts, "进程 "+firstN(rule.Process, 2))
	}
	if len(rule.Networks) > 0 {
		parts = append(parts, strings.ToUpper(strings.Join(rule.Networks, "/")))
	}
	if len(parts) == 0 {
		return "没有匹配条件，不会生效"
	}
	return strings.Join(parts, " · ")
}

// firstN 最多连接 n 个值，并说明还有多少项。
func firstN(values []string, n int) string {
	if len(values) <= n {
		return strings.Join(values, ", ")
	}
	return fmt.Sprintf("%s 等 %d 项", strings.Join(values[:n], ", "), len(values))
}

// outboundLabel 用文字说明规则的目标出口。
func outboundLabel(outbound string) string {
	switch outbound {
	case configgen.TagDirect:
		return "直连"
	case configgen.TagBlock:
		return "拦截"
	case configgen.TagProxy:
		return "代理"
	default:
		return outbound
	}
}

// ruleDialog 是规则编辑器。
//
// 当规则无法存储时禁用保存：任一匹配条件存在语法错误，或完全没有匹配条件
// （Xray 会直接拒绝这样的规则）。
//
// 字段区可滚动，按钮位于滚动区之外：对话框高度受窗口可用高度限制，
// 而在 520 DIP 高的窗口中，表单远高于此。此前保存按钮是列中的最后一项，
// 会超出面板并跑出窗口，导致对话框看起来被截断且没有保存按钮。
func (r *Root) ruleDialog(c *ui.Context) {
	f := &r.ruleF
	if !f.open {
		return
	}
	t := c.Theme()

	f.problems = configgen.ValidateRule(configgen.Rule{
		ID: f.id, Domains: lines(f.domains), IPs: lines(f.ips),
		Ports: strings.TrimSpace(f.ports), Process: lines(f.process),
	})
	canSave := len(f.problems) == 0 && f.hasMatcher()

	title := "新建规则"
	if f.id != "" {
		title = "编辑规则"
	}

	r.modal(c, title, &f.open, func() {
		ui.Column(c).FillWidth().Gap(t.Space(3)).Children(func() {
			if !r.svc.State().Snapshot().GeoReady && configgen.RuleNeedsGeo(configgen.Rule{Domains: lines(f.domains), IPs: lines(f.ips)}) {
				ui.Text(c, tr(c, "规则库未就绪：下载完成后重新连接，Geo 规则才会生效。")).FontSize(11).TextColor(t.Warning)
			}
			ui.Text(c, tr(c, "同一字段内任意一项匹配；不同字段需同时满足。")).FontSize(11).TextColor(t.TextMuted)
			// 可滚动的字段区域。MaxHeight 使其保持在窗口内；
			// 外围面板也不会超出该高度。
			_, height := c.Size()
			fieldHeight := min(float32(320), max(float32(130), height-220))
			if r.renderingPopup {
				height = r.popupLayoutHeight
				fieldHeight = max(130, height-220)
			}
			ui.Scroll(c).FillWidth().MaxHeight(fieldHeight).Children(func() {
				ui.Column(c).FillWidth().Gap(t.Space(3)).Children(func() {
					form.FieldGroup(c, tr(c, "名称"), form.FieldGroupOptions{Help: tr(c, "只用于列表显示")},
						func() {
							ui.TextInput(c, &f.name).FillWidth()
						})

					form.FieldGroup(c, tr(c, "域名"), form.FieldGroupOptions{
						Help:  tr(c, "每行一条：example.com / domain:example.com / keyword:ads / geosite:cn"),
						Error: f.problemFor(configgen.RuleFieldDomains),
					}, func() {
						ui.TextArea(c, &f.domains).FillWidth().Height(72)
					})

					form.FieldGroup(c, tr(c, "IP / 网段"), form.FieldGroupOptions{
						Help:  tr(c, "每行一条：10.0.0.0/8 / geoip:private"),
						Error: f.problemFor(configgen.RuleFieldIPs),
					}, func() {
						ui.TextArea(c, &f.ips).FillWidth().Height(72)
					})

					form.FieldGroup(c, tr(c, "端口"), form.FieldGroupOptions{
						Help:  tr(c, "如 80,443,8000-8100；留空表示任意端口"),
						Error: f.problemFor(configgen.RuleFieldPorts),
					}, func() {
						ui.TextInput(c, &f.ports).FillWidth()
					})

					form.FieldGroup(c, tr(c, "进程"), form.FieldGroupOptions{
						Help:  tr(c, "每行一条；仅在支持进程匹配的平台生效"),
						Error: f.problemFor(configgen.RuleFieldProcess),
					}, func() {
						ui.TextArea(c, &f.process).FillWidth().Height(72)
					})

					ui.Row(c).Gap(t.Space(4)).AlignItems(ui.Center).Children(func() {
						ui.Checkbox(c, &f.tcp, "TCP")
						ui.Checkbox(c, &f.udp, "UDP")
						ui.Checkbox(c, &f.enabled, tr(c, "启用"))
					})

					form.FieldGroup(c, tr(c, "出口"), form.FieldGroupOptions{}, func() {
						ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
							r.outboundChoice(c, &f.outbound, configgen.TagProxy, "代理")
							r.outboundChoice(c, &f.outbound, configgen.TagDirect, "直连")
							r.outboundChoice(c, &f.outbound, configgen.TagBlock, "拦截")
						})
					})
				})
			})

			if !f.hasMatcher() {
				ui.Text(c, tr(c, "至少需要一个匹配条件，否则这条规则不会生效")).
					TextColor(t.Warning).FontSize(t.Rem(0.85))
			}

			// 始终显示在屏幕上：位于滚动区外，固定在对话框底部。
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				if !canSave {
					textButton(c, "保存", disabled())
				} else if textButton(c, "保存", primary()).Clicked() {
					rule := f.rule()
					if rule.ID == "" {
						rule.ID = nextRuleID(r.svc.State().Snapshot().Rules)
					}
					r.svc.SaveRule(rule)
					f.open = false
				}
				if textButton(c, "取消").Clicked() {
					f.open = false
				}
			})
		})
	})
}

// outboundChoice 是规则目标出口的单选式按钮。
//
// 使用按钮而非下拉框：选项只有三个，且必须让所选值直接可见，
// 这正是收起状态的下拉框无法做到的。
func (r *Root) outboundChoice(c *ui.Context, current *string, value, label string) {
	chosen := *current == value

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
