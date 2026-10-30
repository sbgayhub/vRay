package app

import (
	"testing"
	"time"

	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/apiclient"
	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/store"
)

// API 客户端属于正在运行的核心。重连时必须创建新客户端，断开连接时必须释放 gRPC 传输层和重连 goroutine。
func TestDisconnectClosesOwnedStatsClient(t *testing.T) {
	p := paths.Paths{Root: t.TempDir()}
	if err := p.Prepare(); err != nil {
		t.Fatal(err)
	}
	db := store.NewFile(p.DB())
	settings := store.DefaultSettings()
	settings.MixedPort = freePort(t)
	if err := db.SetSettings(settings); err != nil {
		t.Fatal(err)
	}
	engine := xcore.New()
	defer engine.Stop()
	svc := New(NewState(Snapshot{Phase: PhaseIdle}), db, NewMemSecrets(), engine, p)
	profile := testProfile()
	profile.Address = "127.0.0.1"
	if err := svc.AddTestProfile(profile, "password"); err != nil {
		t.Fatal(err)
	}
	for round := range 3 {
		svc.SetAPIPort(freePort(t))
		if err := svc.connectForTest(""); err != nil {
			t.Fatal(err)
		}
		client, ok := svc.stats.(*apiclient.Client)
		if !ok || !client.Connected() {
			t.Fatal("no connected stats client")
		}
		svc.Disconnect()
		waitFor(svc.state, PhaseIdle, 5*time.Second)
		if svc.state.Snapshot().Phase != PhaseIdle {
			t.Fatal("disconnect timed out")
		}
		if client.Connected() {
			t.Fatalf("round %d: stats client retained after disconnect", round)
		}
	}
	if err := svc.connectForTest(""); err != nil {
		t.Fatal(err)
	}
	client := svc.stats.(*apiclient.Client)
	if err := svc.FlushTraffic(); err != nil {
		t.Fatal(err)
	}
	if client.Connected() {
		t.Fatal("quit retained stats client")
	}
}

func TestBackgroundSamplingKeepsTotals(t *testing.T) {
	stats := newFakeStats()
	observed := &collector{}
	sampler := NewSampler(stats, []string{downName}, observed.report)
	sampler.Start(time.Hour)
	defer sampler.Stop()
	deadline := time.Now().Add(time.Second)
	for stats.readCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stats.add(downName, 1024)
	sampler.SetInterval(5 * time.Millisecond)
	for time.Now().Before(deadline) {
		traffic, _ := observed.snapshot()
		if traffic.TotalDown == 1024 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	traffic, _ := observed.snapshot()
	if traffic.TotalDown != 1024 {
		t.Fatal("interval change lost baseline bytes")
	}
	sampler.SetInterval(time.Hour)
	time.Sleep(20 * time.Millisecond)
	reads := stats.readCount()
	time.Sleep(25 * time.Millisecond)
	if stats.readCount() != reads {
		t.Fatal("old foreground ticker kept polling")
	}
	stats.add(downName, 2048)
	sampler.SetInterval(5 * time.Millisecond)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		traffic, _ = observed.snapshot()
		if traffic.TotalDown == 3072 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("totals reset after resume: %+v", traffic)
}

func TestWindowVisibilityChoosesSamplingInterval(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	stats := newFakeStats()
	svc.AttachStats(stats)
	if err := svc.AddTestProfile(testProfile(), "password"); err != nil {
		t.Fatal(err)
	}
	svc.SetWindowVisible(false)
	if err := svc.connectForTest(""); err != nil {
		t.Fatal(err)
	}
	defer svc.disconnectForTest()
	svc.mu.Lock()
	sampler := svc.sampler
	svc.mu.Unlock()
	for _, visible := range []bool{false, true, false, true} {
		svc.SetWindowVisible(visible)
		want := SampleHidden
		if visible {
			want = SampleVisible
		}
		deadline := time.Now().Add(time.Second)
		for {
			sampler.mu.Lock()
			got := sampler.interval
			sampler.mu.Unlock()
			if got == want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("visible=%t interval=%v want %v", visible, got, want)
			}
			time.Sleep(time.Millisecond)
		}
	}
}
