//go:build windows

package sysproxy

// 使用真实注册表，并在结束后恢复用户的实际设置。
//
// 这是唯一会写入机器实时代理配置的测试。它通过 VRAY_LIVE_REGISTRY=1 显式
// 启用，因为会修改仓库外的状态；测试会精确恢复读取到的值，包括原本已经
// 配置代理的情况，而这正是关键场景。

import (
	"os"
	"testing"
)

func liveRegistry(t *testing.T) {
	t.Helper()
	if os.Getenv("VRAY_LIVE_REGISTRY") != "1" {
		t.Skip("set VRAY_LIVE_REGISTRY=1 to exercise the live registry")
	}
}

// 在实时注册表键上启用并恢复后，用户设置会完全保持原样。
func TestLiveRegistryRoundTrip(t *testing.T) {
	liveRegistry(t)

	b := New()
	before, err := b.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	t.Logf("before: enabled=%v address=%q bypass=%q", before.Enabled, before.Address, before.Bypass)

	// 无论下面发生什么，都要恢复用户设置。
	defer func() {
		if err := b.Restore(before); err != nil {
			t.Fatalf("restoring the original settings: %v", err)
		}
		after, err := b.Current()
		if err != nil {
			t.Fatalf("Current after restore: %v", err)
		}
		if after.Enabled != before.Enabled || after.Address != before.Address || after.Bypass != before.Bypass {
			t.Fatalf("settings not restored: before %+v, after %+v", before, after)
		}
		t.Logf("restored: enabled=%v address=%q bypass=%q", after.Enabled, after.Address, after.Bypass)
	}()

	if _, err := b.Enable(Settings{Host: "127.0.0.1", Port: 2080}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	cur, err := b.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if !cur.Enabled {
		t.Fatal("the live key is not enabled after Enable")
	}
	if cur.Address != "127.0.0.1:2080" {
		t.Fatalf("address = %q", cur.Address)
	}
	if cur.Bypass != BypassDefault {
		t.Fatalf("bypass = %q", cur.Bypass)
	}
}
