package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/store"
)

// newTestService 在临时数据目录上构建服务。
func newTestService(t *testing.T) (*Service, *FakeEngine) {
	t.Helper()
	svc, engine := NewFake(t.TempDir())
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return svc, engine
}

func TestFrameReadsDoNotWaitForEngineOperation(t *testing.T) {
	svc, _ := newTestService(t)
	svc.mu.Lock()
	defer svc.mu.Unlock()
	done := make(chan struct{})
	go func() {
		svc.Latencies()
		svc.Testing()
		svc.Logs()
		svc.DroppedLogs()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a frame reader waited for the engine operation lock")
	}
}

// waitPhase 阻塞等待状态进入 phase。
func waitPhase(t *testing.T, svc *Service, phase Phase) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if svc.State().Snapshot().Phase == phase {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := svc.State().Snapshot()
	t.Fatalf("phase never became %s; got %s (problem %+v)", phase, got.Phase, got.Problem)
}

// testProfile 返回 configgen 可接受的最小节点。
func testProfile() store.Profile {
	return store.Profile{
		ID: "p1", Name: "tokyo", Protocol: store.ProtoVLESS,
		Address: "jp.example.com", Port: 443,
	}
}

// Snapshot 返回副本：修改它不会影响状态。
func TestSnapshotIsACopy(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "uuid-1"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	snap := svc.State().Snapshot()
	if len(snap.Profiles) != 1 {
		t.Fatalf("%d profiles, want 1", len(snap.Profiles))
	}
	snap.Profiles[0].Name = "mutated"
	if svc.State().Snapshot().Profiles[0].Name == "mutated" {
		t.Fatal("Snapshot shares its slice with the state")
	}
}

// 快照中的 problem 指针同样是副本。
func TestSnapshotCopiesProblem(t *testing.T) {
	svc, _ := newTestService(t)
	svc.State().SetPhase(PhaseFailed, &Problem{Code: "X", Message: "boom"})

	snap := svc.State().Snapshot()
	snap.Problem.Message = "changed"
	if svc.State().Snapshot().Problem.Message == "changed" {
		t.Fatal("Snapshot shares its Problem pointer")
	}
}

// 使用已存储的节点连接会启动引擎并报告已连接。
func TestConnectStartsEngine(t *testing.T) {
	svc, engine := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "11111111-2222-3333-4444-555555555555"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}

	svc.Connect("p1")
	waitPhase(t, svc, PhaseConnected)

	if !engine.Running() {
		t.Fatal("engine not running after Connect")
	}
	cfg := string(engine.Config())
	if !strings.Contains(cfg, "jp.example.com") {
		t.Fatalf("config does not mention the node:\n%s", cfg)
	}
}

// Connect 不会阻塞：远在引擎启动前就会返回。
func TestConnectReturnsImmediately(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}

	start := time.Now()
	svc.Connect("p1")
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Connect blocked for %v", elapsed)
	}
	waitPhase(t, svc, PhaseConnected)
}

// 未选择任何节点时连接会给出明确问题，而不是崩溃。
func TestConnectWithoutProfileFails(t *testing.T) {
	svc, _ := newTestService(t)
	svc.Connect("")
	waitPhase(t, svc, PhaseFailed)

	p := svc.State().Snapshot().Problem
	if p == nil || p.Code != "NO_PROFILE" {
		t.Fatalf("problem = %+v, want NO_PROFILE", p)
	}
}

// 引擎启动失败会以 CORE_START_FAILED 暴露，并附带原因。
func TestConnectEngineFailure(t *testing.T) {
	svc, engine := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	engine.FailStart(errors.New("boom"))

	svc.Connect("p1")
	waitPhase(t, svc, PhaseFailed)

	p := svc.State().Snapshot().Problem
	if p == nil || p.Code != "CORE_START_FAILED" {
		t.Fatalf("problem = %+v, want CORE_START_FAILED", p)
	}
	if !strings.Contains(p.Detail, "boom") {
		t.Fatalf("detail = %q, want it to carry the cause", p.Detail)
	}
}

// 节点密钥缺失时会在接触引擎之前失败。
func TestConnectWithoutSecretFails(t *testing.T) {
	svc, engine := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), ""); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}

	svc.Connect("p1")
	waitPhase(t, svc, PhaseFailed)

	if starts, _ := engine.Counts(); starts != 0 {
		t.Fatalf("engine started %d times, want 0", starts)
	}
	if p := svc.State().Snapshot().Problem; p == nil || p.Code != "SECRET_MISSING" {
		t.Fatalf("problem = %+v, want SECRET_MISSING", p)
	}
}

// Disconnect 停止引擎并清空计数器。
func TestDisconnect(t *testing.T) {
	svc, engine := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	svc.Connect("p1")
	waitPhase(t, svc, PhaseConnected)

	svc.Disconnect()
	waitPhase(t, svc, PhaseIdle)

	if engine.Running() {
		t.Fatal("engine still running after Disconnect")
	}
	if got := svc.State().Snapshot().Traffic; got != (Traffic{}) {
		t.Fatalf("traffic not cleared: %+v", got)
	}
}

// Toggle 在空闲时连接，在已连接时断开。
func TestToggle(t *testing.T) {
	svc, engine := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}

	svc.Toggle()
	waitPhase(t, svc, PhaseConnected)
	if starts, _ := engine.Counts(); starts != 1 {
		t.Fatalf("%d starts, want 1", starts)
	}

	svc.Toggle()
	waitPhase(t, svc, PhaseIdle)
	if _, stops := engine.Counts(); stops != 1 {
		t.Fatalf("%d stops, want 1", stops)
	}
}

// 导入分享链接会存储它们，并显示在快照中。
func TestImportText(t *testing.T) {
	svc, _ := newTestService(t)
	link := "vless://11111111-2222-3333-4444-555555555555@jp.example.com:443" +
		"?security=tls&sni=jp.example.com#Tokyo"

	svc.ImportText(link)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(svc.State().Snapshot().Profiles) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	snap := svc.State().Snapshot()
	if len(snap.Profiles) != 1 {
		t.Fatalf("%d profiles after import, want 1 (problem %+v)", len(snap.Profiles), snap.Problem)
	}
	if snap.Profiles[0].Name != "Tokyo" {
		t.Fatalf("name = %q, want Tokyo", snap.Profiles[0].Name)
	}

	// 密钥已写入 keeper，连接时能再次找到。
	<-svc.SetActive("")
	if err := svc.connectForTest(snap.Profiles[0].ID); err != nil {
		t.Fatalf("connect after import: %v", err)
	}
}

// 导入无效内容会报告原因，而不是静默忽略。
func TestImportJunk(t *testing.T) {
	svc, _ := newTestService(t)
	svc.ImportText("这不是链接")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := svc.State().Snapshot().ImportProblem; p != "" {
			if !strings.Contains(p, "无法识别") {
				t.Fatalf("import problem = %q, want it to say what was wrong", p)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no problem reported for junk input")
}

// 空导入会说明该怎么做，而不是静默失败。
func TestImportEmptyText(t *testing.T) {
	svc, _ := newTestService(t)
	svc.ImportText("   ")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := svc.State().Snapshot().ImportProblem; p != "" {
			if !strings.Contains(p, "粘贴") {
				t.Fatalf("import problem = %q, want an instruction", p)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("empty input was not reported")
}

// 成功导入会说明接收了多少个节点。
func TestImportReportsCount(t *testing.T) {
	svc, _ := newTestService(t)
	svc.ImportText("vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp#tokyo")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sn := svc.State().Snapshot()
		if sn.ImportAdded > 0 {
			if sn.ImportProblem != "" {
				t.Fatalf("success also reported a problem: %q", sn.ImportProblem)
			}
			if len(sn.Profiles) != sn.ImportAdded {
				t.Fatalf("imported %d but the list has %d", sn.ImportAdded, len(sn.Profiles))
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the import did not complete")
}

// 切换到 TUN 模式会在配置中生成 tun 入站。
func TestConnectInTunMode(t *testing.T) {
	svc, engine := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.SetModeForTest(string(configgen.ModeTun)); err != nil {
		t.Fatalf("SetModeForTest: %v", err)
	}

	// 服务提供有效的接口名和自动路由设置。
	svc.Connect("p1")
	waitPhase(t, svc, PhaseConnected)
	if !strings.Contains(string(engine.Config()), `"protocol": "tun"`) ||
		!strings.Contains(string(engine.Config()), `"autoOutboundsInterface": "auto"`) {
		t.Fatal("TUN defaults were not passed to the engine")
	}
}

// Load 从磁盘存储填充快照。
func TestLoadReadsStore(t *testing.T) {
	dir := t.TempDir()
	p := store.NewFile(filepath.Join(dir, "vray.json"))
	if err := p.PutProfile(testProfile()); err != nil {
		t.Fatalf("PutProfile: %v", err)
	}

	svc, _ := NewFake(dir)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	snap := svc.State().Snapshot()
	if len(snap.Profiles) != 1 || snap.Profiles[0].ID != "p1" {
		t.Fatalf("profiles = %+v", snap.Profiles)
	}
	if snap.DataRoot != dir {
		t.Fatalf("dataRoot = %q, want %q", snap.DataRoot, dir)
	}
}

// 每次更新都会递增 Version，因此视图可以低成本跳过帧。
func TestVersionIncreases(t *testing.T) {
	svc, _ := newTestService(t)
	before := svc.State().Version()
	svc.State().SetTraffic(Traffic{UpBps: 1})
	if after := svc.State().Version(); after <= before {
		t.Fatalf("version %d -> %d, want an increase", before, after)
	}
}
