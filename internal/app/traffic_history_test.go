package app

import (
	"testing"

	"github.com/sbgayhub/vray/internal/store"
)

func TestNodeTrafficAccumulatesAcrossSessionsAndReloads(t *testing.T) {
	svc, _ := newTestService(t)
	previous := store.Transfer{}
	svc.recordTraffic("tokyo", &previous, Traffic{TotalUp: 100, TotalDown: 200, UpBps: 10, DownBps: 20})
	svc.recordTraffic("tokyo", &previous, Traffic{TotalUp: 200, TotalDown: 500, UpBps: 30, DownBps: 40})
	previous = store.Transfer{}
	svc.recordTraffic("tokyo", &previous, Traffic{TotalUp: 50, TotalDown: 70})
	if err := svc.FlushTraffic(); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := NewFake(svc.paths.Root)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	usage := reloaded.State().Snapshot().Usage["tokyo"]
	if usage.Upload != 250 || usage.Download != 570 {
		t.Fatalf("usage = %+v", usage)
	}
	snap := svc.State().Snapshot()
	snap.Usage["tokyo"] = store.Transfer{}
	snap.History[0].UpBps = 99999
	if svc.State().Snapshot().Usage["tokyo"].Upload != 250 || svc.State().Snapshot().History[0].UpBps != 10 {
		t.Fatal("traffic snapshot shares mutable storage")
	}
}

func TestTrafficHistoryIsBounded(t *testing.T) {
	svc, _ := newTestService(t)
	previous := store.Transfer{}
	for i := range 180 {
		svc.recordTraffic("node", &previous, Traffic{TotalUp: int64(i)})
	}
	if count := len(svc.State().Snapshot().History); count != TrafficHistoryPoints {
		t.Fatalf("history length=%d", count)
	}
}
