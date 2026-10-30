package core

import (
	"net"
	"runtime"
	"strconv"
	"testing"
	"time"
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

// proxyConfig 是能证明核心可运行的最小配置：
// 一个 mixed 入站和一个 direct 出站。
func proxyConfig(port int) []byte {
	return []byte(`{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "tag": "in-socks",
    "listen": "127.0.0.1",
    "port": ` + strconv.Itoa(port) + `,
    "protocol": "mixed",
    "settings": {"auth": "noauth", "udp": true, "ip": "127.0.0.1"}
  }],
  "outbounds": [{"tag": "direct", "protocol": "freedom", "settings": {}}]
}`)
}

// waitListening 轮询直到端口接受连接。
func waitListening(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never accepted a connection", addr)
}

// portClosed 报告端口是否已无人监听。
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

// 有效配置应能启动、监听并停止。
func TestEngineStartStop(t *testing.T) {
	port := freePort(t)
	e := New()
	if err := e.Start(proxyConfig(port)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if phase, _ := e.State(); phase != PhaseRunning {
		t.Fatalf("phase = %s, want %s", phase, PhaseRunning)
	}
	waitListening(t, port)

	if err := e.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if phase, _ := e.State(); phase != PhaseIdle {
		t.Fatalf("phase after stop = %s, want %s", phase, PhaseIdle)
	}
	if !portClosed(port) {
		t.Fatal("port still listening after Stop")
	}
}

// 损坏的配置会通不过预检，永远不会进入 Starting 阶段。
func TestEngineRejectsBadConfig(t *testing.T) {
	e := New()
	err := e.Start([]byte(`{"inbounds": [{"protocol": "no-such-protocol", "port": 1}]}`))
	if err == nil {
		t.Fatal("expected an error for an unknown protocol")
	}
	p, ok := err.(*Problem)
	if !ok || p.Code != CodeConfigInvalid {
		t.Fatalf("err = %v, want a %s problem", err, CodeConfigInvalid)
	}
	if phase, _ := e.State(); phase != PhaseFailed {
		t.Fatalf("phase = %s, want %s", phase, PhaseFailed)
	}
	if e.Running() {
		t.Fatal("engine reports running after a failed start")
	}
}

// 重复停止没有问题；运行期间再次启动则会被拒绝。
func TestEngineIdempotence(t *testing.T) {
	port := freePort(t)
	e := New()
	if err := e.Start(proxyConfig(port)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Start(proxyConfig(port)); err == nil {
		t.Fatal("second Start should be refused while running")
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestEngineRestartAcrossInstances 回答 docs/03 S1-4：
// Close 后能否在同一进程内再次启动 Xray？切换节点依赖这一点。
func TestEngineRestartAcrossInstances(t *testing.T) {
	goroutinesBefore := runtime.NumGoroutine()

	for i := 0; i < 3; i++ {
		port := freePort(t)
		e := New()
		if err := e.Start(proxyConfig(port)); err != nil {
			t.Fatalf("round %d: Start: %v", i, err)
		}
		waitListening(t, port)
		if err := e.Stop(); err != nil {
			t.Fatalf("round %d: Stop: %v", i, err)
		}
		if !portClosed(port) {
			t.Fatalf("round %d: port still listening", i)
		}
	}

	// Xray 会遗留少量 goroutine；关键是它不会随每轮操作持续增长。
	time.Sleep(500 * time.Millisecond)
	runtime.GC()
	after := runtime.NumGoroutine()
	if after > goroutinesBefore+20 {
		t.Fatalf("goroutines %d -> %d across 3 rounds; leak suspected", goroutinesBefore, after)
	}
	t.Logf("3 start/stop rounds in one process: goroutines %d -> %d", goroutinesBefore, after)
}
