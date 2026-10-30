package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStats 提供可由测试推动的计数器。
type fakeStats struct {
	mu     sync.Mutex
	values map[string]int64
	err    error
	reads  int
}

func newFakeStats() *fakeStats {
	return &fakeStats{values: map[string]int64{}}
}

func (f *fakeStats) Counters(ctx context.Context, names []string, reset bool) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]int64, len(names))
	for _, n := range names {
		out[n] = f.values[n]
	}
	return out, nil
}

func (f *fakeStats) add(name string, delta int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.values[name] += delta
}

func (f *fakeStats) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeStats) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// collector 记录采样器报告的内容，并保留观察到的峰值速率。
type collector struct {
	mu       sync.Mutex
	last     Traffic
	calls    int
	peakDown int64
	peakUp   int64
}

func (c *collector) report(t Traffic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.last = t
	c.calls++
	if t.DownBps > c.peakDown {
		c.peakDown = t.DownBps
	}
	if t.UpBps > c.peakUp {
		c.peakUp = t.UpBps
	}
}

func (c *collector) snapshot() (Traffic, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last, c.calls
}

func (c *collector) peakDownRate() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peakDown
}

func (c *collector) peakUpRate() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peakUp
}

const (
	upName   = "outbound>>>proxy>>>traffic>>>uplink"
	downName = "outbound>>>proxy>>>traffic>>>downlink"
)

// 第一次读取只是基线：若把它当作速率，会显示等于引擎启动以来所有字节的尖峰。
func TestSamplerFirstReadIsBaseline(t *testing.T) {
	stats := newFakeStats()
	stats.add(downName, 1_000_000)

	c := &collector{}
	s := NewSampler(stats, []string{downName}, c.report)
	s.Start(20 * time.Millisecond)
	defer s.Stop()

	time.Sleep(60 * time.Millisecond)

	got, calls := c.snapshot()
	if calls == 0 {
		t.Fatal("the sampler never reported")
	}
	if got.DownBps != 0 {
		t.Fatalf("first report = %d B/s, want 0 (no traffic since the baseline)", got.DownBps)
	}
	if got.TotalDown != 0 {
		t.Fatalf("first report total = %d, want 0", got.TotalDown)
	}
}

// 增长的计数器会变成正速率和增长的总量。
//
// 速率只在流量之后的第一次采样为非零；下一次 tick 没有新数据可报告，这是正确的：速率表示近期速度，而不是历史总量。
func TestSamplerReportsRate(t *testing.T) {
	stats := newFakeStats()
	c := &collector{}
	s := NewSampler(stats, []string{downName}, c.report)
	s.Start(20 * time.Millisecond)
	defer s.Stop()

	time.Sleep(60 * time.Millisecond) // 让基线落地
	stats.add(downName, 5000)
	time.Sleep(100 * time.Millisecond)

	got, _ := c.snapshot()
	if got.TotalDown != 5000 {
		t.Fatalf("TotalDown = %d, want 5000", got.TotalDown)
	}
	if got.TotalUp != 0 {
		t.Fatalf("TotalUp = %d, want 0", got.TotalUp)
	}
	if peak := c.peakDownRate(); peak <= 0 {
		t.Fatal("the sampler never reported a positive downlink rate")
	}
}

// 上行和下行会归入正确字段。
func TestSamplerSplitsDirections(t *testing.T) {
	stats := newFakeStats()
	c := &collector{}
	s := NewSampler(stats, []string{upName, downName}, c.report)
	s.Start(20 * time.Millisecond)
	defer s.Stop()

	time.Sleep(60 * time.Millisecond)
	stats.add(upName, 1000)
	stats.add(downName, 3000)
	time.Sleep(100 * time.Millisecond)

	got, _ := c.snapshot()
	if got.TotalUp != 1000 {
		t.Fatalf("TotalUp = %d, want 1000", got.TotalUp)
	}
	if got.TotalDown != 3000 {
		t.Fatalf("TotalDown = %d, want 3000", got.TotalDown)
	}
	if c.peakUpRate() <= 0 {
		t.Fatal("no positive uplink rate was reported")
	}
	if c.peakDownRate() <= 0 {
		t.Fatal("no positive downlink rate was reported")
	}
}

// 总量会累加：它们表示连接承载的总量，而不是最后一次 tick。
func TestSamplerTotalsAccumulate(t *testing.T) {
	stats := newFakeStats()
	c := &collector{}
	s := NewSampler(stats, []string{downName}, c.report)
	s.Start(20 * time.Millisecond)
	defer s.Stop()

	time.Sleep(60 * time.Millisecond)
	stats.add(downName, 1000)
	time.Sleep(60 * time.Millisecond)
	stats.add(downName, 2000)
	time.Sleep(100 * time.Millisecond)

	got, _ := c.snapshot()
	if got.TotalDown != 3000 {
		t.Fatalf("TotalDown = %d, want 3000 across two bursts", got.TotalDown)
	}
}

// 计数器回退（引擎重启）时绝不能产生巨大的负速率。
func TestSamplerHandlesCounterReset(t *testing.T) {
	stats := newFakeStats()
	stats.add(downName, 100_000)

	c := &collector{}
	s := NewSampler(stats, []string{downName}, c.report)
	s.Start(20 * time.Millisecond)
	defer s.Stop()

	time.Sleep(60 * time.Millisecond)
	stats.mu.Lock()
	stats.values[downName] = 10
	stats.mu.Unlock()
	time.Sleep(100 * time.Millisecond)

	got, _ := c.snapshot()
	if got.DownBps < 0 {
		t.Fatalf("DownBps = %d, want a non-negative rate after a reset", got.DownBps)
	}
}

// 统计源失败不能停止采样，总量也不能倒退。
func TestSamplerSurvivesReadErrors(t *testing.T) {
	stats := newFakeStats()
	c := &collector{}
	s := NewSampler(stats, []string{downName}, c.report)
	s.Start(20 * time.Millisecond)
	defer s.Stop()

	time.Sleep(60 * time.Millisecond)
	stats.add(downName, 2000)
	time.Sleep(100 * time.Millisecond)
	before, callsBefore := c.snapshot()
	if callsBefore == 0 {
		t.Fatal("no report before the failure")
	}

	stats.fail(errors.New("api down"))
	time.Sleep(80 * time.Millisecond)
	stats.fail(nil)
	time.Sleep(100 * time.Millisecond)

	after, callsAfter := c.snapshot()
	if callsAfter <= callsBefore {
		t.Fatal("sampling stopped after an error")
	}
	if after.TotalDown < before.TotalDown {
		t.Fatalf("totals went backwards during an error: %d -> %d", before.TotalDown, after.TotalDown)
	}
}

// Start 是幂等的，Stop 可以安全调用两次。
func TestSamplerStartStopAreIdempotent(t *testing.T) {
	stats := newFakeStats()
	c := &collector{}
	s := NewSampler(stats, []string{upName}, c.report)

	s.Start(20 * time.Millisecond)
	s.Start(20 * time.Millisecond)
	if !s.Running() {
		t.Fatal("not running after Start")
	}

	s.Stop()
	s.Stop()
	if s.Running() {
		t.Fatal("still running after Stop")
	}

	reads := stats.readCount()
	time.Sleep(80 * time.Millisecond)
	if got := stats.readCount(); got != reads {
		t.Fatalf("read %d more times after Stop", got-reads)
	}
}

// Stop 会等待循环结束，因此调用返回后不会有采样仍在后台运行。
func TestSamplerStopWaits(t *testing.T) {
	stats := newFakeStats()
	c := &collector{}
	s := NewSampler(stats, []string{upName}, c.report)
	s.Start(10 * time.Millisecond)
	time.Sleep(40 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return")
	}
	if s.Running() {
		t.Fatal("Running reports true after Stop returned")
	}
}

// directionOf 必须按 configgen.Name 的写入方式读取方向。
func TestDirectionOf(t *testing.T) {
	cases := map[string]string{
		upName:                                  "uplink",
		downName:                                "downlink",
		"inbound>>>in-socks>>>traffic>>>uplink": "uplink",
		"user>>>me>>>traffic>>>downlink":        "downlink",
		"something-else":                        "",
	}
	for name, want := range cases {
		if got := directionOf(name); got != want {
			t.Fatalf("directionOf(%q) = %q, want %q", name, got, want)
		}
	}
}

// 采样间隔与计划要求一致。
func TestSampleIntervals(t *testing.T) {
	if SampleVisible != time.Second {
		t.Fatalf("SampleVisible = %v, want 1s", SampleVisible)
	}
	if SampleHidden != 3*time.Second {
		t.Fatalf("SampleHidden = %v, want 3s", SampleHidden)
	}
}
