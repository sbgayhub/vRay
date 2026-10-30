package app

import (
	"errors"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/core/effects"
	"github.com/sbgayhub/vray/internal/sysproxy"
)

// fakeProxy 是 sysproxy.Backend，记录调用而不修改机器设置。
type fakeProxy struct {
	state    sysproxy.Previous
	enabled  []sysproxy.Settings
	restored []sysproxy.Previous
	failOn   string // "enable" 或 "restore"，用于让对应调用失败
}

func (f *fakeProxy) Enable(s sysproxy.Settings) (sysproxy.Previous, error) {
	if f.failOn == "enable" {
		return f.state, errors.New("access denied")
	}
	prev := f.state
	f.enabled = append(f.enabled, s)
	f.state = sysproxy.Previous{Enabled: true, Address: "127.0.0.1:1", Bypass: s.Bypass}
	return prev, nil
}

func (f *fakeProxy) Restore(p sysproxy.Previous) error {
	if f.failOn == "restore" {
		return errors.New("access denied")
	}
	f.restored = append(f.restored, p)
	f.state = p
	return nil
}

func (f *fakeProxy) Current() (sysproxy.Previous, error) { return f.state, nil }

// newProxyService 将服务连接到假代理后端。
func newProxyService(t *testing.T) (*Service, *fakeProxy) {
	t.Helper()
	svc, _ := newTestService(t)
	fp := &fakeProxy{state: sysproxy.Previous{Enabled: false, Address: "", Bypass: "old"}}
	svc.AttachEffectLayers(NewProxyLayer(fp), nil)
	return svc, fp
}

// 应用系统代理会记录恢复点，以便还原旧状态。
//
// 备份正是日志值得写入的原因：没有它，崩溃后会留下已配置的代理，却没有任何组件知道该恢复什么。
func TestApplySystemProxyJournalsBackup(t *testing.T) {
	svc, fp := newProxyService(t)
	fp.state = sysproxy.Previous{Enabled: true, Address: "corp:3128", Bypass: "corp.local"}

	if err := svc.ApplySystemProxy("127.0.0.1", 2080); err != nil {
		t.Fatalf("ApplySystemProxy: %v", err)
	}
	if len(fp.enabled) != 1 || fp.enabled[0].Port != 2080 {
		t.Fatalf("backend calls = %+v", fp.enabled)
	}

	entries := svc.Effects().Entries()
	if len(entries) != 1 {
		t.Fatalf("journal = %+v, want one entry", entries)
	}
	e := entries[0]
	if e.Kind != effects.KindSystemProxy {
		t.Fatalf("kind = %q", e.Kind)
	}
	if e.Target != "127.0.0.1:2080" {
		t.Fatalf("target = %q", e.Target)
	}
	if e.Backup == "" {
		t.Fatal("no backup was recorded, so a crash could not restore anything")
	}

	// 恢复操作会重放该备份。
	if err := svc.RestoreEffects(); err != nil {
		t.Fatalf("RestoreEffects: %v", err)
	}
	if len(fp.restored) != 1 {
		t.Fatalf("restored = %+v", fp.restored)
	}
	if !fp.restored[0].Enabled || fp.restored[0].Address != "corp:3128" {
		t.Fatalf("restored state = %+v, want the corporate proxy back", fp.restored[0])
	}
	if svc.Effects().Pending() {
		t.Fatal("the journal still has entries after a restore")
	}
}

// 后端拒绝时不会留下日志条目，因此不会重试从未应用成功的操作。
func TestApplySystemProxyFailureLeavesNoEntry(t *testing.T) {
	svc, fp := newProxyService(t)
	fp.failOn = "enable"

	if err := svc.ApplySystemProxy("127.0.0.1", 2080); err == nil {
		t.Fatal("a backend failure was reported as success")
	}
	if len(svc.Effects().Entries()) != 0 {
		t.Fatalf("journal = %+v, want nothing", svc.Effects().Entries())
	}
}

// 恢复失败会保留条目，供下次启动继续处理。
func TestRestoreSystemProxyFailureIsKept(t *testing.T) {
	svc, fp := newProxyService(t)
	if err := svc.ApplySystemProxy("127.0.0.1", 2080); err != nil {
		t.Fatalf("ApplySystemProxy: %v", err)
	}
	fp.failOn = "restore"
	if err := svc.RestoreEffects(); err == nil {
		t.Fatal("a failed restore was reported as success")
	}
	if !svc.Effects().Pending() {
		t.Fatal("the failed entry was dropped")
	}
}

// 启用状态从后端读取，而不是记住，因此用户手动关闭代理后不会被误报为仍开启。
func TestSystemProxyEnabledReadsBackend(t *testing.T) {
	svc, fp := newProxyService(t)
	if svc.SystemProxyEnabled() {
		t.Fatal("reported enabled before anything was applied")
	}
	if err := svc.ApplySystemProxy("127.0.0.1", 2080); err != nil {
		t.Fatalf("ApplySystemProxy: %v", err)
	}
	if !svc.SystemProxyEnabled() {
		t.Fatal("reported disabled after ApplySystemProxy")
	}
	// 用户在 Windows 设置中关闭它。
	fp.state = sysproxy.Previous{Enabled: false}
	if svc.SystemProxyEnabled() {
		t.Fatal("reported enabled after the user disabled it")
	}
}

// 未挂载平台层时，应用操作是空操作而不是 panic。
func TestApplySystemProxyWithoutLayer(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.ApplySystemProxy("127.0.0.1", 2080); err != nil {
		t.Fatalf("ApplySystemProxy with no layer: %v", err)
	}
	if svc.SystemProxyEnabled() {
		t.Fatal("reported enabled with no layer")
	}
}

// 在系统代理模式下连接不会在服务锁上死锁。
//
// connectLocked 已经持有该锁，因此代理步骤必须使用加锁版本。此测试会以挂起失败（由包的 -timeout 杀死），而不是断言失败，这正是该 bug 的表现。
func TestConnectWithSystemProxyDoesNotDeadlock(t *testing.T) {
	svc, fp := newProxyService(t)
	if err := svc.SetModeForTest("system-proxy"); err != nil {
		t.Fatalf("SetModeForTest: %v", err)
	}
	if err := svc.AddTestProfile(testProfile(), "uuid"); err != nil {
		t.Fatalf("AddTestProfile: %v", err)
	}
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- svc.connectForTest("p1") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connect deadlocked; the proxy step took the service lock again")
	}
	if len(fp.enabled) != 1 {
		t.Fatalf("the system proxy was not applied: %+v", fp.enabled)
	}
}

// 登录项日志条目不是代理变更，绝不能传给代理后端。
func TestProxyLayerIgnoresLoginItemEntry(t *testing.T) {
	fp := &fakeProxy{}
	layer := NewProxyLayer(fp)
	err := layer.Restore(effects.Entry{Kind: effects.KindSystemProxy, Target: "login-item"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(fp.restored) != 0 {
		t.Fatalf("the proxy backend was called: %+v", fp.restored)
	}
}
