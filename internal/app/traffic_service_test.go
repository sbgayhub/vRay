package app

import (
	"testing"
	"time"
)

// 连接会启动流量采样器，断开连接会停止它：若采样器继续运行，会持续读取已不存在的 API 端口。
func TestServiceStartsAndStopsSampling(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	stats := newFakeStats()
	svc.AttachStats(stats)

	if err := svc.AddTestProfile(testProfile(), "password"); err != nil {
		t.Fatalf("add profile: %v", err)
	}
	if err := svc.connectForTest(""); err != nil {
		t.Fatalf("connect: %v", err)
	}

	svc.mu.Lock()
	running := svc.sampler != nil && svc.sampler.Running()
	svc.mu.Unlock()
	if !running {
		t.Fatal("sampling did not start on connect")
	}

	// 先让它取得基线，再移动计数器并等待报告。
	time.Sleep(700 * time.Millisecond)
	stats.add("outbound>>>proxy>>>traffic>>>downlink", 4096)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if svc.State().Snapshot().Traffic.TotalDown > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := svc.State().Snapshot().Traffic.TotalDown; got == 0 {
		t.Fatal("traffic did not reach the state")
	}

	if err := svc.disconnectForTest(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	svc.mu.Lock()
	sampler := svc.sampler
	svc.mu.Unlock()
	if sampler != nil {
		t.Fatal("the sampler is still attached after disconnect")
	}
	if got := svc.State().Snapshot().Traffic; got != (Traffic{}) {
		t.Fatalf("traffic not cleared on disconnect: %+v", got)
	}
}

// 没有统计源时服务仍可连接，只是不显示流量。
func TestServiceWithoutStats(t *testing.T) {
	svc, _ := NewFake(t.TempDir())
	if err := svc.AddTestProfile(testProfile(), "password"); err != nil {
		t.Fatalf("add profile: %v", err)
	}
	if err := svc.connectForTest(""); err != nil {
		t.Fatalf("connect without stats: %v", err)
	}
	svc.mu.Lock()
	has := svc.sampler != nil
	svc.mu.Unlock()
	if has {
		t.Fatal("a sampler was created without a stats source")
	}
}

// disconnectForTest 同步执行断开路径。
func (s *Service) disconnectForTest() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopSamplingLocked()
	if err := s.engine.Stop(); err != nil {
		return err
	}
	s.state.Update(func(sn *Snapshot) {
		sn.Phase = PhaseIdle
		sn.Problem = nil
		sn.Traffic = Traffic{}
	})
	return nil
}
