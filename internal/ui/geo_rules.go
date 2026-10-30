package ui

import (
	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

func (r *Root) geoRules(c *ui.Context, snap app.Snapshot) {
	t := c.Theme()
	panel(c, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(c, "Geo 规则库")).Bold()
			ui.Spacer(c)
			switch {
			case snap.GeoReady:
				ui.Text(c, tr(c, "已就绪")).TextColor(t.Success).FontSize(12)
			case snap.GeoState == "downloading":
				ui.Text(c, "下载中  "+bytesLabel(snap.GeoDone, snap.GeoTotal)).TextColor(t.Accent).FontSize(12)
			default:
				if textButton(c, "下载规则库").Clicked() {
					r.svc.RetryGeo()
				}
			}
		})
		ui.Text(c, tr(c, "内置路由：局域网直连；规则库就绪后，国内域名直连。其余流量走当前节点。")).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, tr(c, "geosite 用于域名分组，geoip 用于 IP 地区。选择模板后可编辑匹配范围和出口。")).FontSize(11).TextColor(t.TextMuted)
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			for _, preset := range []store.Rule{
				{Name: "广告拦截", Domains: []string{"geosite:category-ads-all"}, Outbound: "block", Enabled: true},
				{Name: "国内 IP 直连", IPs: []string{"geoip:cn"}, Outbound: "direct", Enabled: true},
				{Name: "域名分组代理", Domains: []string{"geosite:geolocation-!cn"}, Outbound: "proxy", Enabled: true},
			} {
				if textButton(c, preset.Name).Clicked() {
					r.ruleF.loadRule(preset)
				}
			}
		})
		if !snap.GeoReady {
			ui.Text(c, tr(c, "规则库未就绪时，含 Geo 条件的整条规则暂不启用；普通规则仍可使用。")).FontSize(11).TextColor(t.Warning)
			if snap.GeoError != "" {
				ui.Text(c, snap.GeoError).FontSize(11).TextColor(t.Warning).MaxLines(2)
			}
		}
	}).Padding(18).Gap(10)
}
