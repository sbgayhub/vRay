package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 编辑后的节点会将其传输配置完整保留到生成的配置中。
//
// 存储层保留列表展示的字段，而传输配置只存在于 RawOutbound。若保存时从列数据重建 outbound，REALITY-over-WebSocket 节点会退化成普通 TCP，因此配置文本本身就是断言：它仍必须包含网络、安全方式和密钥。
func TestEditedNodeKeepsTransportInGeneratedConfig(t *testing.T) {
	svc, eng := newTestService(t)
	p := testProfile()
	p.Name = "旧名"
	p.RawOutbound = json.RawMessage(`{"name":"旧名","protocol":"vless","address":"` + p.Address +
		`","port":443,"secret":"uuid","network":"ws","path":"/ws","host":"cdn.example.com",` +
		`"security":"reality","sni":"www.microsoft.com","fingerprint":"chrome","publicKey":"PUBKEY",` +
		`"shortId":"abcd","flow":"xtls-rprx-vision"}`)
	if err := svc.AddTestProfile(p, "uuid"); err != nil {
		t.Fatalf("add: %v", err)
	}

	svc.SaveProfile(ProfileEdit{ID: p.ID, Name: "新名", Address: "new.example.com", Port: 8443})
	waitProfileName(t, svc, "新名")

	svc.Connect(p.ID)
	waitPhase(t, svc, PhaseConnected)

	cfg := string(eng.Config())
	for _, want := range []string{
		`"address": "new.example.com"`, // 编辑已生效
		`"network": "ws"`,
		`"security": "reality"`,
		`"publicKey": "PUBKEY"`,
		`"path": "/ws"`,
		`"flow": "xtls-rprx-vision"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("generated config lost %s\n%s", want, cfg)
		}
	}
}

func waitProfiles(t *testing.T, svc *Service, n int) []ProfileView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := svc.State().Snapshot().Profiles; len(got) == n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("profiles = %d, want %d", len(svc.State().Snapshot().Profiles), n)
	return nil
}

// waitProfileName 轮询直到节点使用预期名称。
//
// 只数行数不足以检测保存：该行本来就在，因此基于计数的等待会在写入落地前返回，测试无法证明任何事。
func waitProfileName(t *testing.T, svc *Service, name string) ProfileView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range svc.State().Snapshot().Profiles {
			if p.Name == name {
				return p
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no profile named %q: %+v", name, svc.State().Snapshot().Profiles)
	return ProfileView{}
}

// 保存节点会修改可编辑字段，其余字段保持不变。
//
// 协议和密钥来自分享链接；若表单重写它们，可能会在无提示的情况下破坏可用节点。
func TestSaveProfileEditsOnlyEditableFields(t *testing.T) {
	svc, _ := newTestService(t)
	p := testProfile()
	if err := svc.AddTestProfile(p, "uuid"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	svc.SaveProfile(ProfileEdit{
		ID:       p.ID,
		Name:     "renamed",
		Address:  "new.example.com",
		Port:     8443,
		Favorite: true,
	})
	// 只等待计数不够：列表本来就只有一行，测试会在保存落地前通过。应等待值本身。
	got := waitProfileName(t, svc, "renamed")

	if got.Name != "renamed" || got.Address != "new.example.com" || got.Port != 8443 {
		t.Fatalf("editable fields not applied: %+v", got)
	}
	if !got.Favorite {
		t.Fatal("favorite not applied")
	}
	if got.Protocol != string(p.Protocol) {
		t.Fatalf("protocol changed to %q", got.Protocol)
	}

	// 密钥仍必须可读：连接依赖它。
	d, err := svc.store.Load()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	if len(d.Profiles) != 1 || d.Profiles[0].SecretRef == "" {
		t.Fatal("the secret reference was lost")
	}
	if _, err := svc.secretOf(d.Profiles[0]); err != nil {
		t.Fatalf("the secret is unreadable after saving: %v", err)
	}
}

// 保存未知节点会报告错误，而不是创建幽灵行。
func TestSaveUnknownProfile(t *testing.T) {
	svc, _ := newTestService(t)
	svc.SaveProfile(ProfileEdit{ID: "nope", Name: "x"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := svc.State().Snapshot().Problem; p != nil && p.Code == "NO_PROFILE" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("saving an unknown profile was not reported")
}

// 删除节点会同时删除其密钥：遗留密钥就是泄漏。
func TestDeleteProfilesRemovesSecret(t *testing.T) {
	svc, _ := newTestService(t)
	p := testProfile()
	if err := svc.AddTestProfile(p, "uuid"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	d, err := svc.store.Load()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	ref := d.Profiles[0].SecretRef
	if ref == "" {
		t.Fatal("the test profile has no secret to check")
	}

	svc.DeleteProfiles([]string{p.ID})
	waitProfiles(t, svc, 0)

	if _, err := svc.secrets.Get(ref); err == nil {
		t.Fatal("the secret survived the delete")
	}
}

// 一次删除多个节点可用，未知 ID 会被忽略。
func TestDeleteSeveralProfiles(t *testing.T) {
	svc, _ := newTestService(t)
	for _, id := range []string{"a", "b", "c"} {
		p := testProfile()
		p.ID = id
		p.Name = id
		if err := svc.AddTestProfile(p, "uuid"); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	waitProfiles(t, svc, 3)

	svc.DeleteProfiles([]string{"a", "c", "unknown"})
	got := waitProfiles(t, svc, 1)
	if got[0].ID != "b" {
		t.Fatalf("survivor = %q, want b", got[0].ID)
	}
}

// 存储问题会被报告，但不会假装隧道已断开：用户编辑节点列表时引擎可能仍在运行。
func TestSaveProblemKeepsPhase(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.AddTestProfile(testProfile(), "uuid"); err != nil {
		t.Fatalf("add: %v", err)
	}
	svc.State().SetPhase(PhaseConnected, nil)

	svc.SaveProfile(ProfileEdit{ID: "missing", Name: "x"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sn := svc.State().Snapshot()
		if sn.Problem != nil {
			if sn.Phase != PhaseConnected {
				t.Fatalf("phase = %s, want it to stay connected", sn.Phase)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the problem was not recorded")
}
