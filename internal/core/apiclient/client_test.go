package apiclient

import (
	"context"
	"errors"
	"net"
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

// Connect 前读取统计必须明确失败，而不是返回零值，
// 避免未连接的客户端伪装成“无流量”。
func TestStatsBeforeConnect(t *testing.T) {
	c := New(freePort(t))
	_, err := c.Stats(
		context.Background(),
		"outbound>>>proxy>>>traffic>>>uplink",
		false,
	)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v, want ErrNotConnected", err)
	}
	if _, err := c.Counters(context.Background(), []string{"a"}, false); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Counters err = %v, want ErrNotConnected", err)
	}
}

// 拒绝连接的端口必须让 Connect 失败，而不能挂起。
func TestConnectToDeadPort(t *testing.T) {
	c := New(freePort(t))
	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("Connect succeeded against a dead port")
	}
	if c.Connected() {
		t.Fatal("Connected reports true after a failed Connect")
	}
}

// Close 必须幂等；Close 之后再 Connect 应被拒绝。
func TestCloseIsFinal(t *testing.T) {
	c := New(freePort(t))
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("Connect succeeded after Close")
	}
}

// 没有端口就没有 API；Connect 应直接报错，而不是拨号 127.0.0.1:0。
func TestConnectWithoutPort(t *testing.T) {
	c := New(0)
	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("Connect succeeded without a port")
	}
}

// 环回端口若接受后立即关闭，Connect 也不能挂起。
func TestConnectToNonGRPCPortReturns(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	c := New(l.Addr().(*net.TCPAddr).Port)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_ = c.Connect(ctx)
	_ = c.Close()
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("Connect took %v against a non-gRPC port", elapsed)
	}
}

// 新建客户端应处于未连接状态，并保留其端口。
func TestNewKeepsPort(t *testing.T) {
	p := freePort(t)
	c := New(p)
	if c.port != p {
		t.Fatalf("port = %d, want %d", c.port, p)
	}
	if c.Connected() {
		t.Fatal("a fresh client reports connected")
	}
}
