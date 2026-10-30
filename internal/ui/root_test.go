package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
)

// newRoot 在带有一个已存储节点的模拟服务上构建根视图。
func newRoot(t *testing.T) (*Root, *app.Service) {
	t.Helper()
	svc, _ := app.NewFake(t.TempDir())
	if err := svc.AddTestProfile(profileForTest(), "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return NewRoot(svc), svc
}

// 根视图能在每个页面构建，并显示该页标题。
func TestRootBuildsEveryPage(t *testing.T) {
	r, _ := newRoot(t)
	tr := ui.NewTester(r.View, 960, 640)
	for p := range pageRoutes {
		r.cur = Page(p)
		r.route = pageRoutes[p].ID
		tr.Frame()
		if !tr.HasText(pageRoutes[p].Label) {
			t.Fatalf("page %d (%s): title not shown", p, pageRoutes[p].Label)
		}
	}
}

// 首页会公告崩溃恢复。
//
// 用户的代理设置未经其操作被改回；该提示是唯一能把谜团变成解释的内容。
func TestHomeAnnouncesCrashRecovery(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().Update(func(sn *app.Snapshot) { sn.Recovered = 2 })

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("上次没有正常退出，已还原 2 处系统设置") {
		t.Fatal("the recovery notice is missing")
	}
}

// 上次运行正常退出时，不显示任何公告。
func TestHomeSilentWithoutRecovery(t *testing.T) {
	r, _ := newRoot(t)
	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if tr.HasText("上次没有正常退出") {
		t.Fatal("a clean start showed the recovery notice")
	}
}

// 标题栏显示品牌。
func TestTitleBarShowsBrand(t *testing.T) {
	r, _ := newRoot(t)
	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("vRay") {
		t.Fatal("brand missing from the title bar")
	}
}

// 首页提供连接按钮，按下后请求服务连接。
func TestHomeConnectButton(t *testing.T) {
	r, svc := newRoot(t)
	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()

	if !tr.HasText("连接") {
		t.Fatal("home page does not offer 连接")
	}
	// 通过按钮使用的同一路径驱动服务；断言阶段可使测试不依赖像素位置。
	svc.Toggle()
	if !waitPhaseIn(svc, app.PhaseConnected) {
		t.Fatalf("phase = %s after toggle, want connected", svc.State().Snapshot().Phase)
	}
	tr.Frame()
	if !tr.HasText("已连接") {
		t.Fatal("home page does not show 已连接 after connecting")
	}
	if !tr.HasText("断开连接") {
		t.Fatal("the button does not switch to 断开连接")
	}
}

// 失败会显示其代码，以便报告。
func TestHomeShowsProblem(t *testing.T) {
	r, svc := newRoot(t)
	svc.State().SetPhase(app.PhaseFailed, &app.Problem{Code: "CORE_START_FAILED", Message: "内核启动失败"})

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("CORE_START_FAILED") {
		t.Fatal("the problem code is not shown")
	}
}

// 地理数据行仅在文件缺失时出现，并说明下载进度，而不是保持沉默（26 MB 可能耗时数分钟）。
func TestHomeShowsGeoState(t *testing.T) {
	r, _ := newRoot(t)
	tr := ui.NewTester(r.View, 960, 640)

	// 缺失且空闲：说明规则库尚不可用。
	r.svc.State().Update(func(s *app.Snapshot) {
		s.GeoReady, s.GeoState = false, "idle"
	})
	tr.Frame()
	if !tr.HasText("规则库未就绪") {
		t.Fatal("missing geo data is not surfaced")
	}

	// 下载中：显示进度，避免慢速链路看起来像卡死。
	r.svc.State().Update(func(s *app.Snapshot) {
		s.GeoState = "downloading"
		s.GeoDone, s.GeoTotal = 5*1024*1024, 11*1024*1024
	})
	tr.Frame()
	if !tr.HasText("正在下载规则库") || !tr.HasText("5.0 / 11.0 MB") {
		t.Fatal("download progress is not shown")
	}

	// 失败：解释后果并提供重试。
	r.svc.State().Update(func(s *app.Snapshot) {
		s.GeoState = "failed"
		s.GeoError = "规则库下载失败，将先使用不带 geoip/geosite 的规则"
	})
	tr.Frame()
	if !tr.HasText("规则库下载失败") || !tr.HasText("重试") {
		t.Fatal("a failed download is not actionable")
	}

	// 就绪：该行消失。
	r.svc.State().Update(func(s *app.Snapshot) {
		s.GeoReady, s.GeoState, s.GeoError = true, "ready", ""
	})
	tr.Frame()
	if tr.HasText("规则库未就绪") || tr.HasText("正在下载规则库") {
		t.Fatal("geo line still shown although the data is present")
	}
}
