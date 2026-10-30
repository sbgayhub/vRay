package app

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/sbgayhub/vray/internal/store"
)

// LatencyState 表示单个节点探测所处的状态。
type LatencyState string

// 探测状态。pending 画成转圈，failed 画成短横并在悬停时显示原因，
// 列表因此不会默默显示空白。
const (
	LatencyIdle    LatencyState = ""
	LatencyPending LatencyState = "pending"
	LatencyOK      LatencyState = "ok"
	LatencyFailed  LatencyState = "failed"
	LatencySkipped LatencyState = "skipped"
)

// Latency 是单个节点的探测结果。
type Latency struct {
	State LatencyState `json:"state"`
	MS    int64        `json:"ms,omitempty"`
	Error string       `json:"error,omitempty"`
}

// ProbeDialer 向 addr 建立 TCP 连接。测试会替换它；生产环境使用真实网络。
type ProbeDialer func(ctx context.Context, addr string) (net.Conn, error)

// Prober 测量节点可达性与连接耗时。
//
// 它刻意只做有限的测量：向节点自己的地址和端口发起 TCP 连接。它不把
// 流量挤过隧道，因为那需要内核运行该节点，而为每个节点重启内核既慢又会
// 打断正在使用的连接。因此这个数字的含义是“该端点多久接受连接”，
// 正是列表需要的：快速找出死节点。
//
// 它也刻意不尝试 TLS 或 REALITY 握手：错误的 SNI 或公钥会把可用节点
// 报成死节点，那比诚实的“端口有响应”更令人困惑。
type Prober struct {
	// Dial 与 Now 可注入，测试因此能获得确定的时间。
	Dial ProbeDialer
	Now  func() time.Time

	// Timeout 限制单个节点的探测时长。
	Timeout time.Duration

	mu      sync.Mutex
	results map[string]Latency
	// generation 防止已取消的运行写入更新的运行：取消后立即重新开始时，
	// 旧运行的残余任务不得覆盖新结果。
	generation uint64
	running    bool
	cancel     context.CancelFunc
	onChange   func()
	done       chan struct{}
}

// NewProber 返回使用真实网络的探测器。
func NewProber() *Prober {
	return &Prober{
		Dial: func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
		Now:     time.Now,
		Timeout: 5 * time.Second,
		results: map[string]Latency{},
	}
}

// SetOnChange 注册请求重绘的回调。
func (p *Prober) SetOnChange(fn func()) {
	p.mu.Lock()
	p.onChange = fn
	p.mu.Unlock()
}

// Result 返回单个节点的延迟。
func (p *Prober) Result(id string) Latency {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.results[id]
}

// Results 返回全部结果的副本。
func (p *Prober) Results() map[string]Latency {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]Latency, len(p.results))
	for k, v := range p.results {
		out[k] = v
	}
	return out
}

// Running 返回是否有运行正在进行。
func (p *Prober) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// Start 探测 list 中的每个节点，并在每个结果落地时报告。
//
// 一次只探测一个节点，串行执行：同时向一千个节点发起探测会耗尽临时端口，
// 让每次探测都像超时。结果仍逐个到达，列表会边测边填。运行期间再次
// 启动会重启本次运行，这正是按钮应有的语义。
func (p *Prober) Start(list []store.Profile) {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.generation++
	gen := p.generation
	p.running = true
	done := make(chan struct{})
	p.done = done
	// 先把所有节点标为 pending，让列表显示排队情况。
	for _, prof := range list {
		p.results[prof.ID] = Latency{State: LatencyPending}
	}
	p.mu.Unlock()
	p.notify()

	go func() {
		defer close(done)
		defer func() {
			p.mu.Lock()
			if p.generation == gen {
				p.running = false
			}
			p.mu.Unlock()
			p.notify()
		}()

		for _, prof := range list {
			res := p.probeOne(ctx, prof)
			p.mu.Lock()
			stale := p.generation != gen
			if !stale {
				p.results[prof.ID] = res
			}
			p.mu.Unlock()
			if stale {
				return
			}
			p.notify()
			if ctx.Err() != nil {
				return
			}
		}
	}()
}

// Cancel 停止运行，并把仍在排队的节点标为跳过，避免它们永远转圈。
func (p *Prober) Cancel() {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.generation++
	for id, r := range p.results {
		if r.State == LatencyPending {
			p.results[id] = Latency{State: LatencySkipped, Error: "已取消"}
		}
	}
	p.running = false
	p.mu.Unlock()
	p.notify()
}

// Wait 阻塞到当前运行结束。测试使用它；UI 不使用，因为阻塞会冻结一帧。
func (p *Prober) Wait() {
	p.mu.Lock()
	done, running := p.done, p.running
	p.mu.Unlock()
	if running && done != nil {
		<-done
	}
}

// notify 在锁外请求重绘。
func (p *Prober) notify() {
	p.mu.Lock()
	fn := p.onChange
	p.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// probeOne 测量一个节点。
func (p *Prober) probeOne(ctx context.Context, prof store.Profile) Latency {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 地址和端口同时存在于出站 JSON 与数据列中；outboundOf 会合并它们，
	// 使被探测节点与连接节点使用同一套描述。
	o := outboundOf(prof, "")

	addr := net.JoinHostPort(o.Address, strconv.Itoa(o.Port))
	start := p.Now()

	dial := p.Dial
	if dial == nil {
		dial = NewProber().Dial
	}
	conn, err := dial(ctx, addr)
	if err != nil {
		return Latency{State: LatencyFailed, Error: TrimDetail(err.Error())}
	}
	conn.Close()

	ms := p.Now().Sub(start).Milliseconds()
	if ms < 0 {
		// 注入的时钟若倒退，不能输出负延迟；报告零而不是无意义的数值。
		ms = 0
	}
	return Latency{State: LatencyOK, MS: ms}
}

// LatencyLabel 是节点行展示单个探测结果的文字。
func LatencyLabel(l Latency) string {
	switch l.State {
	case LatencyPending:
		return "…"
	case LatencyOK:
		return strconv.FormatInt(l.MS, 10) + " ms"
	case LatencyFailed:
		return "失败"
	case LatencySkipped:
		return "—"
	default:
		return ""
	}
}
