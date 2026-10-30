package apiclient

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"

	xcore "github.com/xtls/xray-core/core"

	_ "github.com/sbgayhub/vray/internal/core/distro"
)

// apiConfig 返回包含 API 入站、SOCKS 入站且启用统计的配置。
// Xray 的策略默认关闭统计，这是一个不易察觉的陷阱：
// 系统策略启用统计前，所有计数器都会读成零。
func apiConfig(t *testing.T, socksPort, apiPort int) []byte {
	t.Helper()
	cfg := map[string]any{
		"log":   map[string]any{"loglevel": "warning"},
		"stats": map[string]any{},
		"api": map[string]any{
			"tag":      "api",
			"services": []string{"StatsService"},
		},
		"policy": map[string]any{
			"system": map[string]any{
				"statsInboundUplink":    true,
				"statsInboundDownlink":  true,
				"statsOutboundUplink":   true,
				"statsOutboundDownlink": true,
			},
		},
		"inbounds": []any{
			map[string]any{
				"tag":      "in-socks",
				"listen":   "127.0.0.1",
				"port":     socksPort,
				"protocol": "socks",
				"settings": map[string]any{"auth": "noauth", "udp": true},
			},
			map[string]any{
				"tag":      "api-in",
				"listen":   "127.0.0.1",
				"port":     apiPort,
				"protocol": "dokodemo-door",
				"settings": map[string]any{"address": "127.0.0.1"},
			},
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{}},
		},
		"routing": map[string]any{
			"rules": []any{
				map[string]any{"type": "field", "inboundTag": []string{"api-in"}, "outboundTag": "api"},
			},
		},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// 客户端必须从真实实例读取真实计数器：这是唯一同时验证 API 接线、
// 入站路由和统计策略彼此吻合的测试。本包其他测试都使用模拟实现。
func TestReadsStatsFromRealInstance(t *testing.T) {
	socksPort := freePort(t)
	apiPort := freePort(t)

	// 此处数据目录没有 geo 文件也可以：该配置不请求 geoip 或 geosite。
	t.Setenv("xray.location.asset", t.TempDir())

	inst, err := xcore.StartInstance("json", apiConfig(t, socksPort, apiPort))
	if err != nil {
		t.Fatalf("start instance: %v", err)
	}
	defer func() { _ = inst.Close() }()

	// 端口开始接受连接后，API 入站即已启动。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		address := net.JoinHostPort("127.0.0.1", strconv.Itoa(apiPort))
		if c, err := net.DialTimeout("tcp", address, 200*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	c := New(apiPort)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = c.Close() }()
	if !c.Connected() {
		t.Fatal("client does not report connected")
	}

	// 尚未递增的计数器必须读到零而非报错：
	// 这正是用户发送任何流量前 UI 显示的状态。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	name := "outbound>>>direct>>>traffic>>>uplink"
	got, err := c.Stats(ctx, name, false)
	if err != nil {
		t.Fatalf("Stats(%s): %v", name, err)
	}
	if got != 0 {
		t.Fatalf("a fresh counter = %d, want 0", got)
	}

	// 同一名称下，Counters 的结果必须与 Stats 一致。
	m, err := c.Counters(ctx, []string{name}, false)
	if err != nil {
		t.Fatalf("Counters: %v", err)
	}
	if m[name] != got {
		t.Fatalf("Counters = %d, Stats = %d for %s", m[name], got, name)
	}
}

// 流量经过实例后，计数器必须实际增长。读到零只能证明接线存在；
// 读到递增的数值才能证明它接的是正确的计数器。
func TestCounterGrowsWithTraffic(t *testing.T) {
	socksPort := freePort(t)
	apiPort := freePort(t)
	t.Setenv("xray.location.asset", t.TempDir())

	inst, err := xcore.StartInstance("json", apiConfig(t, socksPort, apiPort))
	if err != nil {
		t.Fatalf("start instance: %v", err)
	}
	defer func() { _ = inst.Close() }()

	waitPort(t, apiPort)
	waitPort(t, socksPort)

	c := New(apiPort)
	if err := c.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	name := "outbound>>>direct>>>traffic>>>downlink"

	before, err := c.Stats(ctx, name, false)
	if err != nil {
		t.Fatalf("Stats before: %v", err)
	}

	sendThroughSocks(t, socksPort)
	time.Sleep(500 * time.Millisecond)

	after, err := c.Stats(ctx, name, false)
	if err != nil {
		t.Fatalf("Stats after: %v", err)
	}
	if after <= before {
		t.Fatalf("%s did not grow: %d -> %d", name, before, after)
	}
	t.Logf("%s: %d -> %d bytes", name, before, after)
}

// waitPort 轮询直到环回端口接受连接。
func waitPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never accepted a connection", addr)
}

// sendThroughSocks 向本地服务器发起一次 SOCKS5 CONNECT，
// 让 direct 出站承载少量字节并推动其计数器。
func sendThroughSocks(t *testing.T, socksPort int) {
	t.Helper()

	srv, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer srv.Close()
	go func() {
		for {
			conn, err := srv.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("hello from the target"))
			conn.Close()
		}
	}()
	targetPort := srv.Addr().(*net.TCPAddr).Port

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort)), 3*time.Second)
	if err != nil {
		t.Fatalf("dial socks: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	// 问候：版本 5、一种方法、“无认证”。
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	resp := make([]byte, 2)
	if _, err := conn.Read(resp); err != nil {
		t.Fatalf("read greeting reply: %v", err)
	}

	// CONNECT 到 127.0.0.1:targetPort。
	req := []byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0, 0}
	req[8] = byte(targetPort >> 8)
	req[9] = byte(targetPort & 0xff)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := conn.Read(reply); err != nil {
		t.Fatalf("read connect reply: %v", err)
	}

	// 目标端先发送数据，因此读取即可推动计数器。
	buf := make([]byte, 64)
	_, _ = conn.Read(buf)
}
