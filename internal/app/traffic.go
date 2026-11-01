package app

import (
	"context"
	"strings"
	"sync"
	"time"
)

// StatsSource 读取流量计数。apiclient.Client 实现它；测试替换为假实现，
// 从而无需 Xray 实例也能驱动采样。
type StatsSource interface {
	// Counters 读取指定计数器。尚不存在的计数器返回零而不是报错。
	Counters(ctx context.Context, names []string, reset bool) (map[string]int64, error)
}

// 前台速率每秒更新一次；隐藏窗口保留较慢的轮询。
const (
	SampleVisible        = time.Second
	SampleHidden         = 3 * time.Second
	TrafficHistoryWindow = 30 * time.Second
	TrafficHistoryPoints = 31
)

// Sampler 把累计字节计数转换为传输速率。
//
// Xray 报告总量而非速率，因此速率是两次读取之差除以间隔时间。第一次读取
// 只建立基线：把它当速率会显示“启动以来全部流量”的尖峰。
type Sampler struct {
	mu     sync.Mutex
	src    StatsSource
	names  []string
	last   map[string]int64
	lastAt time.Time
	out    func(Traffic)

	// totalUp 与 totalDown 跨采样累计，UI 因此能展示本次连接的累计流量，
	// 而不只是最后一个 tick。
	totalUp, totalDown int64

	// stop 关闭以结束循环；done 在循环结束后关闭。
	stop            chan struct{}
	done            chan struct{}
	interval        time.Duration
	intervalChanged chan struct{}
}

// NewSampler 返回从 src 读取指定计数、并向 onTraffic 报告速率的采样器。
func NewSampler(src StatsSource, names []string, onTraffic func(Traffic)) *Sampler {
	return &Sampler{
		src:   src,
		names: append([]string(nil), names...),
		last:  map[string]int64{},
		out:   onTraffic,
	}
}

// Start 按间隔开始采样。重复调用 Start 不产生效果。
func (s *Sampler) Start(interval time.Duration) {
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	s.stop, s.done = stop, done
	s.interval = interval
	changed := make(chan struct{}, 1)
	s.intervalChanged = changed
	s.last = map[string]int64{}
	s.lastAt = time.Time{}
	s.totalUp, s.totalDown = 0, 0
	s.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// 立即采样一次以建立基线，让第一个 tick 有差值可用。
		s.sample()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.sample()
			case <-changed:
				s.mu.Lock()
				next := s.interval
				s.mu.Unlock()
				ticker.Reset(next)
			}
		}
	}()
}

// SetInterval 修改轮询间隔，不重置计数基线与累计值。
func (s *Sampler) SetInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.interval == interval {
		return
	}
	s.interval = interval
	select {
	case s.intervalChanged <- struct{}{}:
	default:
	}
}

// Stop 结束采样并等待循环退出，可重复调用。
func (s *Sampler) Stop() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	s.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

// Running 返回循环是否处于活动状态。
func (s *Sampler) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop != nil
}

// sample 读取一次计数并报告自上次读取以来的速率。
func (s *Sampler) sample() {
	s.mu.Lock()
	src, names, out := s.src, s.names, s.out
	s.mu.Unlock()
	if src == nil || len(names) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	now := time.Now()
	counters, err := src.Counters(ctx, names, false)
	if err != nil {
		// 读取失败不应把旧速率当成当前值：下一个 tick 会重试，
		// 期间 UI 保留上一次的数值。
		return
	}

	s.mu.Lock()
	prev, prevAt := s.last, s.lastAt
	s.last, s.lastAt = counters, now

	if prevAt.IsZero() {
		// 仅建立基线。
		s.mu.Unlock()
		return
	}
	elapsed := now.Sub(prevAt).Seconds()
	if elapsed <= 0 {
		s.mu.Unlock()
		return
	}

	t := Traffic{}
	for _, name := range names {
		delta := counters[name] - prev[name]
		if delta < 0 {
			// 计数被重置（内核重启）：本轮按零流量处理，而不是巨大的负速率。
			delta = 0
		}
		switch directionOf(name) {
		case "uplink":
			t.UpBps += int64(float64(delta) / elapsed)
			t.TotalUp += delta
		case "downlink":
			t.DownBps += int64(float64(delta) / elapsed)
			t.TotalDown += delta
		}
	}
	s.totalUp += t.TotalUp
	s.totalDown += t.TotalDown
	t.TotalUp, t.TotalDown = s.totalUp, s.totalDown
	s.mu.Unlock()

	// 回调会一路走到状态更新与 UI 通知，绝不能在持锁状态下调用：
	// 当前回调不碰 Sampler 只是巧合，不是保证。
	if out != nil {
		out(t)
	}
}

// directionOf 从 configgen.Name 构造的计数器名中读取方向，
// 例如 "outbound>>>proxy>>>traffic>>>uplink"。
func directionOf(name string) string {
	switch {
	case strings.HasSuffix(name, "uplink"):
		return "uplink"
	case strings.HasSuffix(name, "downlink"):
		return "downlink"
	default:
		return ""
	}
}
