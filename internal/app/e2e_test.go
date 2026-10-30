package app

import (
	"net"
	"strconv"
	"testing"
	"time"

	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/store"
)

// freePort 返回当前无人监听的端口。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitListening 等待 addr 接受连接。
func waitListening(t *testing.T, port int) bool {
	t.Helper()
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// portClosed 报告端口上是否已无监听。
func portClosed(port int) bool {
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 150*time.Millisecond)
		if err != nil {
			return true
		}
		c.Close()
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestEndToEndWithRealEngine 是第一个运行完整栈的测试：真实 Xray 在本进程启动，mixed 入站开始监听，Disconnect 释放端口。它是对“按钮确实可用”的 S7 验收。
//
// 节点指向本地地址，因此不会有流量离开本机：无论上游是否可达，mixed 入站都会启动。
func TestEndToEndWithRealEngine(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{Root: dir}
	if err := p.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	mixedPort := freePort(t)
	apiPort := freePort(t)

	st := NewState(Snapshot{Phase: PhaseIdle, Mode: "system-proxy", DataRoot: dir})
	storer := store.NewFile(p.DB())
	// 直接写入设置：本测试要求 mixed 端口空闲。
	set := store.DefaultSettings()
	set.MixedPort = mixedPort
	if err := storer.SetSettings(set); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	engine := xcore.New()
	svc := New(st, storer, NewMemSecrets(), engine, p)

	if err := svc.AddTestProfile(store.Profile{
		ID: "local", Name: "local", Protocol: store.ProtoVLESS,
		Address: "127.0.0.1", Port: 1, // 有意设为不可达：只关心入站
	}, "11111111-2222-3333-4444-555555555555"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// API 入站只有配置了端口才会生成。
	svc.SetAPIPort(apiPort)

	if err := svc.connectForTest("local"); err != nil {
		t.Fatalf("connect: %v (problem %+v)", err, st.Snapshot().Problem)
	}
	if !engine.Running() {
		t.Fatal("engine not running after a successful connect")
	}
	if !waitListening(t, mixedPort) {
		t.Fatalf("mixed inbound never listened on %d", mixedPort)
	}

	// Disconnect 必须释放端口。
	svc.Disconnect()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st.Snapshot().Phase == PhaseIdle {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Snapshot().Phase != PhaseIdle {
		t.Fatalf("phase = %s after Disconnect, want idle", st.Snapshot().Phase)
	}
	if !portClosed(mixedPort) {
		t.Fatal("mixed inbound still listening after Disconnect")
	}
}
