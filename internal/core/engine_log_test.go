package core

import (
	"sync/atomic"
	"testing"
)

// stubAttacher 记录 Attach 的调用次数。
type stubAttacher struct{ calls atomic.Int32 }

func (s *stubAttacher) Attach() { s.calls.Add(1) }

// 日志器只能在成功启动后附加，因为 Xray 在构建配置时会替换
// 进程级处理器。
func TestEngineAttachesLoggerOnStart(t *testing.T) {
	port := freePort(t)
	e := New()
	stub := &stubAttacher{}
	e.SetLogger(stub)

	if err := e.Start(proxyConfig(port)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("Attach called %d times, want 1", got)
	}
	if err := e.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// 启动失败时绝不能附加日志器：没有实例可供记录，
// 抢占处理器还会隐藏后续运行产生的日志。
func TestEngineDoesNotAttachOnFailedStart(t *testing.T) {
	e := New()
	stub := &stubAttacher{}
	e.SetLogger(stub)

	if err := e.Start([]byte(`{"inbounds":[{"protocol":"nope","port":1}]}`)); err == nil {
		t.Fatal("expected a failure")
	}
	if got := stub.calls.Load(); got != 0 {
		t.Fatalf("Attach called %d times after a failed start, want 0", got)
	}
}
