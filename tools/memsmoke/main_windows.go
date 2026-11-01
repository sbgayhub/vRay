// Command memsmoke 对真实 UI 和仅限回环的代理进行性能分析。其数据库是临时的；
// 它从不更改系统代理或用户数据。
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/ELY-Open-Source/CrychicUI/sound"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/sbgayhub/vray/internal/app"
	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/logs"
	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/store"
	vui "github.com/sbgayhub/vray/internal/ui"
	xray "github.com/xtls/xray-core/core"
)

var memoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

func main() {
	sound.SetEnabled(false)
	duration := flag.Duration("duration", 3*time.Minute, "continuous traffic duration")
	out := flag.String("out", "build/memory-before", "profile directory")
	geo := flag.String("geo", "", "optional directory containing public geoip.dat/geosite.dat")
	protocol := flag.String("protocol", "shadowsocks", "local relay protocol: shadowsocks or vless")
	stream := flag.Bool("stream", false, "paced HTTPS/HTTP2 video segments over persistent connections")
	connections := flag.Bool("connections", false, "show the real connections page and capture it during traffic")
	policies := flag.Bool("policies", false, "exercise the policy page, real health checks and inactive-group edits")
	capture := flag.Bool("capture", false, "capture the visible home page during traffic")
	releaseMemory := flag.Bool("release-memory", false, "click the home GC button during real proxy traffic")
	flag.Parse()
	if err := run(
		*duration, *out, *geo, *protocol, *stream, *connections, *capture, *releaseMemory, *policies,
	); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func freePort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func run(duration time.Duration, out, geo, protocol string, stream, connections, capture, releaseMemory, policies bool) error {
	if policies && (stream || connections || releaseMemory) {
		return fmt.Errorf("policy smoke uses plain HTTP fixtures and its own page")
	}
	if releaseMemory && connections {
		return fmt.Errorf("release-memory requires the home page")
	}
	if protocol != "vless" && protocol != "shadowsocks" {
		return fmt.Errorf("unsupported fixture protocol %q", protocol)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "vray-memory-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	p := paths.Paths{Root: temp}
	if err := p.Prepare(); err != nil {
		return err
	}
	if geo != "" {
		for _, name := range []string{"geoip.dat", "geosite.dat"} {
			data, err := os.ReadFile(filepath.Join(geo, name))
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(temp, name), data, 0o600); err != nil {
				return err
			}
		}
	}
	payload := make([]byte, 128<<10)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		chunks := 1
		if stream {
			chunks = 32
		}
		for range chunks {
			if _, err := w.Write(payload); err != nil {
				return
			}
			if stream {
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(200 * time.Millisecond):
				}
			}
		}
	})
	api := httptest.NewUnstartedServer(handler)
	if stream {
		api.EnableHTTP2 = true
		api.StartTLS()
	} else {
		api.Start()
	}
	defer api.Close()
	u, _ := url.Parse(api.URL)
	relayPort, mixedPort, apiPort := freePort(), freePort(), freePort()
	secret := "memory-fixture"
	relaySettings := map[string]any{"method": "aes-128-gcm", "password": secret, "network": "tcp"}
	rawOutbound := json.RawMessage(`{"method":"aes-128-gcm"}`)
	if protocol == "vless" {
		secret = "11111111-2222-3333-4444-555555555555"
		relaySettings = map[string]any{"decryption": "none", "clients": []any{map[string]any{"id": secret}}}
		rawOutbound = nil
	}
	cfg, _ := json.Marshal(map[string]any{
		"log":      map[string]any{"loglevel": "none"},
		"inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": relayPort, "protocol": protocol, "settings": relaySettings}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{
			"redirect": u.Host, "finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
		}}},
	})
	relay, err := xray.StartInstance("json", cfg)
	if err != nil {
		return err
	}
	defer relay.Close()
	storer := store.NewFile(p.DB())
	set := store.DefaultSettings()
	set.MixedPort, set.ActiveProfile, set.Material = mixedPort, "local", "mica"
	d := store.Data{Settings: set}
	for i := range 100 {
		id := fmt.Sprintf("node-%d", i)
		if i == 0 {
			id = "local"
		}
		d.Profiles = append(d.Profiles, store.Profile{ID: id, Name: fmt.Sprintf("本地测试节点 %d", i), Protocol: store.Protocol(protocol), Address: "127.0.0.1", Port: relayPort, SecretRef: "fixture", RawOutbound: rawOutbound})
	}
	if policies {
		d.Groups = []store.PolicyGroup{
			{ID: "main", Name: "默认代理", Type: "select", Selected: "auto", Members: []string{"auto", "local", "node-1"}},
			{ID: "auto", Name: "自动选择", Type: "url-test", Members: []string{"local", "node-1"}, URL: api.URL, Interval: 30},
			{ID: "spare", Name: "备用线路", Type: "select", Selected: "node-2", Members: []string{"node-2", "node-3"}},
		}
		d.Settings.ActiveGroup = "main"
		if capture {
			// 受控的持久化测试数据，不代表公开出口 IP。
			d.NodeDetails = map[string]store.NodeIPInfo{
				"local":  {IP: "203.0.113.10", Country: "测试地区", Provider: "本地验证", CheckedAt: time.Now()},
				"node-1": {IP: "203.0.113.11", Country: "测试地区", Provider: "本地验证", CheckedAt: time.Now()},
			}
		}
	}
	if err := storer.Save(d); err != nil {
		return err
	}
	keeper := app.NewMemSecrets()
	_ = keeper.Set("fixture", []byte(secret))
	state := app.NewState(app.Snapshot{Phase: app.PhaseIdle, Mode: set.Mode})
	engine := xcore.New()
	defer engine.Stop()
	collector, err := logs.NewCollector(logs.DefaultRing, "")
	if err != nil {
		return err
	}
	defer collector.Close()
	engine.SetLogger(collector)
	svc := app.New(state, storer, keeper, engine, p)
	svc.AttachLogs(collector)
	svc.SetAPIPort(apiPort)
	if err := svc.Load(); err != nil {
		return err
	}
	var requests, failures, frames atomic.Int64
	var bytes atomic.Int64
	started := time.Now()
	sample := func(stage string, gc bool) {
		if gc {
			runtime.GC()
		}
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		// PROCESS_MEMORY_COUNTERS_EX，在 Windows/amd64 上自然对齐。
		var pm struct {
			Size, Faults                                                                                     uint32
			PeakWS, WS, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile, Private, PrivateWS uintptr
			SharedCommit                                                                                     uint64
		}
		pm.Size = uint32(unsafe.Sizeof(pm))
		memoryInfo.Call(^uintptr(0), uintptr(unsafe.Pointer(&pm)), uintptr(pm.Size))
		fmt.Printf("%s t=%.0fs heap=%.2fMiB heapSys=%.2fMiB released=%.2fMiB goUsed=%.2fMiB private=%.2fMiB ws=%.2fMiB pws=%.2fMiB goroutines=%d gc=%d frames=%d requests=%d failures=%d bytes=%d alloc=%.2fMiB\n", stage, time.Since(started).Seconds(), float64(m.HeapAlloc)/(1<<20), float64(m.HeapSys)/(1<<20), float64(m.HeapReleased)/(1<<20), float64(m.Sys-m.HeapReleased)/(1<<20), float64(pm.Private)/(1<<20), float64(pm.WS)/(1<<20), float64(pm.PrivateWS)/(1<<20), runtime.NumGoroutine(), m.NumGC, frames.Load(), requests.Load(), failures.Load(), bytes.Load(), float64(m.TotalAlloc)/(1<<20))
		if gc {
			for _, kind := range []string{"heap", "goroutine", "allocs"} {
				f, e := os.Create(filepath.Join(out, stage+"-"+kind+".pprof"))
				if e != nil {
					panic(e)
				}
				_ = pprof.Lookup(kind).WriteTo(f, 0)
				_ = f.Close()
			}
		}
	}
	result := make(chan error, 1)
	mygo.App.WhenReady(func() {
		root := vui.NewRoot(svc)
		win := mygo.NewWindow(mygo.WindowOptions{Title: "vRay memory test", Width: 1120, Height: 760, Hidden: true, TitleBarStyle: mygo.TitleBarHidden, HideWindowButtons: true, Vibrancy: mygo.VibrancyMica, Content: ui.View(func(c *ui.Context) { frames.Add(1); root.View(c) })})
		root.Attach(win)
		state.SetOnChange(win.Invalidate)
		win.Show()
		// STARTF_USESHOWWINDOW/SW_HIDE 可能消耗第一次原生 Show。
		win.Show()
		go func() {
			defer mygo.App.Quit()
			time.Sleep(3 * time.Second)
			if frames.Load() == 0 {
				result <- fmt.Errorf("native window did not render")
				return
			}
			if connections || (policies && !capture) {
				var clickErr error
				mygo.RunOnMain(func() {
					tr := ui.NewTester(root.View, 1120, 760)
					tr.Frame()
					label := "实时连接"
					if policies {
						label = "策略组"
					}
					clickErr = tr.Click(label)
					win.Invalidate()
				})
				if clickErr != nil {
					result <- clickErr
					return
				}
			}
			sample("idle", true)
			svc.Connect("local")
			deadline := time.Now().Add(10 * time.Second)
			for state.Snapshot().Phase != app.PhaseConnected && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if state.Snapshot().Phase != app.PhaseConnected {
				result <- fmt.Errorf("connect: %+v", state.Snapshot().Problem)
				return
			}
			sample("connected", true)
			proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", mixedPort))
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
			workersCount := 4
			destination := "http://memory-fixture.invalid/data"
			expectedBytes := int64(len(payload))
			if stream {
				roots := x509.NewCertPool()
				roots.AddCert(api.Certificate())
				// 仅信任生成的本地测试证书。
				transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: api.Certificate().DNSNames[0]}
				transport.DisableKeepAlives = false
				transport.ForceAttemptHTTP2 = true
				workersCount = 2
				destination = "https://memory-fixture.invalid/data"
				expectedBytes *= 32
			}
			client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
			ctx, cancel := context.WithCancel(context.Background())
			var workers sync.WaitGroup
			for range workersCount {
				workers.Go(func() {
					for ctx.Err() == nil {
						req, _ := http.NewRequestWithContext(ctx, http.MethodGet, destination, nil)
						resp, e := client.Do(req)
						if e == nil {
							n, re := io.Copy(io.Discard, resp.Body)
							resp.Body.Close()
							bytes.Add(n)
							if ctx.Err() == nil && (re != nil || resp.StatusCode != http.StatusOK || n != expectedBytes) {
								failures.Add(1)
							}
							requests.Add(1)
						} else if ctx.Err() == nil {
							failures.Add(1)
						}
						select {
						case <-ctx.Done():
							return
						case <-time.After(50 * time.Millisecond):
						}
					}
				})
			}
			end := time.Now().Add(duration)
			captured := false
			released := false
			for time.Now().Before(end) {
				time.Sleep(min(10*time.Second, time.Until(end)))
				sample("traffic", false)
				if (connections || capture || policies) && !captured {
					png, err := win.CapturePage()
					if err != nil {
						cancel()
						workers.Wait()
						result <- err
						return
					}
					name := "home-live.png"
					if connections {
						name = "connections-live.png"
					}
					if policies {
						name = "policies-live.png"
						if capture {
							name = "policy-home-live.png"
						}
					}
					if err := os.WriteFile(filepath.Join(out, name), png, 0o600); err != nil {
						cancel()
						workers.Wait()
						result <- err
						return
					}
					captured = true
					if policies {
						if capture {
							if err := verifyPolicyHome(root, svc); err != nil {
								cancel()
								workers.Wait()
								result <- err
								return
							}
						}
						if err := verifyInactivePolicyEdit(root, svc, engine, win); err != nil {
							cancel()
							workers.Wait()
							result <- err
							return
						}
					}
					fmt.Printf("live connections=%d\n", len(svc.Connections()))
					if capture {
						d := state.Snapshot().Dashboard
						fmt.Printf("dashboard: connections=%d memory=%d heap=%d goroutines=%d\n", d.Connections, d.Memory.PrivateWorkingSetBytes, d.Memory.HeapAllocBytes, d.Memory.Goroutines)
					}
				}
				if releaseMemory && !released {
					var before runtime.MemStats
					runtime.ReadMemStats(&before)
					var clickErr error
					mygo.RunOnMain(func() {
						tr := ui.NewTester(root.View, 1120, 760)
						tr.Frame()
						clickErr = tr.Click("释放内存")
						win.Invalidate()
					})
					if clickErr == nil {
						deadline := time.Now().Add(5 * time.Second)
						for state.Snapshot().Dashboard.MemoryReleasedAt.IsZero() && time.Now().Before(deadline) {
							time.Sleep(10 * time.Millisecond)
						}
						d := state.Snapshot().Dashboard
						if d.MemoryReleasedAt.IsZero() || d.Memory.GCCount <= before.NumGC {
							clickErr = fmt.Errorf("home memory button did not collect and refresh")
						} else {
							fmt.Printf("manual GC: before=%d after=%d heapBefore=%d heapAfter=%d releasedPages=%d phase=%s\n",
								before.NumGC, d.Memory.GCCount, before.HeapAlloc, d.Memory.HeapAllocBytes,
								d.Memory.HeapReleasedBytes, state.Snapshot().Phase)
						}
					}
					if clickErr != nil {
						cancel()
						workers.Wait()
						result <- clickErr
						return
					}
					released = true
				}
			}
			cancel()
			workers.Wait()
			transport.CloseIdleConnections()
			sample("loaded", true)
			svc.Disconnect()
			deadline = time.Now().Add(10 * time.Second)
			for state.Snapshot().Phase != app.PhaseIdle && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if state.Snapshot().Phase != app.PhaseIdle {
				result <- fmt.Errorf("disconnect timed out")
				return
			}
			time.Sleep(3 * time.Second)
			sample("stopped", true)
			if connections && len(svc.Connections()) != 0 {
				result <- fmt.Errorf("connections retained after stop")
				return
			}
			if failures.Load() != 0 {
				result <- fmt.Errorf("%d failed requests", failures.Load())
			} else {
				result <- nil
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		return err
	}
	return <-result
}

func verifyPolicyHome(root *vui.Root, svc *app.Service) error {
	snap := svc.State().Snapshot()
	exits := snap.Dashboard.PolicyExits
	if exits.GroupID != "main" || len(exits.Nodes) == 0 || !snap.Dashboard.PolicySince.Equal(snap.Since) {
		return fmt.Errorf("home did not collect actual policy exits")
	}
	var checkErr error
	mygo.RunOnMain(func() {
		tester := ui.NewTester(root.View, 1120, 760)
		tester.Frame()
		matched := false
		for _, node := range exits.Nodes {
			if tester.HasText(node.Name) && tester.HasText(snap.NodeDetails[node.ID].IP) {
				matched = true
			}
		}
		if !matched {
			checkErr = fmt.Errorf("home did not display the actual node and its persisted IP fixture")
		}
	})
	if checkErr == nil {
		fmt.Printf("policy home: actual node metadata + persisted IP fixture displayed; source=%s\n", exits.Source)
	}
	return checkErr
}

func verifyInactivePolicyEdit(root *vui.Root, svc *app.Service, engine *xcore.Engine, win *mygo.Window) error {
	health := engine.PolicyHealth()
	if len(health) != 2 || !health["local"].Alive || !health["node-1"].Alive {
		return fmt.Errorf("real policy HTTP health observations missing")
	}
	before := svc.State().Snapshot().Since
	var clickErr error
	mygo.RunOnMain(func() {
		tester := ui.NewTester(root.View, 1120, 760)
		tester.Frame()
		if clickErr = tester.Click("策略组"); clickErr != nil {
			return
		}
		tester.Frame()
		if clickErr = tester.Click("查看策略组 备用线路"); clickErr != nil {
			return
		}
		tester.Frame()
		clickErr = tester.Click("选择成员 本地测试节点 3")
		win.Invalidate()
	})
	if clickErr != nil {
		return clickErr
	}
	deadline := time.Now().Add(3 * time.Second)
	for svc.State().Snapshot().GroupBusy && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	snap := svc.State().Snapshot()
	if snap.GroupBusy || snap.Phase != app.PhaseConnected || !snap.Since.Equal(before) {
		return fmt.Errorf("editing an inactive policy interrupted the running core")
	}
	for _, g := range snap.Groups {
		if g.ID == "spare" && g.Selected != "node-3" {
			return fmt.Errorf("inactive member choice was not saved")
		}
	}
	fmt.Println("policies: two real HTTP observations; inactive edit saved without reconnecting")
	return nil
}
