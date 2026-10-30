package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	xray "github.com/xtls/xray-core/core"

	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/logs"
	"github.com/sbgayhub/vray/internal/store"
)

type inspectedRealEngine struct {
	*xcore.Engine
	starts, stops int
}

func (e *inspectedRealEngine) Start(config []byte) error {
	e.starts++
	return e.Engine.Start(config)
}

func (e *inspectedRealEngine) Stop() error {
	e.stops++
	return e.Engine.Stop()
}

func TestExitIPThroughRealRelaysWithSameEntryAndDifferentExits(t *testing.T) {
	svc, _ := newTestService(t)
	engine := &inspectedRealEngine{Engine: xcore.New()}
	svc.engine = engine
	collector, _ := logs.NewCollector(logs.DefaultRing, "")
	defer collector.Close()
	defer func() {
		if t.Failed() {
			for _, entry := range collector.Snapshot() {
				t.Log(entry.Text)
			}
		}
	}()
	engine.SetLogger(collector)
	defer engine.Stop()
	var requests atomic.Int32
	var downloads [2]atomic.Int32
	for index, ip := range []string{"8.8.8.8", "8.8.4.4"} {
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == "/download" {
				downloads[index].Add(1)
				writeDownloadFixture(w, 64<<10)
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			requests.Add(1)
			var query map[string]string
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Error(err)
			}
			if _, containsEntry := query["q"]; containsEntry {
				t.Error("query sent the relay/entry address instead of asking for request origin")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"ip": ip, "country": "Example country"})
		}))
		defer api.Close()
		endpoint, _ := url.Parse(api.URL)
		port := freePort(t)
		config, _ := json.Marshal(map[string]any{
			"log": map[string]string{"loglevel": "none"},
			"inbounds": []any{map[string]any{
				"listen": "127.0.0.1", "port": port, "protocol": "shadowsocks",
				"settings": map[string]any{"method": "aes-128-gcm", "password": "real-test-password", "network": "tcp"},
			}},
			"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{
				"redirect": endpoint.Host, "finalRules": []any{map[string]any{"action": "allow", "ip": []string{"127.0.0.1/32"}}},
			}}},
		})
		relay, err := xray.StartInstance("json", config)
		if err != nil {
			t.Fatal(err)
		}
		defer relay.Close()
		id := "active"
		if index == 1 {
			id = "selected"
		}
		if err := svc.AddTestProfile(store.Profile{
			ID: id, Name: id, Protocol: store.ProtoShadowsocks, Address: "127.0.0.1", Port: port,
			RawOutbound: json.RawMessage(`{"method":"aes-128-gcm","network":"tcp"}`),
		}, "real-test-password"); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := svc.store.Load()
	d.Settings.MixedPort, d.Settings.ProbeURL, d.Settings.LogLevel = freePort(t), "http://probe-health.invalid/", "debug"
	if err := svc.store.SetSettings(d.Settings); err != nil {
		t.Fatal(err)
	}
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if err := svc.connectForTest("active"); err != nil {
		t.Fatal(err)
	}
	svc.ipEndpoint = "http://exit-probe.invalid/metadata"
	// 有意不注入 factory：完整走一遍 secrets → outbound 合并 → 真实 Xray → 选中的 relay → provider 响应 → snapshot。
	awaitOperation(t, svc.InspectNode("active"))
	awaitOperation(t, svc.InspectNode("selected"))
	svc.downloadEndpoint = "http://exit-probe.invalid/download"
	svc.downloadLimits = downloadLimits{MaxBytes: 64 << 10, Duration: time.Second, Timeout: 3 * time.Second}
	awaitOperation(t, svc.TestDownload("selected"))
	snap := svc.State().Snapshot()
	if requests.Load() != 2 || snap.NodeDetails["active"].IP != "8.8.8.8" || snap.NodeDetails["selected"].IP != "8.8.4.4" {
		t.Fatalf("wrong exits: requests=%d details=%+v", requests.Load(), snap.NodeDetails)
	}
	speed := snap.NodeSpeeds["selected"]
	d, _ = svc.store.Load()
	if speed.Error != "" || speed.Bytes != 64<<10 || speed.BytesPerSecond <= 0 || downloads[0].Load() != 0 || downloads[1].Load() != 1 || d.NodeSpeeds["selected"] != speed.DownloadSpeed || d.Usage["selected"].Download != 64<<10 {
		t.Fatalf("download did not use/persist the selected relay: speed=%+v downloads=%d/%d", speed, downloads[0].Load(), downloads[1].Load())
	}
	if snap.Phase != PhaseConnected || snap.Active != "active" || engine.starts != 1 || engine.stops != 0 || !engine.Running() {
		t.Fatalf("inspection disturbed the live connection: state=%s/%s engine starts/stops=%d/%d", snap.Phase, snap.Active, engine.starts, engine.stops)
	}
}
