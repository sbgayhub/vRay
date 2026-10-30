package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
)

func TestNativePopupContentLeavesSystemMaterialVisible(t *testing.T) {
	for _, material := range []string{"none", "mica", "micaAlt", "acrylic", "liquidGlass"} {
		for _, available := range []bool{false, true} {
			for _, kind := range []string{"外观设置", "编辑订阅"} {
				t.Run(fmt.Sprintf("%s/%s/available=%t", kind, material, available), func(t *testing.T) {
					r, svc := newRoot(t)
					svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Material = material })
					r.lookOpen = kind == "外观设置"
					r.lookTheme, r.lookMaterial, r.lookAccent = "light", material, "system"
					r.sourceF = sourceForm{open: kind == "编辑订阅", draft: app.SourceEdit{Name: "测试订阅", URL: "https://example.com/sub"}, interval: "180"}
					host := &nativePopup{kind: kind, material: material, modal: kind == "编辑订阅"}
					tr := ui.NewTester(func(c *ui.Context) { r.nativePopupView(c, host) }, 440, 440)
					tr.SetVibrancy(available)
					tr.Advance(time.Second)
					_, _, _, alpha := tr.Image().At(4, 4).RGBA()
					if available && material != "none" {
						if alpha != 0 {
							t.Fatalf("native system material covered by content: alpha=%d", alpha)
						}
					} else if alpha != 65535 {
						t.Fatalf("fallback is not opaque: alpha=%d", alpha)
					}
					if kind == "外观设置" {
						if err := tr.Click("完成"); err != nil {
							t.Fatal(err)
						}
						if r.lookOpen {
							t.Fatal("appearance completion did not dismiss the popup")
						}
					} else {
						if !tr.HasText("订阅链接") {
							t.Fatal("native editor content is absent")
						}
						tr.Key(0, ui.KeyEscape)
						if r.sourceF.open || r.sourceF.draft.URL != "" {
							t.Fatal("Escape did not dismiss and clear the editor")
						}
					}
				})
			}
		}
	}
}

func TestNativeGeoEditorKeepsSaveWithinSmallWindow(t *testing.T) {
	r, _ := newRoot(t)
	r.ruleF.blankRule()
	r.ruleF.name, r.ruleF.domains = "广告拦截", "geosite:category-ads-all"
	host := &nativePopup{kind: "新建规则", modal: true}
	tr := ui.NewTester(func(c *ui.Context) { r.nativePopupView(c, host) }, 440, 472)
	tr.Advance(time.Second)
	bounds, found := tr.Find("保存")
	if !found || bounds.Y < 0 || bounds.Y+bounds.H > 472 {
		t.Fatalf("native editor save is clipped: %+v", bounds)
	}
}
