package app

import (
	"context"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

func TestDashboardSamplingCoalescesAndUsesRealMemory(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	var updates atomic.Int32
	svc.state.SetOnChange(func() { updates.Add(1) })
	for range 100 {
		svc.RefreshDashboard()
	}
	end := time.Now().Add(time.Second)
	for time.Now().Before(end) && svc.state.Snapshot().Dashboard.At.IsZero() {
		time.Sleep(time.Millisecond)
	}
	d := svc.state.Snapshot().Dashboard
	if d.At.IsZero() || d.Memory.HeapAllocBytes == 0 || d.Memory.Goroutines == 0 {
		t.Fatalf("invalid dashboard: %+v", d)
	}
	for range 100 {
		svc.RefreshDashboard()
	}
	if updates.Load() != 1 {
		t.Fatalf("duplicate sampling: %d updates", updates.Load())
	}
}

func TestManualMemoryReleaseCollectsAndRefreshesDashboard(t *testing.T) {
	svc, _ := newTestService(t)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	awaitOperation(t, svc.ReleaseMemory())
	runtime.ReadMemStats(&after)
	d := svc.state.Snapshot().Dashboard
	if after.NumGC <= before.NumGC || d.MemoryReleasedAt.IsZero() || d.MemoryReleasing {
		t.Fatalf("GC did not complete: before=%d after=%d dashboard=%+v", before.NumGC, after.NumGC, d)
	}
	if d.Memory.HeapAllocBytes == 0 || d.At.Before(d.MemoryReleasedAt) {
		t.Fatal("memory was not resampled after collection")
	}
}

func TestDashboardNodeProbeTargetsOnlySelectedNode(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	for _, p := range []store.Profile{{ID: "first", Name: "first", Protocol: store.ProtoVLESS, Address: "first.invalid", Port: 443}, {ID: "second", Name: "second", Protocol: store.ProtoVLESS, Address: "second.invalid", Port: 8443}} {
		if err := svc.AddTestProfile(p, ""); err != nil {
			t.Fatal(err)
		}
	}
	dialed := make(chan string, 2)
	p := svc.Prober()
	p.Dial = func(_ context.Context, address string) (net.Conn, error) {
		dialed <- address
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}
	svc.TestNode("second")
	select {
	case address := <-dialed:
		if address != "second.invalid:8443" {
			t.Fatalf("wrong node %q", address)
		}
	case <-time.After(time.Second):
		t.Fatal("probe did not start")
	}
	p.Wait()
	if results := svc.Latencies(); len(results) != 1 || results["second"].State != LatencyOK {
		t.Fatalf("wrong results: %+v", results)
	}
}

func TestDashboardRespectsCachedSnapshot(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	stamp := time.Now().Add(time.Hour)
	svc.state.Update(func(sn *Snapshot) { sn.Dashboard = Dashboard{At: stamp, Connections: 23} })
	svc.RefreshDashboard()
	if got := svc.state.Snapshot().Dashboard; !got.At.Equal(stamp) || got.Connections != 23 {
		t.Fatal("cached sample overwritten")
	}
}
