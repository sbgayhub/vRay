package app

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

// fakeConn 是一个只需实现 Close 的 net.Conn。
type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

// newTestProber 返回带脚本化拨号器的探测器。
//
// 每个地址要么在固定“时间”后响应，要么失败；时钟由拨号器自身推进，因此测得的延迟是精确值而非区间。
func newTestProber(script map[string]struct {
	fail bool
	took time.Duration
}) (*Prober, *[]string) {
	p := NewProber()
	var mu sync.Mutex
	var dialled []string
	now := time.Unix(0, 0)
	p.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	p.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		mu.Lock()
		dialled = append(dialled, addr)
		mu.Unlock()
		step, ok := script[addr]
		if !ok {
			return nil, errors.New("no route to host")
		}
		mu.Lock()
		now = now.Add(step.took)
		mu.Unlock()
		if step.fail {
			return nil, errors.New("connection refused")
		}
		return fakeConn{}, nil
	}
	return p, &dialled
}

func probeProfile(id, addr string, port int) store.Profile {
	return store.Profile{
		ID: id, Name: id, Protocol: store.ProtoVLESS,
		Address: addr, Port: port,
	}
}

// 探测成功会报告测得的时间，失败会报告原因。
func TestProberReportsLatencyAndFailure(t *testing.T) {
	p, _ := newTestProber(map[string]struct {
		fail bool
		took time.Duration
	}{
		"ok.example.com:443":   {took: 42 * time.Millisecond},
		"dead.example.com:443": {fail: true},
	})

	p.Start([]store.Profile{
		probeProfile("ok", "ok.example.com", 443),
		probeProfile("dead", "dead.example.com", 443),
	})
	p.Wait()

	if got := p.Result("ok"); got.State != LatencyOK || got.MS != 42 {
		t.Fatalf("ok node = %+v, want 42 ms", got)
	}
	if got := p.Result("dead"); got.State != LatencyFailed || got.Error == "" {
		t.Fatalf("dead node = %+v, want a failure with a reason", got)
	}
}

// 结果逐个到达，因此列表会随着运行逐步填充。
//
// 如果只在结束时发布结果，大型订阅会让整个列表显示 pending 长达数分钟。
func TestProberPublishesEachResultAsItLands(t *testing.T) {
	p, _ := newTestProber(map[string]struct {
		fail bool
		took time.Duration
	}{
		"a:1": {took: time.Millisecond},
		"b:2": {took: time.Millisecond},
	})
	p.Timeout = time.Second

	var frames int
	var mu sync.Mutex
	p.SetOnChange(func() {
		mu.Lock()
		frames++
		mu.Unlock()
	})

	p.Start([]store.Profile{probeProfile("a", "a", 1), probeProfile("b", "b", 2)})
	p.Wait()

	mu.Lock()
	n := frames
	mu.Unlock()
	// 一次用于“全部 pending”，每个结果一次，运行结束一次。
	if n < 4 {
		t.Fatalf("repaints = %d, want at least 4 (pending, two results, done)", n)
	}
}

// 并发数必须真正限制同时在途的探测，而不是一次性启动所有节点。
func TestProberRunsAtConfiguredConcurrency(t *testing.T) {
	const concurrency = 3
	p := NewProber()
	p.SetLimits(concurrency, 2*time.Second)

	started := make(chan struct{}, 5)
	release := make(chan struct{})
	var active, peak atomic.Int32
	p.Dial = func(ctx context.Context, _ string) (net.Conn, error) {
		current := active.Add(1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		active.Add(-1)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return fakeConn{}, nil
	}

	profiles := make([]store.Profile, 5)
	for i := range profiles {
		profiles[i] = probeProfile(strconv.Itoa(i), "node"+strconv.Itoa(i), 443)
	}
	p.Start(profiles)
	for range concurrency {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("configured workers did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("more dials started than the configured concurrency")
	default:
	}
	if got := active.Load(); got != concurrency {
		t.Fatalf("active dials = %d, want %d", got, concurrency)
	}

	close(release)
	p.Wait()
	if got := peak.Load(); got != concurrency {
		t.Fatalf("peak concurrency = %d, want %d", got, concurrency)
	}
	if got := len(p.Results()); got != len(profiles) {
		t.Fatalf("results = %d, want %d", got, len(profiles))
	}
}

// Cancel 停止运行，不留下仍在自旋的任务。
func TestProberCancelLeavesNothingPending(t *testing.T) {
	block := make(chan struct{})
	p := NewProber()
	p.Now = time.Now
	p.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		<-block // 自身永远不会响应
		return nil, errors.New("cancelled")
	}

	done := make(chan struct{})
	p.SetOnChange(func() {
		select {
		case <-done:
		default:
			close(done)
		}
	})

	p.Start([]store.Profile{probeProfile("slow", "slow.example.com", 443)})
	// 等到运行将其标记为 pending。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && p.Result("slow").State != LatencyPending {
		time.Sleep(time.Millisecond)
	}

	p.Cancel()
	close(block)
	p.Wait()

	if p.Running() {
		t.Fatal("still running after Cancel")
	}
	if got := p.Result("slow"); got.State == LatencyPending {
		t.Fatalf("node still pending after Cancel: %+v", got)
	}
}

// 已取消的运行不得写入取代它的新运行。
//
// Cancel 会把结果设为 skipped；否则旧运行中迟到的结果在新 Start 之后落地，会用过期数据覆盖新运行的实时状态。
func TestProberStaleRunDoesNotOverwriteNewRun(t *testing.T) {
	release := make(chan struct{})
	p := NewProber()
	p.Now = time.Now
	first := true
	var mu sync.Mutex
	p.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		mu.Lock()
		was, first := first, false
		_ = was
		mu.Unlock()
		if first {
			<-release
			return nil, errors.New("stale")
		}
		return fakeConn{}, nil
	}

	p.Start([]store.Profile{probeProfile("n", "n.example.com", 443)})
	time.Sleep(20 * time.Millisecond)
	p.Cancel()
	close(release)
	// 让过期的 goroutine 完成探测并尝试发布。
	time.Sleep(20 * time.Millisecond)

	// 新运行获胜：现在发布的内容必须来自它。
	p.Start([]store.Profile{probeProfile("n", "n.example.com", 443)})
	p.Wait()
	if got := p.Result("n"); got.State != LatencyOK {
		t.Fatalf("node = %+v, want the second run's ok result", got)
	}
}

// 探测会超时，而不是在黑洞地址上永远挂起。
func TestProberRespectsTimeout(t *testing.T) {
	p := NewProber()
	p.Now = time.Now
	p.SetLimits(1, 50*time.Millisecond)
	p.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	start := time.Now()
	p.Start([]store.Profile{probeProfile("hole", "hole.example.com", 443)})
	p.Wait()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("probe took %s, the timeout did not apply", elapsed)
	}
	if got := p.Result("hole"); got.State != LatencyFailed {
		t.Fatalf("node = %+v, want a failure", got)
	}
}

// 对需要显示的状态，标签绝不能为空。
func TestLatencyLabel(t *testing.T) {
	cases := map[Latency]string{
		{State: LatencyPending}:    "…",
		{State: LatencyOK, MS: 88}: "88 ms",
		{State: LatencyFailed}:     "失败",
		{State: LatencySkipped}:    "—",
		{State: LatencyIdle}:       "",
	}
	for in, want := range cases {
		if got := LatencyLabel(in); got != want {
			t.Fatalf("LatencyLabel(%+v) = %q, want %q", in, got, want)
		}
	}
}

// 对真实监听器的真实探测会测出合理结果。
//
// 这是唯一接触网络栈的探测测试：它验证生产拨号器和超时设置已正确接入，而不只是验证脚本化实现。
func TestProberRealListener(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port

	p := NewProber()
	p.Start([]store.Profile{probeProfile("local", "127.0.0.1", port)})
	p.Wait()

	got := p.Result("local")
	if got.State != LatencyOK {
		t.Fatalf("local listener = %+v, want ok", got)
	}
	if got.MS < 0 || got.MS > 5000 {
		t.Fatalf("latency = %d ms, want a sane number", got.MS)
	}
}
