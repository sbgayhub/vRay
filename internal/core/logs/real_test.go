package logs

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/log"
	xcore "github.com/xtls/xray-core/core"

	_ "github.com/sbgayhub/vray/internal/core/distro"
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

// testConfig 是真实核心可接受的最小配置。
func testConfig(t *testing.T, port int) []byte {
	t.Helper()
	cfg, err := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "info"},
		"inbounds": []any{
			map[string]any{
				"tag":      "in-socks",
				"listen":   "127.0.0.1",
				"port":     port,
				"protocol": "socks",
				"settings": map[string]any{"auth": "noauth", "udp": true},
			},
		},
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom", "settings": map[string]any{}},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return cfg
}

// waitForLine 轮询直到收集器持有包含 want 的行。
func waitForLine(t *testing.T, c *Collector, want string) []Entry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		lines := c.Snapshot()
		var joined strings.Builder
		for _, e := range lines {
			joined.WriteString(e.Text)
			joined.WriteString("\n")
		}
		if strings.Contains(joined.String(), want) {
			return lines
		}
		time.Sleep(20 * time.Millisecond)
	}
	var joined strings.Builder
	for _, e := range c.Snapshot() {
		joined.WriteString(e.Text)
		joined.WriteString("\n")
	}
	t.Fatalf("no line containing %q; got:\n%s", want, joined.String())
	return nil
}

// 实例启动后附加的收集器必须收到核心日志。
// 这是唯一能证明接线的测试：在 StartInstance 前附加无效，
// 因为 Xray 的 app/log 在构建配置时会注册自己的处理器，
// 并替换任何已有处理器。
func TestCollectorReceivesRealCoreLogs(t *testing.T) {
	port := freePort(t)
	t.Setenv("xray.location.asset", t.TempDir())

	c, err := NewCollector(500, "")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	defer c.Close()
	c.SetLevel(LevelDebug)

	inst, err := xcore.StartInstance("json", testConfig(t, port))
	if err != nil {
		t.Fatalf("start instance: %v", err)
	}
	defer func() { _ = inst.Close() }()

	c.Attach()

	// Record 走的是 Xray 记录自身日志的同一路径，
	// 因此这能证明该处理器就是核心会调用的处理器。
	log.Record(&log.GeneralMessage{Severity: log.Severity_Warning, Content: "probe-after-attach"})
	lines := waitForLine(t, c, "probe-after-attach")

	// 级别必须保留：UI 会据此着色。
	found := false
	for _, e := range lines {
		if strings.Contains(e.Text, "probe-after-attach") {
			found = true
			if e.Level != LevelWarning {
				t.Fatalf("level = %v, want warning", e.Level)
			}
		}
	}
	if !found {
		t.Fatal("the probe line vanished between reads")
	}
	if c.Dropped() != 0 {
		t.Fatalf("%d lines dropped while recording one line", c.Dropped())
	}
}

// 在 StartInstance 前附加必须被认定为无效，
// 这样测试会记录该陷阱，而不是静默重蹈覆辙。
func TestAttachBeforeStartReceivesNothing(t *testing.T) {
	port := freePort(t)
	t.Setenv("xray.location.asset", t.TempDir())

	c, err := NewCollector(500, "")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	defer c.Close()
	c.SetLevel(LevelDebug)
	c.Attach()

	inst, err := xcore.StartInstance("json", testConfig(t, port))
	if err != nil {
		t.Fatalf("start instance: %v", err)
	}
	defer func() { _ = inst.Close() }()

	// 现在记录的日志会发往 Xray 放置其处理器的位置，除非再次附加 c，
	// 否则不会发往 c。短暂等待既能说明问题，又不会导致失败：
	// 这是已记录的行为，不是需要修复的缺陷。
	time.Sleep(200 * time.Millisecond)
	if n := len(c.Snapshot()); n != 0 {
		t.Fatalf("attaching before StartInstance worked (%d lines); the documented "+
			"behaviour is that Xray replaces the handler, so Attach must come after", n)
	}
}

// 处理器必须能处理 nil 消息：写入路径上的 panic 会导致代理崩溃。
func TestHandleNilMessage(t *testing.T) {
	c, err := NewCollector(10, "")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	defer c.Close()

	c.Handle(nil)
	if n := len(c.Snapshot()); n != 0 {
		t.Fatalf("a nil message produced %d entries", n)
	}
}
