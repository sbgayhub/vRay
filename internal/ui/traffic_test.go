package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

// 首页仅在已连接时显示速率：此前显示的零值没有意义，标题栏也从不显示速率。
func TestHomeShowsRatesWhenConnected(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().Update(func(s *app.Snapshot) {
		s.Phase = app.PhaseConnected
		s.Traffic = app.Traffic{UpBps: 2048, DownBps: 3 * 1024 * 1024, TotalDown: 9000}
	})

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("↑ 2.0 KB/s") {
		t.Fatal("the uplink rate is not shown")
	}
	if !tr.HasText("↓ 3.00 MB/s") {
		t.Fatal("the downlink rate is not shown")
	}
	if !tr.HasText("累计") {
		t.Fatal("the total is not shown")
	}
}

// 断开连接时，页面不显示任何速率。
func TestHomeHidesRatesWhenIdle(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().Update(func(s *app.Snapshot) { s.Phase = app.PhaseIdle })

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if tr.HasText("KB/s") || tr.HasText("MB/s") || tr.HasText("B/s") {
		t.Fatal("rates are shown while disconnected")
	}
}

// 标题栏绝不能显示速率或连接状态。
func TestTitleBarCarriesNoRates(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().Update(func(s *app.Snapshot) {
		s.Phase = app.PhaseConnected
		s.Traffic = app.Traffic{UpBps: 123456, DownBps: 654321}
	})

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	brand, ok := tr.Find("vRay")
	if !ok {
		t.Fatal("the brand is missing")
	}
	// 标题栏区域内的所有绘制内容都属于标题栏。
	for _, text := range tr.Texts() {
		rect, found := tr.Find(text)
		if !found || rect.Y > brand.Y+brand.H {
			continue
		}
		if text == "vRay" || text == "" {
			continue
		}
		switch text {
		case "─", "□", "❐", "✕", "最小化", "最大化", "还原", "关闭", "外观设置", "切换语言":
			continue
		}
		t.Fatalf("the title bar draws %q; it must hold only the brand and the buttons", text)
	}
}

// rateLabel 按用户熟悉的单位格式化。
func TestRateLabel(t *testing.T) {
	cases := map[int64]string{
		0:               "0 B/s",
		-5:              "0 B/s",
		512:             "512 B/s",
		1023:            "1023 B/s",
		1024:            "1.0 KB/s",
		2048:            "2.0 KB/s",
		1024 * 1024:     "1.00 MB/s",
		3 * 1024 * 1024: "3.00 MB/s",
	}
	for bps, want := range cases {
		if got := rateLabel(bps); got != want {
			t.Fatalf("rateLabel(%d) = %q, want %q", bps, got, want)
		}
	}
}
