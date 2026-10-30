package ui

import (
	"fmt"
	"strings"

	"github.com/ELY-Open-Source/CrychicUI/icon"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

// searchBox 是节点搜索框：只有一层外框，而非框中框。
//
// 边框由这一行绘制。TextInputBase 不绘制内边距、背景、边框或圆角，
// 因此唯一可见的边就是这里设置的边框（mygo 自带的 TextInput 会在其中再加一层边框）。
func (r *Root) searchBox(c *ui.Context) {
	searchInput(c, "搜索节点", &r.search, "搜索名称、地址或协议")
}

// searchInput 是节点、连接和日志共用的搜索界面。
func searchInput(c *ui.Context, label string, value *string, placeholder string) {
	t := c.Theme()
	ui.Row(c).
		Label(label).
		FillWidth().
		Height(36).
		AlignItems(ui.Center).
		Gap(t.Space(2)).
		Padding(0, t.Space(3)).
		Radius(cardRadius).
		Background(t.Surface).
		Border(1, t.Border).
		Children(func() {
			ui.Icon(c, icon.Must("search")).Size(16, 16).TextColor(t.TextMuted)
			// FillHeight 使点击框内任意位置都能聚焦输入框，
			// 而不只限于 20px 高的文本行。
			ui.TextInputBase(c, value).
				Label(label).
				Grow(1).
				FillHeight().
				Placeholder(tr(c, placeholder)).
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key != ui.KeyEscape || k.Text() == "" {
						return false
					}
					k.SetText("")
					return true
				})
			if *value != "" {
				if homeIconButton(c, homeActionOptions{
					label: "清空" + label, glyph: "x", hint: "清空搜索",
				}) {
					*value = ""
				}
			}
		})
}

// countLabel 说明已显示的节点数与总数。
func countLabel(shown, total int) string {
	if shown == total {
		return fmt.Sprintf("共 %d 个节点", total)
	}
	return fmt.Sprintf("显示 %d / %d 个节点", shown, total)
}

// filterProfiles 返回匹配查询的节点，匹配范围为名称、
// 地址和协议。查询为空时保留全部节点。
func filterProfiles(list []app.ProfileView, query string) []app.ProfileView {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		out := make([]app.ProfileView, len(list))
		copy(out, list)
		return out
	}
	out := make([]app.ProfileView, 0, len(list))
	for _, p := range list {
		hay := strings.ToLower(p.Name + " " + p.Address + " " + p.Protocol)
		if strings.Contains(hay, q) {
			out = append(out, p)
		}
	}
	return out
}

// emptyState 是页面在无列表时显示的卡片。
func (r *Root) emptyState(c *ui.Context, title, hint string) {
	t := c.Theme()
	ui.Column(c).
		FillWidth().
		Padding(t.Space(8)).
		Radius(t.Radius).
		Background(t.Surface).
		AlignItems(ui.Center).
		Gap(t.Space(2)).
		Children(func() {
			ui.Text(c, tr(c, title)).Bold()
			ui.Text(c, tr(c, hint)).TextColor(t.TextMuted).FontSize(t.Rem(0.85))
		})
}
