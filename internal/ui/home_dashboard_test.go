package ui

import (
	"context"
	"fmt"
	"image/png"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/diag"
	"github.com/sbgayhub/vray/internal/store"
)

func homeDashboardFixture(t *testing.T) (*Root, *app.Service) {
	t.Helper()
	r, svc := newRoot(t)
	now := time.Now()
	svc.State().Update(func(sn *app.Snapshot) {
		sn.Phase, sn.Active, sn.Since = app.PhaseConnected, "p1", now.Add(-6*time.Minute-46*time.Second)
		sn.Settings.Theme = "dark"
		sn.GeoReady, sn.GeoState = true, "ready"
		sn.Profiles[0].Name = "东京 · 高速 01"
		sn.Profiles[0].SourceID = "sub1"
		for i := 1; i < 68; i++ {
			sn.Profiles = append(sn.Profiles, app.ProfileView{ID: fmt.Sprint(i), Name: fmt.Sprintf("节点 %d", i), Protocol: "vless"})
		}
		sn.NodeDetails = map[string]app.IPDetails{"p1": {IP: "203.0.113.28", Country: "日本", Provider: "Example Transit", ASN: "AS64500", CheckedAt: now}}
		sn.NodeSpeeds = map[string]app.DownloadTest{"p1": {DownloadSpeed: store.DownloadSpeed{BytesPerSecond: 12 << 20, CheckedAt: now}}}
		sn.Sources = []app.SourceView{{
			ID: "sub1", Name: "我的订阅", Total: 300 << 30, Download: 89 << 30,
			ExpireAt: now.Add(25 * 24 * time.Hour), LastFetchedAt: now,
			Enabled: true, NodeCount: 68,
		}}
		sn.Traffic = app.Traffic{UpBps: 80 * 1024, DownBps: 8 << 20, TotalUp: 33 << 20, TotalDown: 112 << 20}
		sn.Usage = map[string]store.Transfer{"p1": {Upload: 320 << 20, Download: 12 << 30}}
		for i := range 120 {
			up := int64(8000 + 82000*math.Pow((math.Sin(float64(i)/12)+1)/2, 4))
			down := int64(400000 + 8500000*math.Pow((math.Sin(float64(i)/17-1)+1)/2, 3))
			sn.History = append(sn.History, app.TrafficPoint{At: now.Add(time.Duration(i-120) * 500 * time.Millisecond), UpBps: up, DownBps: down})
		}
		sn.Rules = []app.RuleView{{ID: "r1", Enabled: true}, {ID: "r2", Enabled: true}, {ID: "r3", Enabled: true}}
		sn.Dashboard = app.Dashboard{At: now.Add(time.Hour), Connections: 28, Memory: diag.MemoryStats{WorkingSetBytes: 131 << 20, HeapAllocBytes: 36 << 20, Goroutines: 48}}
	})
	p := svc.Prober()
	tick := 0
	p.Now = func() time.Time { tick++; return now.Add(time.Duration(tick) * 46 * time.Millisecond) }
	p.Dial = func(context.Context, string) (net.Conn, error) { a, b := net.Pipe(); _ = b.Close(); return a, nil }
	p.Start([]store.Profile{{ID: "p1", Address: "fixture.invalid", Port: 443}})
	p.Wait()
	return r, svc
}

func TestHomeDashboardLayoutAndPreview(t *testing.T) {
	for _, theme := range []string{"light", "dark"} {
		for _, size := range [][2]int{{1120, 760}, {760, 520}} {
			t.Run(fmt.Sprintf("%s-%d", theme, size[0]), func(t *testing.T) {
				r, svc := homeDashboardFixture(t)
				svc.State().Update(func(sn *app.Snapshot) { sn.Settings.Theme = theme })
				tr := ui.NewTester(r.View, size[0], size[1])
				tr.Frame()
				tr.Advance(time.Second)
				for _, label := range []string{
					"首页连接状态", "首页上传速度", "首页下载速度", "首页出站方式", "首页代理方式",
					"首页订阅概览", "首页节点质量", "首页运行内存", "首页运行概览", "首页累计流量",
				} {
					b, ok := tr.Find(label)
					if size[0] == 760 && b.H == 0 {
						continue
					}
					if !ok || b.W <= 0 || b.X < 170 || b.X+b.W > float32(size[0])+1 {
						t.Fatalf("card %s out of bounds: %+v", label, b)
					}
					if size[0] == 1120 && b.Y+b.H > float32(size[1])+1 {
						t.Fatalf("desktop card %s below window: %+v", label, b)
					}
				}
				if !tr.HasText("203.0.113.28") || (size[0] == 1120 && (!tr.HasText("我的订阅") || !tr.HasText("46 ms"))) {
					t.Fatal("real dashboard fields missing")
				}
				if dir := os.Getenv("VRAY_HOME_PREVIEW"); dir != "" {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%d.png", theme, size[0])))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tr.Image()); err != nil {
						t.Fatal(err)
					}
				}
				if size[0] == 760 {
					tr.Scroll(600, 400, 0, 430)
					tr.Frame()
					tr.Advance(time.Second)
					for _, label := range []string{"首页订阅概览", "首页节点质量", "首页运行内存"} {
						b, ok := tr.Find(label)
						if !ok || b.W < 180 || b.X+b.W > 760 || b.H < 100 {
							t.Fatalf("scrolled card %s clipped: %+v", label, b)
						}
					}
				}
			})
		}
	}
}

func TestHomeManualMemoryReleaseButton(t *testing.T) {
	r, svc := newRoot(t)
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	if tr.HasText("工作空间") {
		t.Fatal("sidebar section label still visible")
	}
	if err := tr.Click("释放内存"); err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(3 * time.Second)
	for svc.State().Snapshot().Dashboard.MemoryReleasedAt.IsZero() && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	tr.Frame()
	if !tr.HasText("已释放") {
		t.Fatal("manual GC did not complete from the button")
	}
	button, _ := tr.Find("释放内存")
	card, _ := tr.Find("首页运行内存")
	if button.Y < card.Y || button.Y+button.H > card.Y+card.H {
		t.Fatalf("memory action outside card: button=%+v card=%+v", button, card)
	}
}

func TestHomeSubscriptionRefreshUpdatesQuota(t *testing.T) {
	var requests atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		download := "1024"
		if n := requests.Add(1); n > 1 {
			if n == 2 {
				close(started)
			}
			select {
			case <-release:
			case <-req.Context().Done():
				return
			}
			download = "2048"
		}
		w.Header().Set("Subscription-Userinfo", "upload=0; download="+download+"; total=4096")
		_, _ = w.Write([]byte("vless://11111111-2222-3333-4444-555555555555@jp.example.com:443#SubscriptionNode"))
	}))
	defer server.Close()
	defer unblock.Do(func() { close(release) })
	r, svc := newRoot(t)
	svc.Import(app.ImportInput{Text: server.URL, Name: "首页订阅"})
	sn := waitImport(t, svc)
	for _, node := range sn.Profiles {
		if node.SourceID == sn.Sources[0].ID {
			<-svc.SetActive(node.ID)
		}
	}
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	if err := tr.Click("更新当前订阅"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("subscription button did not start an update")
	}
	tr.Frame()
	if !tr.HasText("正在更新订阅…") {
		t.Fatal("pending update has no feedback")
	}
	// 重复点击不会为同一订阅源创建并发抓取。
	_ = tr.Click("更新当前订阅")
	unblock.Do(func() { close(release) })
	end := time.Now().Add(3 * time.Second)
	for svc.State().Snapshot().Sources[0].Updating && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	tr.Frame()
	if requests.Load() != 2 || svc.State().Snapshot().Sources[0].Download != 2048 ||
		!tr.HasText("50% 已用 · 2.00 KB / 4.00 KB") {
		t.Fatal("quota did not update or refresh was duplicated")
	}
}

func TestHomeDashboardNavigation(t *testing.T) {
	for _, tc := range []struct {
		button string
		page   Page
	}{{"查看实时连接", PageConnections}, {"打开当前订阅", PageSources}, {"打开节点质量", PageNodes}, {"代理设置", PageSettings}, {"查看启用规则", PageRules}} {
		r, _ := homeDashboardFixture(t)
		tr := ui.NewTester(r.View, 1120, 760)
		tr.Frame()
		if err := tr.Click(tc.button); err != nil {
			t.Fatal(err)
		}
		tr.Frame()
		if r.cur != tc.page {
			t.Fatalf("%s opened %v", tc.button, r.cur)
		}
	}
}

func TestHomeMissingMeasurementsAreExplicit(t *testing.T) {
	r, _ := newRoot(t)
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	for _, label := range []string{"未测速", "出口信息尚未查询", "本地节点"} {
		if !tr.HasText(label) {
			t.Fatalf("missing truthful empty state %q", label)
		}
	}
}

func TestHomeTUNDoesNotClaimMixedPortIsListening(t *testing.T) {
	r, svc := homeDashboardFixture(t)
	svc.State().Update(func(sn *app.Snapshot) { sn.Mode, sn.Settings.Mode = "tun", "tun" })
	tr := ui.NewTester(r.View, 1120, 760)
	tr.Frame()
	if !tr.HasText("虚拟网卡运行中") || tr.HasText("HTTP / SOCKS 端口 2080") || tr.HasText("复制地址") {
		t.Fatal("TUN mode misreported a mixed listener")
	}
}
