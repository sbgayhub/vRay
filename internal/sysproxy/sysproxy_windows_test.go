//go:build windows

package sysproxy

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

var (
	procRegCreateKey = advapi32.NewProc("RegCreateKeyExW")
	procRegDeleteKey = advapi32.NewProc("RegDeleteKeyW")
)

// createKey 在 HKCU 下创建 path，包括所有中间键。
func createKey(path string) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var h syscall.Handle
	var disposition uint32
	ret, _, _ := procRegCreateKey.Call(
		hkeyCurrentUser,
		uintptr(unsafe.Pointer(p)),
		0, 0, 0,
		uintptr(keyWrite),
		0,
		uintptr(unsafe.Pointer(&h)),
		uintptr(unsafe.Pointer(&disposition)),
	)
	if ret != 0 {
		return fmt.Errorf("sysproxy: RegCreateKeyEx %s: %w", path, syscall.Errno(ret))
	}
	closeKey(h)
	return nil
}

// deleteKey 删除一个不含子键的键。
func deleteKey(path string) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	ret, _, _ := procRegDeleteKey.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(p)))
	switch syscall.Errno(ret) {
	case 0, 2:
		return nil
	default:
		return fmt.Errorf("sysproxy: RegDeleteKey %s: %w", path, syscall.Errno(ret))
	}
}

// scratchKey 在 HKCU 下为测试创建注册表键，并在结束后删除。
//
// 这里使用真实键而不是伪造对象：本文件的重点就是验证 Windows 调用确实
// 执行了其声称的操作；如果测试从不接触注册表，即使 API 用错也会通过。
// HKCU 不需要管理员权限，因此普通开发者账号也能运行。
func scratchKey(t *testing.T) string {
	t.Helper()
	name := `Software\vRay-test\sysproxy-` + strings.ReplaceAll(t.Name(), "/", "-")
	if err := createKey(name); err != nil {
		t.Fatalf("createKey: %v", err)
	}
	t.Cleanup(func() {
		if err := deleteKey(name); err != nil {
			t.Logf("removing the scratch key: %v", err)
		}
	})
	return name
}

// 往返测试：启用时写入应用期望的值，恢复时精确还原之前的值。
func TestWininetRoundTrip(t *testing.T) {
	b := &wininet{Key: scratchKey(t)}

	// 准备一个用于恢复的状态：代理已经配置。
	if err := b.writeString(valProxyServer, "old.example.com:1080"); err != nil {
		t.Fatalf("seed server: %v", err)
	}
	if err := b.writeString(valProxyBypass, "old-bypass"); err != nil {
		t.Fatalf("seed bypass: %v", err)
	}

	prev, err := b.Enable(Settings{Host: "127.0.0.1", Port: 2080})
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !prev.Enabled {
		// ProxyEnable 从未设置，因此即使存有地址，之前的状态仍是“关闭”。
		// 这正是恢复操作必须重现的状态。
		t.Log("previous state was disabled, as seeded")
	}
	if prev.Address != "old.example.com:1080" {
		t.Fatalf("previous address = %q", prev.Address)
	}

	cur, err := b.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if !cur.Enabled {
		t.Fatal("the proxy is not enabled after Enable")
	}
	if cur.Address != "127.0.0.1:2080" {
		t.Fatalf("address = %q, want 127.0.0.1:2080", cur.Address)
	}
	if cur.Bypass != BypassDefault {
		t.Fatalf("bypass = %q, want the default list", cur.Bypass)
	}

	// 恢复操作会把旧值写回。
	if err := b.Restore(prev); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	back, err := b.Current()
	if err != nil {
		t.Fatalf("Current after restore: %v", err)
	}
	if back.Enabled != prev.Enabled {
		t.Fatalf("enabled = %v, want %v", back.Enabled, prev.Enabled)
	}
	if back.Address != "old.example.com:1080" {
		t.Fatalf("address after restore = %q", back.Address)
	}
	if back.Bypass != "old-bypass" {
		t.Fatalf("bypass after restore = %q", back.Bypass)
	}
}

// 恢复一个没有代理的状态时会删除这些值，而不是留下空字符串，否则 Windows
// 会将其解读为没有地址的代理。
func TestWininetRestoreToCleanState(t *testing.T) {
	b := &wininet{Key: scratchKey(t)}

	prev, err := b.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if _, err := b.Enable(Settings{Host: "127.0.0.1", Port: 2080}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := b.Restore(prev); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	cur, err := b.Current()
	if err != nil {
		t.Fatalf("Current after restore: %v", err)
	}
	if cur.Enabled {
		t.Fatal("the proxy is still enabled after restoring a clean state")
	}
	if cur.Address != "" || cur.Bypass != "" {
		t.Fatalf("values left behind: %+v", cur)
	}
}

// 自定义绕过列表会被采用，而不会被默认值替换。
func TestWininetCustomBypass(t *testing.T) {
	b := &wininet{Key: scratchKey(t)}
	if _, err := b.Enable(Settings{Host: "127.0.0.1", Port: 2080, Bypass: "corp.local"}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	cur, err := b.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if cur.Bypass != "corp.local" {
		t.Fatalf("bypass = %q, want corp.local", cur.Bypass)
	}
}

// 默认绕过列表会让本地流量不经过代理：代理 127.0.0.1 或局域网地址会形成
// 回环或破坏本地访问。
func TestBypassDefaultCoversLocal(t *testing.T) {
	for _, want := range []string{"127.*", "10.*", "192.168.*", "<local>"} {
		if !strings.Contains(BypassDefault, want) {
			t.Fatalf("the default bypass does not cover %q: %s", want, BypassDefault)
		}
	}
}
