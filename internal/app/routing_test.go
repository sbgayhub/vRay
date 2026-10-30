package app

import (
	"bytes"
	"errors"
	"testing"
)

func TestRoutingModePersistsWithoutStartingIdleCore(t *testing.T) {
	svc, engine := newTestService(t)
	awaitOperation(t, svc.SetRoutingMode("global"))
	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if d.Settings.RoutingMode != "global" || d.Settings.Mode != "system-proxy" || engine.Running() {
		t.Fatalf("inbound and outbound settings confused: %+v", d.Settings)
	}
	// 普通设置保存必须保留独立编辑的路由设置。
	v := svc.state.Snapshot().Settings
	v.LogLevel, v.RoutingMode = "error", ""
	awaitOperation(t, svc.SaveSettings(v))
	if err := svc.Load(); err != nil {
		t.Fatal(err)
	}
	if sn := svc.state.Snapshot(); sn.Settings.RoutingMode != "global" || sn.RoutingSaving {
		t.Fatalf("routing choice was lost: %+v", sn.Settings)
	}
	awaitOperation(t, svc.SetRoutingMode("invalid"))
	d, _ = svc.store.Load()
	if d.Settings.RoutingMode != "global" || svc.state.Snapshot().Problem == nil {
		t.Fatal("invalid choice changed the saved setting")
	}
}

func TestRoutingModeRestartsConnectedCoreAndRetainsNode(t *testing.T) {
	svc, engine := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "11111111-2222-3333-4444-555555555555"); err != nil {
		t.Fatal(err)
	}
	svc.Connect("p1")
	waitPhase(t, svc, PhaseConnected)
	for _, mode := range []string{"global", "direct", "rule"} {
		awaitOperation(t, svc.SetRoutingMode(mode))
		sn := svc.state.Snapshot()
		if sn.Phase != PhaseConnected || sn.Active != "p1" || sn.RoutingSaving {
			t.Fatalf("restart failed: %+v", sn)
		}
		if mode != "rule" && !bytes.Contains(engine.Config(), []byte("mode-"+mode)) {
			t.Fatalf("core did not receive routing mode %s", mode)
		}
	}
	starts, stops := engine.Counts()
	if starts != 4 || stops != 3 {
		t.Fatalf("unexpected lifecycle: starts=%d stops=%d", starts, stops)
	}
	awaitOperation(t, svc.SetRoutingMode("rule"))
	if next, _ := engine.Counts(); next != starts {
		t.Fatal("selecting the current mode restarted the core")
	}
	svc.Disconnect()
	waitPhase(t, svc, PhaseIdle)
}

func TestRoutingRestartFailureIsVisible(t *testing.T) {
	svc, engine := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "11111111-2222-3333-4444-555555555555"); err != nil {
		t.Fatal(err)
	}
	svc.Connect("p1")
	waitPhase(t, svc, PhaseConnected)
	engine.FailStart(errors.New("fixture startup failure"))
	awaitOperation(t, svc.SetRoutingMode("direct"))
	sn := svc.state.Snapshot()
	if sn.Phase != PhaseFailed || sn.Problem == nil || sn.Problem.Code != "CORE_START_FAILED" || sn.RoutingSaving {
		t.Fatalf("restart failure hidden: %+v", sn)
	}
}
