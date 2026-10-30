package ui

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/store"
)

// 节点页提供测速按钮，运行期间它会变为取消按钮：死节点列表必须可以中断。
func TestNodesTestButtonToggles(t *testing.T) {
	r, svc := newRoot(t)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("延迟测速") {
		t.Fatal("the test button is missing")
	}
	if tr.HasText("取消延迟测速") {
		t.Fatal("the cancel label shows before a run starts")
	}

	// 启动一次无法完成的测速，以便观察取消状态。
	p := svc.Prober()
	p.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p.Timeout = time.Minute
	svc.TestAll()
	defer svc.CancelTest()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !svc.Testing() {
		time.Sleep(time.Millisecond)
	}
	if !svc.Testing() {
		t.Fatal("the run never started")
	}

	tr.Frame()
	if !tr.HasText("取消延迟测速") {
		t.Fatal("the cancel label is missing while a run is in flight")
	}
}

// 测速完成后在行内显示数值，失败则显示文字。
func TestNodesShowsLatency(t *testing.T) {
	svc, _ := app.NewFake(t.TempDir())
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
	ok := store.Profile{
		ID: "ok", Name: "live", Protocol: store.ProtoVLESS,
		Address: "127.0.0.1", Port: port,
	}
	if err := svc.AddTestProfile(ok, "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	svc.TestAll()
	svc.Prober().Wait()

	if got := svc.Latencies()["ok"]; got.State != app.LatencyOK {
		t.Fatalf("probe = %+v, want ok against a live listener", got)
	}

	r := NewRoot(svc)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("ms") {
		t.Fatal("the measured latency is not drawn in the row")
	}
}

// 测速失败时会用文字说明，因此该行永远不会为空。
func TestNodesShowsFailedLatency(t *testing.T) {
	svc, _ := app.NewFake(t.TempDir())
	p := store.Profile{
		ID: "dead", Name: "dead", Protocol: store.ProtoVLESS,
		// 回环地址上的端口 1 会拒绝连接，因此测速会在本地快速失败。
		Address: "127.0.0.1", Port: 1,
	}
	if err := svc.AddTestProfile(p, "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	svc.Prober().Timeout = 500 * time.Millisecond
	svc.TestAll()
	svc.Prober().Wait()

	if got := svc.Latencies()["dead"]; got.State != app.LatencyFailed {
		t.Fatalf("probe = %+v, want failed", got)
	}

	r := NewRoot(svc)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("失败") {
		t.Fatal("a failed probe is not shown as a word")
	}
}

// REALITY 节点在整个“导入到存储”路径中保留其传输配置，
// 这使延迟测速和连接对地址的判断保持一致。
func TestNodesRowUsesStoredOutbound(t *testing.T) {
	svc, _ := app.NewFake(t.TempDir())
	p := store.Profile{
		ID: "r", Name: "reality", Protocol: store.ProtoVLESS,
		Address: "jp.example.com", Port: 443,
		RawOutbound: json.RawMessage(`{"security":"reality","publicKey":"PUBKEY","network":"grpc"}`),
	}
	if err := svc.AddTestProfile(p, "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	r := NewRoot(svc)
	r.cur, r.route = PageNodes, pageRoutes[PageNodes].ID
	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("jp.example.com") {
		t.Fatal("the row does not show the stored address")
	}
}
