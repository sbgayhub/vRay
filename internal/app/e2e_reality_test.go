package app

import (
	"encoding/json"
	"testing"

	"github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/store"
)

// 规则会进入配置，且真实核心会接受它们。
//
// Xray 在启动时校验路由：不支持的进程匹配器，或没有有效字段的规则，都会导致启动失败。仅构建配置不足以证明，启动核心才算。
func TestRealEngineStartsWithRules(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{Root: dir}
	if err := p.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	mixedPort := freePort(t)
	apiPort := freePort(t)

	st := NewState(Snapshot{Phase: PhaseIdle, Mode: "system-proxy", DataRoot: dir})
	storer := store.NewFile(p.DB())
	set := store.DefaultSettings()
	set.MixedPort = mixedPort
	if err := storer.SetSettings(set); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if err := storer.SetRules([]store.Rule{
		{ID: "lan", Name: "私网直连", Enabled: true, IPs: []string{"10.0.0.0/8"}, Outbound: "direct"},
		{ID: "ads", Name: "拦截", Enabled: true, Domains: []string{"domain:ads.example.com"}, Outbound: "block"},
		{
			ID: "work", Name: "公司内网", Enabled: true,
			Domains:  []string{"corp.example.com"},
			IPs:      []string{"192.168.0.0/16"},
			Ports:    "80,443,8000-8100",
			Process:  []string{"chrome.exe"},
			Outbound: "direct",
		},
		// 禁用规则绝不能进入核心。
		{ID: "off", Name: "停用的", Enabled: false, IPs: []string{"bogus"}, Outbound: "direct"},
	}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	engine := core.New()
	defer func() { _ = engine.Stop() }()
	svc := New(st, storer, NewMemSecrets(), engine, p)

	if err := svc.AddTestProfile(store.Profile{
		ID: "local", Name: "local", Protocol: store.ProtoVLESS,
		Address: "127.0.0.1", Port: 1,
	}, "11111111-2222-3333-4444-555555555555"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc.SetAPIPort(apiPort)

	if err := svc.connectForTest("local"); err != nil {
		t.Fatalf("the real core rejected the rules: %v (problem %+v)",
			err, st.Snapshot().Problem)
	}
	if !waitListening(t, mixedPort) {
		t.Fatalf("mixed inbound never listened on %d", mixedPort)
	}

	if got := len(st.Snapshot().Rules); got != 4 {
		t.Fatalf("rule views = %d, want 4 (all stored rules are listed)", got)
	}
}

// REALITY 节点必须能启动真实核心，而不只是生成看起来正确的文本。
//
// 这是导入修复所涉及完整链路的末端：分享链接 → 存储 → outbound → 配置 → Xray。丢失传输层的配置仍是合法 JSON，也仍能通过 configgen，但核心会拒绝没有公钥的 REALITY outbound；因此成功启动就证明传输层通过了每一层。
//
// 使用 gRPC 而非 WebSocket，因为 Xray 本身拒绝 REALITY over WS（"REALITY only supports RAW, XHTTP and gRPC for now"）；否则 WS 节点会因与本次修复无关的原因失败。
func TestRealEngineStartsRealityGRPCNode(t *testing.T) {
	dir := t.TempDir()
	p := paths.Paths{Root: dir}
	if err := p.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	mixedPort := freePort(t)
	apiPort := freePort(t)

	st := NewState(Snapshot{Phase: PhaseIdle, Mode: "system-proxy", DataRoot: dir})
	storer := store.NewFile(p.DB())
	set := store.DefaultSettings()
	set.MixedPort = mixedPort
	if err := storer.SetSettings(set); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	engine := core.New()
	defer func() { _ = engine.Stop() }()
	svc := New(st, storer, NewMemSecrets(), engine, p)

	raw := json.RawMessage(`{
		"name":"jp","protocol":"vless","address":"127.0.0.1","port":1,
		"secret":"11111111-2222-3333-4444-555555555555",
		"network":"grpc","serviceName":"mygrpc",
		"security":"reality","sni":"www.microsoft.com","fingerprint":"chrome",
		"publicKey":"jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0","shortId":"abcd",
		"flow":"xtls-rprx-vision"
	}`)
	if err := svc.AddTestProfile(store.Profile{
		ID: "jp", Name: "jp", Protocol: store.ProtoVLESS,
		// 有意设为不可达：只有生成的 outbound 重要，且无论上游是否可用，mixed 入站都会启动。
		Address: "127.0.0.1", Port: 1, RawOutbound: raw,
	}, "11111111-2222-3333-4444-555555555555"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc.SetAPIPort(apiPort)

	if err := svc.connectForTest("jp"); err != nil {
		t.Fatalf("real core rejected the REALITY outbound: %v (problem %+v)",
			err, st.Snapshot().Problem)
	}
	if !engine.Running() {
		t.Fatal("engine not running after a successful connect")
	}
	if !waitListening(t, mixedPort) {
		t.Fatalf("mixed inbound never listened on %d", mixedPort)
	}
}
