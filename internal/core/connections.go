package core

import (
	"context"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/session"
	xray "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/transport"

	"github.com/sbgayhub/vray/internal/core/configgen"
)

// Connection 表示一个活动逻辑 TCP 连接或 UDP 会话。负载字节计数
// 不包含代理/TLS 封装开销。已完成的会话不会保留。
type Connection struct {
	ID                                                        uint64
	Started                                                   time.Time
	Source, Destination, Network, Protocol, Inbound, Outbound string
	NodeName, NodeProtocol                                    string
	Upload, Download                                          int64
}

type connectionRecord struct {
	info Connection
	up   atomic.Int64
	down connectionCounter
}

type connectionTracker struct {
	policyNodes            map[string]configgen.PolicyLabel
	mu                     sync.Mutex
	epoch, sequence        uint64
	active                 map[uint64]*connectionRecord
	nodeName, nodeProtocol string
	lastPolicyTag          string
}

func (t *connectionTracker) reset() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.epoch++
	t.active = nil
	t.nodeName, t.nodeProtocol = "", ""
	t.policyNodes = nil
	t.lastPolicyTag = ""
	return t.epoch
}

func (t *connectionTracker) begin(ctx context.Context, epoch uint64, tag string) *connectionRecord {
	in := session.InboundFromContext(ctx)
	if in == nil || in.Tag == "" || in.Tag == configgen.TagInAPI || in.Tag == configgen.TagInDNS {
		return nil
	}
	outs := session.OutboundsFromContext(ctx)
	if len(outs) == 0 {
		return nil
	}
	dest := outs[len(outs)-1].Target
	if route := outs[len(outs)-1].RouteTarget; route.IsValid() {
		dest = route
	}
	if !dest.IsValid() {
		return nil
	}
	r := &connectionRecord{info: Connection{Started: time.Now(), Destination: net.JoinHostPort(dest.Address.String(), dest.Port.String()), Network: dest.Network.String(), Inbound: in.Tag, Outbound: tag}}
	if in.Source.IsValid() {
		r.info.Source = net.JoinHostPort(in.Source.Address.String(), in.Source.Port.String())
	}
	if content := session.ContentFromContext(ctx); content != nil {
		r.info.Protocol = content.Protocol
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if epoch != t.epoch {
		return nil
	}
	t.sequence++
	r.info.ID = t.sequence
	if tag == configgen.TagProxy {
		r.info.NodeName, r.info.NodeProtocol = t.nodeName, t.nodeProtocol
	}
	if node, ok := t.policyNodes[tag]; ok {
		r.info.NodeName, r.info.NodeProtocol = node.Name, node.Protocol
		t.lastPolicyTag = tag
	}
	if t.active == nil {
		t.active = make(map[uint64]*connectionRecord)
	}
	t.active[r.info.ID] = r
	return r
}

// SetConnectionNode 将元数据绑定到实际建立连接的核心，而不是稍后的 UI 选择。
// 它还会填充启动期间开始的会话。
func (e *Engine) SetConnectionNode(name, protocol string) {
	t := e.connections
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nodeName, t.nodeProtocol = name, protocol
	for _, r := range t.active {
		if r.info.Outbound == configgen.TagProxy {
			r.info.NodeName, r.info.NodeProtocol = name, protocol
		}
	}
}

func (t *connectionTracker) finish(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.active, id)
	if len(t.active) == 0 {
		t.active = nil
	}
}

// Connections 返回副本，且不获取引擎的生命周期锁，因此
// 读取此页面永远不会等待代理启动或停止。
func (e *Engine) Connections() []Connection {
	if e.connections == nil {
		return nil
	}
	t := e.connections
	t.mu.Lock()
	list := make([]Connection, 0, len(t.active))
	for _, r := range t.active {
		info := r.info
		info.Upload, info.Download = r.up.Load(), r.down.Value()
		list = append(list, info)
	}
	t.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID })
	return list
}

// ConnectionCount 避免为主页仪表盘复制并排序完整列表。
func (e *Engine) ConnectionCount() int {
	if e.connections == nil {
		return 0
	}
	e.connections.mu.Lock()
	defer e.connections.mu.Unlock()
	return len(e.connections.active)
}

func (e *Engine) trackConnections(inst *xray.Instance) error {
	d, ok := inst.GetFeature(routing.DispatcherType()).(*dispatcher.DefaultDispatcher)
	if !ok {
		return nil
	}
	manager := inst.GetFeature(outbound.ManagerType()).(outbound.Manager)
	tracked := &connectionManager{Manager: manager, tracker: e.connections, epoch: e.connections.reset()}
	return d.Init(&dispatcher.Config{}, tracked,
		inst.GetFeature(routing.RouterType()).(routing.Router),
		inst.GetFeature(policy.ManagerType()).(policy.Manager),
		inst.GetFeature(stats.ManagerType()).(stats.Manager))
}

// 仅包装 dispatcher 看到的 manager 视图。原生 manager、
// 默认出站、API 操作以及 full-handler/Vision 钩子保持原样。
type connectionManager struct {
	outbound.Manager
	tracker *connectionTracker
	epoch   uint64
}

func (m *connectionManager) wrap(h outbound.Handler) outbound.Handler {
	if h == nil {
		return nil
	}
	return &connectionHandler{Handler: h, tracker: m.tracker, epoch: m.epoch}
}
func (m *connectionManager) GetHandler(tag string) outbound.Handler {
	return m.wrap(m.Manager.GetHandler(tag))
}
func (m *connectionManager) GetDefaultHandler() outbound.Handler {
	return m.wrap(m.Manager.GetDefaultHandler())
}

type connectionHandler struct {
	outbound.Handler
	tracker *connectionTracker
	epoch   uint64
}

func (h *connectionHandler) Dispatch(ctx context.Context, link *transport.Link) {
	r := h.tracker.begin(ctx, h.epoch, h.Tag())
	if r == nil {
		h.Handler.Dispatch(ctx, link)
		return
	}
	defer h.tracker.finish(r.info.ID)
	reader := &connectionReader{Reader: link.Reader, counter: &r.up}
	var counted buf.Reader = reader
	if timed, ok := link.Reader.(buf.TimeoutReader); ok {
		counted = &connectionTimeoutReader{connectionReader: reader, timed: timed}
	}
	writer := link.Writer
	if original, ok := writer.(*dispatcher.SizeStatWriter); ok {
		r.down.original = original.Counter
		writer = original.Writer
	}
	// 保持此具体类型：Vision 的直接复制路径会更新其 Counter。
	countedWriter := &dispatcher.SizeStatWriter{Writer: writer, Counter: &r.down}
	// Mux 对非管道读取器采用同步处理，会等待其逻辑会话关闭。
	// 因此启用 mux 时，Dispatch 返回也意味着处理完成。
	h.Handler.Dispatch(ctx, &transport.Link{Reader: counted, Writer: countedWriter})
}

type connectionReader struct {
	buf.Reader
	counter *atomic.Int64
}

func (r *connectionReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBuffer()
	r.counter.Add(int64(mb.Len()))
	return mb, err
}
func (r *connectionReader) Interrupt()   { common.Interrupt(r.Reader) }
func (r *connectionReader) Close() error { return common.Close(r.Reader) }

type connectionTimeoutReader struct {
	*connectionReader
	timed buf.TimeoutReader
}

func (r *connectionTimeoutReader) ReadMultiBufferTimeout(d time.Duration) (buf.MultiBuffer, error) {
	mb, err := r.timed.ReadMultiBufferTimeout(d)
	r.counter.Add(int64(mb.Len()))
	return mb, err
}

type connectionCounter struct {
	value    atomic.Int64
	original stats.Counter
}

func (c *connectionCounter) Value() int64      { return c.value.Load() }
func (c *connectionCounter) Set(n int64) int64 { return c.value.Swap(n) }
func (c *connectionCounter) Add(n int64) int64 {
	if c.original != nil {
		c.original.Add(n)
	}
	return c.value.Add(n)
}
