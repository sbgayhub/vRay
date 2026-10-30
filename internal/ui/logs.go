package ui

import (
	"fmt"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/data"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/core/logs"
)

// logPageMax 是页面渲染的最新日志行数上限。
//
// 采集器会保留更多日志，但页面通过滚动阅读，向列表传入超过
// 此数量的行会浪费帧时间，而且没人会看。
const logPageMax = 500

// logsPage 是日志视图：级别筛选、文本筛选和只读列表。
//
// 设置决定采集级别。页面只能收窄该阈值，包括设置更改前采集的旧条目。
func (r *Root) logsPage(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	lines := r.svc.Logs()
	configured := logs.ParseLevel(snap.Settings.LogLevel)
	filtered := filterLogs(lines, r.logQuery, max(configured, r.logLevel))

	ui.Column(c).FillWidth().Gap(t.Space(3)).Children(func() {
		ui.Row(c).FillWidth().Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
			r.levelPicker(c, configured)
			ui.Spacer(c)
			ui.Text(c, tr(c, logCountLabel(len(filtered), len(lines)))).
				TextColor(t.TextMuted).FontSize(t.Rem(0.85))
		})
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(c, "采集级别：")+tr(c, configured.Label())+tr(c, "（由设置控制，保存后立即生效）")).FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			ui.Checkbox(c, &r.logRaw, tr(c, "原始日志"))
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1).Basis(0).Children(func() {
				searchInput(c, "搜索日志", &r.logQuery, "过滤日志")
			})
			if textButton(c, "清空").Clicked() {
				r.svc.ClearLogs()
			}
			// 诊断包：包含错误报告所需且已经脱敏的信息。按钮会说明文件
			// 去向，因为默默写入文件的按钮无法让人信任。
			if snap.DiagnosticSaving {
				textButton(c, "正在导出…", disabled())
			} else if textButton(c, "导出诊断包").Clicked() {
				r.svc.ExportDiagnosticsAsync()
			}
		})

		if snap.DiagnosticResult != "" {
			ui.Text(c, snap.DiagnosticResult).TextColor(t.TextMuted).FontSize(t.Rem(0.8)).MaxLines(2)
		}

		// 写入端跟不上时，缓冲区会丢弃日志行。应明确提示，
		// 而不是让视图看起来完整却实际并非如此。
		if dropped := r.svc.DroppedLogs(); dropped > 0 {
			ui.Text(c, fmt.Sprintf(tr(c, "有 %d 行因写入繁忙未进入界面，文件里通常仍完整"), dropped)).
				TextColor(t.Warning).FontSize(t.Rem(0.8))
		}

		if len(filtered) == 0 {
			hint := "还没有日志。连接后内核的日志会出现在这里。"
			if len(lines) > 0 {
				hint = "没有匹配的日志行。"
			}
			r.emptyState(c, "暂无日志", hint)
			return
		}

		display := filtered
		if !r.logRaw && snap.Settings.Language != "en" {
			display = append([]logs.Entry(nil), filtered...)
			for i := range display {
				display[i].Text = logs.Describe(display[i].Text)
			}
		}
		data.LogList(c, "logs", toLogLines(display), data.LogListOptions{
			Levels: [4]string{tr(c, "调试"), tr(c, "信息"), tr(c, "警告"), tr(c, "错误")},
		})
	})
}

// levelPicker 切换当前显示的最低日志级别。
func (r *Root) levelPicker(c *ui.Context, configured logs.Level) {
	levels := []logs.Level{logs.LevelDebug, logs.LevelInfo, logs.LevelWarning, logs.LevelError}
	for _, l := range levels {
		label := l.Label()
		selected := max(r.logLevel, configured) == l
		opts := []buttonOption{}
		if selected {
			opts = append(opts, tinted())
		}
		if l < configured {
			opts = append(opts, disabled())
		}
		if textButton(c, label, opts...).Clicked() {
			r.logLevel = l
		}
	}
}

// filterLogs 保留不低于 minLevel 且匹配查询的日志行。
//
// 查询为空时保留全部日志，这是常见情况：通常只用级别选择器就足够了。
func filterLogs(lines []logs.Entry, query string, minLevel logs.Level) []logs.Entry {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]logs.Entry, 0, len(lines))
	for _, e := range lines {
		if e.Level < minLevel {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Text), q) && !strings.Contains(strings.ToLower(logs.Describe(e.Text)), q) {
			continue
		}
		out = append(out, e)
	}
	// 只绘制最新日志行，其余仍保留在缓冲区中。
	if len(out) > logPageMax {
		out = out[len(out)-logPageMax:]
	}
	return out
}

// toLogLines 将日志条目转换为列表控件所需的形式。
//
// 两套 Level 类型一一对应（debug、info、warning、error），但控件遇到越界值会
// panic，因此这里对值进行钳制，而不是直接信任。
func toLogLines(entries []logs.Entry) []data.LogLine {
	out := make([]data.LogLine, len(entries))
	for i, e := range entries {
		level := int(e.Level)
		if level < 0 {
			level = 0
		}
		if level > int(data.LevelError) {
			level = int(data.LevelError)
		}
		out[i] = data.LogLine{
			Level: data.Level(level),
			Time:  e.At.Format("15:04:05"),
			Text:  e.Text,
		}
	}
	return out
}

// logCountLabel 说明显示了多少行以及共持有多少行。
func logCountLabel(shown, total int) string {
	if shown == total {
		return fmt.Sprintf("%d 行", total)
	}
	return fmt.Sprintf("显示 %d / %d 行", shown, total)
}
