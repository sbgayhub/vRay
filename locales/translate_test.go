package locales

import "testing"

func TestNativeUITranslationsAndFormattedCounts(t *testing.T) {
	for source, want := range map[string]string{
		"共 68 个节点":                      "68 nodes",
		"显示 2 / 68 个节点":                 "2 / 68 nodes",
		"共 14 个策略组":                     "14 policy groups",
		"剩余 25 天":                       "25 days remaining",
		"30% 已用 · 89.00 GB / 300.00 GB": "30% used · 89.00 GB / 300.00 GB",
		"断线后自动重连":                       "Reconnect automatically after a drop",
	} {
		if got := Translate("en", source); got != want {
			t.Errorf("%q = %q, want %q", source, got, want)
		}
		if got := Translate("zh-Hans", source); got != source {
			t.Errorf("source language changed: %q", got)
		}
	}
}
