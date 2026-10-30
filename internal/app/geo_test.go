package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/geo"
	"github.com/sbgayhub/vray/internal/paths"
)

// geoServer 提供两个数据文件，因此下载可以立即完成。
func geoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256sum") {
			http.NotFound(w, r) // 没有校验和：非必需
			return
		}
		w.Write([]byte("data"))
	}))
}

// waitGeoState 轮询状态直到达到 want。
func waitGeoState(t *testing.T, svc *Service, want string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sn := svc.State().Snapshot()
		if sn.GeoState == want {
			return sn
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("geoState = %q, want %q", svc.State().Snapshot().GeoState, want)
	return Snapshot{}
}

// AttachGeo 发布下载器的初始状态，EnsureGeo 在文件缺失时启动下载。
func TestEnsureGeoDownloads(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	dir := t.TempDir()

	d := geo.New(dir)
	d.Source = geoServer(t).URL
	d.Files = []string{"a.dat"}
	svc.AttachGeo(d)

	svc.EnsureGeo()
	if sn := waitGeoState(t, svc, "ready"); !sn.GeoReady {
		t.Fatal("GeoReady should be true once the download finished")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.dat")); err != nil {
		t.Fatalf("file missing: %v", err)
	}
}

// 文件已存在时，EnsureGeo 完全不会访问网络。
func TestEnsureGeoSkippedWhenPresent(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.dat"), []byte("x"), 0o644)

	d := geo.New(dir)
	d.Files = []string{"a.dat"}
	// 一旦使用就会明显失败的源。
	d.Source = "http://127.0.0.1:1/none"
	svc.AttachGeo(d)

	svc.EnsureGeo()
	sn := svc.State().Snapshot()
	if sn.GeoState != "ready" || !sn.GeoReady {
		t.Fatalf("snapshot = %+v, want ready", sn)
	}
}

// 下载失败会用文字报告，而不是裸露的错误字符串。
func TestEnsureGeoReportsFailure(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer srv.Close()

	d := geo.New(t.TempDir())
	d.Source = srv.URL
	d.Files = []string{"a.dat"}
	svc.AttachGeo(d)

	svc.EnsureGeo()
	sn := waitGeoState(t, svc, "failed")
	if sn.GeoError == "" {
		t.Fatal("a failure needs an explanation")
	}
	if sn.GeoReady {
		t.Fatal("GeoReady must stay false after a failure")
	}
}

// RetryGeo 在失败后启动新的下载。
func TestRetryGeo(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验和请求不能返回文件内容，否则下载会拿垃圾数据做校验并失败。
		if strings.HasSuffix(r.URL.Path, ".sha256sum") {
			http.NotFound(w, r)
			return
		}
		if fail.Load() {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	d := geo.New(t.TempDir())
	d.Source = srv.URL
	d.Files = []string{"a.dat"}
	svc.AttachGeo(d)

	svc.EnsureGeo()
	waitGeoState(t, svc, "failed")

	fail.Store(false)
	svc.RetryGeo()
	if sn := waitGeoState(t, svc, "ready"); !sn.GeoReady {
		t.Fatal("retry should end ready")
	}
	if sn := svc.State().Snapshot(); sn.GeoError != "" {
		t.Fatalf("GeoError = %q, want it cleared after a good retry", sn.GeoError)
	}
}

// 没有 geo 数据时，配置使用字面私有地址段；有数据时使用 geo 规则。Xray 会拒绝无法读取的 geo 规则，因此这个区分很重要。
func TestConnectUsesGeoRulesOnlyWhenReady(t *testing.T) {
	cases := []struct {
		name     string
		geo      bool
		wantRule string
	}{
		{"without geo data", false, "private"},
		{"with geo data", true, "geo-private"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			svc, engine := NewFake(dir)
			if c.geo {
				p := paths.Paths{Root: dir}
				os.WriteFile(p.GeoIP(), []byte("x"), 0o644)
				os.WriteFile(p.GeoSite(), []byte("x"), 0o644)
			}

			if err := svc.AddTestProfile(testProfile(), "password"); err != nil {
				t.Fatalf("add profile: %v", err)
			}
			if err := svc.connectForTest(""); err != nil {
				t.Fatalf("connect: %v", err)
			}

			cfg := string(engine.Config())
			if !strings.Contains(cfg, `"ruleTag": "`+c.wantRule+`"`) {
				t.Fatalf("config is missing rule %q:\n%s", c.wantRule, cfg)
			}
		})
	}
}

// 连接时会把 Xray 指向数据目录，使其 geo 文件能够解析。
func TestConnectSetsAssetLocation(t *testing.T) {
	dir := t.TempDir()
	svc, _ := NewFake(dir)
	if err := svc.AddTestProfile(testProfile(), "password"); err != nil {
		t.Fatalf("add profile: %v", err)
	}

	t.Setenv("xray.location.asset", "")
	if err := svc.connectForTest(""); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if got := os.Getenv("xray.location.asset"); got != dir {
		t.Fatalf("xray.location.asset = %q, want %q", got, dir)
	}
}

// 无需提示时 geoMessage 保持为空，因此成功重试后 UI 不会显示过时错误。
func TestGeoMessage(t *testing.T) {
	if got := geoMessage(""); got != "" {
		t.Fatalf("geoMessage(\"\") = %q, want empty", got)
	}
	got := geoMessage("boom\ndetail")
	if !strings.Contains(got, "boom") {
		t.Fatalf("geoMessage = %q, want it to carry the cause", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("geoMessage = %q, want a single line", got)
	}
}
