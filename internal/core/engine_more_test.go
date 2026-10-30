package core

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	xray "github.com/xtls/xray-core/core"
)

func TestEngineLoadsConfigOnce(t *testing.T) {
	e := New()
	defer e.Stop()
	loads := 0
	config := proxyConfig(freePort(t))
	e.load = func(_ []byte) (*xray.Config, error) {
		loads++
		return xray.LoadConfig("json", bytes.NewReader(config))
	}
	// 只有注入的加载器理解该输入。验证后重新解析会失败，
	// 而不是启动已经加载的配置。
	if err := e.Start([]byte("loader-specific-input")); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatalf("loaded %d times", loads)
	}
}

type panicAttacher struct{}

func (panicAttacher) Attach() { panic("logger attachment failed") }

func TestEngineStartupPanicReleasesListener(t *testing.T) {
	e := New()
	port := freePort(t)
	e.SetLogger(panicAttacher{})
	if err := e.Start(proxyConfig(port)); err == nil {
		t.Fatal("expected startup panic")
	}
	if e.Running() || !portClosed(port) {
		t.Fatal("failed startup retained its instance/listener")
	}
	e.SetLogger(nil)
	if err := e.Start(proxyConfig(port)); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
}

// 核心内部的 panic 必须被捕获，使窗口能够继续存活并重试
// （docs/03 S4-5）。将配置校验器替换为会 panic 的实现，
// 是故意进入 Start 周围 recover 分支的唯一方式。
func TestEngineRecoversFromPanic(t *testing.T) {
	e := New()
	e.load = func([]byte) (*xray.Config, error) { panic("injected: config loader blew up") }

	err := e.Start([]byte(`{}`))
	if err == nil {
		t.Fatal("a panic must surface as an error, not as a dead process")
	}
	prob, ok := err.(*Problem)
	if !ok {
		t.Fatalf("err = %T, want *Problem", err)
	}
	if prob.Code != CodePanic {
		t.Fatalf("code = %s, want %s", prob.Code, CodePanic)
	}
	if phase, p := e.State(); phase != PhaseFailed || p == nil || p.Code != CodePanic {
		t.Fatalf("state = %s/%v, want failed with %s", phase, p, CodePanic)
	}
	if e.Running() {
		t.Fatal("a panicked engine must not report running")
	}

	// 之后引擎必须仍可使用：窗口提供重试按钮。
	e.mu.Lock()
	e.load = nil
	e.mu.Unlock()
	port := freePort(t)
	if err := e.Start(proxyConfig(port)); err != nil {
		t.Fatalf("retry after a panic failed: %v", err)
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// 十轮启动/停止后，goroutine 数量每轮增长不得超过一个：
// docs/03 S4 DoD。
func TestEngineTenRoundsNoLeak(t *testing.T) {
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	before := runtime.NumGoroutine()

	for i := 0; i < 10; i++ {
		port := freePort(t)
		e := New()
		if err := e.Start(proxyConfig(port)); err != nil {
			t.Fatalf("round %d: Start: %v", i, err)
		}
		if err := e.Stop(); err != nil {
			t.Fatalf("round %d: Stop: %v", i, err)
		}
	}

	time.Sleep(time.Second)
	runtime.GC()
	after := runtime.NumGoroutine()
	t.Logf("10 rounds: goroutines %d -> %d (delta %d)", before, after, after-before)
	if after > before+10 {
		t.Fatalf("goroutines %d -> %d across 10 rounds; more than one per round", before, after)
	}
}
