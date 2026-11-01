// Package apiclient 与同一进程中运行的 Xray 实例的 stats API 通信。
//
// 这里使用回环 TCP 端口而非进程内 bufconn，这是有意为之，并非疏忽：
// Xray 在 app/commander 中构建 gRPC 服务器，且从不将其暴露。Commander
// 没有 *grpc.Server 的访问器，而 Register(server *grpc.Server) 只能从
// Commander 自身拥有的服务列表访问。因此没有可传入 bufconn 监听器的
// 接入点，只能按 Xray 文档所述方式访问 API：在 127.0.0.1 上使用
// dokodemo-door 入站，并对其建立 gRPC 客户端。
//
// 代价是一条打开一次后复用的回环连接。它承载的流量（每半秒若干计数器）
// 远低于用户可感知的程度，而且绝不会离开本机。
package apiclient

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	stats "github.com/xtls/xray-core/app/stats/command"
)

// ErrNotConnected 在调用 Connect 之前或调用 Close 之后返回。
var ErrNotConnected = fmt.Errorf("API 客户端：未连接")

// Client 是 stats API 客户端，可安全并发使用。
type Client struct {
	mu     sync.Mutex
	conn   *grpc.ClientConn
	stats  stats.StatsServiceClient
	port   int
	closed bool
}

// New 返回一个客户端，用于与指定回环端口上的 API 通信。
func New(port int) *Client {
	return &Client{port: port}
}

// Connect 打开连接，具有幂等性：已连接时再次连接不会执行任何操作。
//
// 调用方应在引擎报告运行后连接，因为 API 入站只有在 Xray 启动后才会监听。
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("API 客户端：客户端已关闭")
	}
	if c.conn != nil {
		return nil
	}
	if c.port <= 0 {
		return fmt.Errorf("API 客户端：未配置 API 端口")
	}

	// 设置较短超时并等待连接就绪：否则客户端会静默地把调用排队到
	// 已失效端口上，并一直报告零流量，而不是失败。
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(c.port))
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}),
	)
	if err != nil {
		return fmt.Errorf("API 客户端：连接 127.0.0.1:%d 失败：%w", c.port, err)
	}
	conn.Connect()
	for state := conn.GetState(); state != connectivity.Ready; state = conn.GetState() {
		if state == connectivity.Shutdown {
			_ = conn.Close()
			return fmt.Errorf("API 客户端：连接 127.0.0.1:%d 失败：连接已关闭", c.port)
		}
		if !conn.WaitForStateChange(dialCtx, state) {
			_ = conn.Close()
			return fmt.Errorf("API 客户端：连接 127.0.0.1:%d 失败：%w", c.port, dialCtx.Err())
		}
	}
	c.conn = conn
	c.stats = stats.NewStatsServiceClient(conn)
	return nil
}

// Close 关闭连接，具有幂等性。
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.stats = nil
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// Connected 报告连接是否已打开。
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Stats 读取一个计数器，并可选重置它。
//
// 调用方传入由 configgen.Name 生成的完整名称，例如
// "outbound>>>proxy>>>traffic>>>uplink"。
func (c *Client) Stats(ctx context.Context, name string, reset bool) (int64, error) {
	client, err := c.statsClient()
	if err != nil {
		return 0, err
	}
	resp, err := client.GetStats(ctx, &stats.GetStatsRequest{Name: name, Reset_: reset})
	if err != nil {
		return 0, fmt.Errorf("API 客户端：读取 %s 失败：%w", name, err)
	}
	return resp.GetStat().GetValue(), nil
}

// Counters 读取多个计数器，不存在时报告为零。
//
// 不重置的读取（采样主路径：20 节点的策略组每秒要读 40 个名字）走
// QueryStats 一次往返取回；逐个 GetStats 会放大成同等次数的 RPC。
// 重置语义必须精确：QueryStats 的 pattern 是子串匹配，会把同前缀的
// 其他计数器一并清零，因此 reset=true 仍逐个读取。
func (c *Client) Counters(ctx context.Context, names []string, reset bool) (map[string]int64, error) {
	out := make(map[string]int64, len(names))
	for _, name := range names {
		out[name] = 0
	}
	if reset {
		return c.countersOneByOne(ctx, out, reset)
	}
	pattern := counterPattern(names)
	if pattern == "" {
		return c.countersOneByOne(ctx, out, reset)
	}
	client, err := c.statsClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.QueryStats(ctx, &stats.QueryStatsRequest{Pattern: pattern, Reset_: reset})
	if err != nil {
		// 批量查询失败不致命：退回逐个读取。
		return c.countersOneByOne(ctx, out, reset)
	}
	for _, stat := range resp.GetStat() {
		if _, wanted := out[stat.GetName()]; wanted {
			out[stat.GetName()] = stat.GetValue()
		}
	}
	return out, nil
}

// countersOneByOne 逐个读取计数器；请求列表中 Xray 尚未创建的名字报告为零。
func (c *Client) countersOneByOne(ctx context.Context, out map[string]int64, reset bool) (map[string]int64, error) {
	client, err := c.statsClient()
	if err != nil {
		return nil, err
	}
	for name := range out {
		resp, err := client.GetStats(ctx, &stats.GetStatsRequest{Name: name, Reset_: reset})
		if err != nil {
			return nil, fmt.Errorf("API 客户端：读取 %s 失败：%w", name, err)
		}
		out[name] = resp.GetStat().GetValue()
	}
	return out, nil
}

// counterPattern 返回所有名字的公共前缀，并对齐到 ">>>" 分隔符边界，
// 供 QueryStats 做子串匹配；没有公共前缀时返回空串。
func counterPattern(names []string) string {
	if len(names) == 0 {
		return ""
	}
	prefix := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, prefix) {
			prefix = prefix[:len(prefix)-1]
			if prefix == "" {
				return ""
			}
		}
	}
	if i := strings.LastIndex(prefix, ">>>"); i >= 0 {
		return prefix[:i+3]
	}
	return ""
}

// statsClient 返回当前可用的客户端；若未连接则返回 ErrNotConnected。
func (c *Client) statsClient() (stats.StatsServiceClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stats == nil {
		return nil, ErrNotConnected
	}
	return c.stats, nil
}
