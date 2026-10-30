package app

import (
	"encoding/json"
	"testing"

	"github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/paths"
	"github.com/sbgayhub/vray/internal/store"
)

// 真实核心会接受 hysteria2 节点。
//
// 有用户报告：hysteria2 节点导入正常，但连接时提示“不支持的协议”。生成器只识别 vless/vmess/trojan/shadowsocks，因此协议检查在构建任何配置之前就拒绝了它。本测试用 hysteria2 outbound 启动真实 Xray，这是验证配置结构正确的唯一证据：传输层从 streamSettings 读取认证信息，而密码放错位置的配置仍然能解析。
func TestRealEngineStartsHysteria2Node(t *testing.T) {
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
		"name":"hy","protocol":"hysteria2","address":"127.0.0.1","port":1,
		"secret":"hy2-password","security":"tls","sni":"hy.example.com"
	}`)
	if err := svc.AddTestProfile(store.Profile{
		ID: "hy", Name: "hy", Protocol: store.ProtoHysteria2,
		Address: "127.0.0.1", Port: 1, RawOutbound: raw,
	}, "hy2-password"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	svc.SetAPIPort(apiPort)

	if err := svc.connectForTest("hy"); err != nil {
		t.Fatalf("the real core rejected the hysteria2 node: %v (problem %+v)",
			err, st.Snapshot().Problem)
	}
	if !engine.Running() {
		t.Fatal("engine not running after a successful connect")
	}
	if !waitListening(t, mixedPort) {
		t.Fatalf("mixed inbound never listened on %d", mixedPort)
	}
}
