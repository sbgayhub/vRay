package app

import (
	"errors"
	"net"
	"strconv"
	"testing"
	"time"
)

// reservePort 在测试期间保持端口打开并返回它，以便需要占用端口的测试使用。
func reservePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// itoa 是本地保留的 strconv.Itoa 封装，便于测试中直接调用。
func itoa(n int) string { return strconv.Itoa(n) }

// waitSettings 轮询直到存储的设置满足 predicate。
func waitSettings(t *testing.T, svc *Service, ok func(SettingsView) bool) SettingsView {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := svc.State().Snapshot().Settings; ok(got) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("settings = %+v", svc.State().Snapshot().Settings)
	return SettingsView{}
}

// 保存设置会写入它们，快照会反映结果。
func TestSaveSettingsPersists(t *testing.T) {
	svc, _ := newTestService(t)
	v := svc.State().Snapshot().Settings
	v.MixedPort = 12345
	v.LogLevel = "info"
	v.ProbeURL = "https://example.com/generate_204"
	v.AutoReconnect = false
	svc.SaveSettings(v)

	got := waitSettings(t, svc, func(s SettingsView) bool { return s.MixedPort == 12345 })
	if got.LogLevel != "info" || got.ProbeURL != "https://example.com/generate_204" || got.AutoReconnect {
		t.Fatalf("settings = %+v", got)
	}
}

func TestAutomaticUpdatePreferencePersists(t *testing.T) {
	svc, _ := newTestService(t)
	view := svc.State().Snapshot().Settings
	view.AutoUpdateChecks = false
	select {
	case <-svc.SaveSettings(view):
	case <-time.After(3 * time.Second):
		t.Fatal("settings save did not finish")
	}

	d, err := svc.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if d.Settings.AutoUpdateChecks {
		t.Fatal("disabled daily update checks were not persisted")
	}
	if svc.State().Snapshot().Settings.AutoUpdateChecks {
		t.Fatal("snapshot still shows daily update checks enabled")
	}
}

// 超出范围的端口会被拒绝而不是截断：静默改变用户输入会让代理监听在意料之外的位置。
func TestSaveSettingsRefusesBadPort(t *testing.T) {
	svc, _ := newTestService(t)
	before := svc.State().Snapshot().Settings

	v := before
	v.MixedPort = 70000
	svc.SaveSettings(v)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p := svc.State().Snapshot().Problem; p != nil && p.Code == "SETTINGS_INVALID" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if p := svc.State().Snapshot().Problem; p == nil || p.Code != "SETTINGS_INVALID" {
		t.Fatalf("problem = %+v, want SETTINGS_INVALID", p)
	}
	if got := svc.State().Snapshot().Settings.MixedPort; got != before.MixedPort {
		t.Fatalf("port changed to %d despite the refusal", got)
	}
}

// 未知模式或日志级别会被拒绝：两者都会进入生成的配置，存储它们只会把失败推迟到连接时。
func TestSaveSettingsRefusesBadEnums(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*SettingsView)
	}{
		{"mode", func(v *SettingsView) { v.Mode = "nonsense" }},
		{"log level", func(v *SettingsView) { v.LogLevel = "verbose" }},
		{"probe url", func(v *SettingsView) { v.ProbeURL = "ftp://example.com" }},
		{"missing probe host", func(v *SettingsView) { v.ProbeURL = "https://?missing-host" }},
		{"theme", func(v *SettingsView) { v.Theme = "unknown" }},
		{"material", func(v *SettingsView) { v.Material = "unknown" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			v := svc.State().Snapshot().Settings
			tc.apply(&v)
			svc.SaveSettings(v)

			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if p := svc.State().Snapshot().Problem; p != nil && p.Code == "SETTINGS_INVALID" {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Fatalf("problem = %+v, want SETTINGS_INVALID", svc.State().Snapshot().Problem)
		})
	}
}

func TestAppearanceSettingsSurviveReload(t *testing.T) {
	for _, material := range []string{"none", "mica", "micaAlt", "acrylic", "liquidGlass"} {
		t.Run(material, func(t *testing.T) {
			svc, _ := newTestService(t)
			v := svc.State().Snapshot().Settings
			v.Theme, v.Material = "dark", material
			select {
			case <-svc.SaveSettings(v):
			case <-time.After(3 * time.Second):
				t.Fatal("settings save did not finish")
			}
			if err := svc.Load(); err != nil {
				t.Fatal(err)
			}
			got := svc.State().Snapshot()
			if got.Settings.Theme != "dark" || got.Settings.Material != material || got.SettingsSaving || got.SettingsError != "" {
				t.Fatalf("saved preferences = %+v, saving=%t, error=%q", got.Settings, got.SettingsSaving, got.SettingsError)
			}
		})
	}
}

func TestGeneralSettingsPreserveTitlebarAppearance(t *testing.T) {
	svc, _ := newTestService(t)
	value := svc.State().Snapshot().Settings
	value.Theme, value.Material, value.Accent = "", "", ""
	<-svc.SaveAppearance("dark", "acrylic", "system")
	value.MixedPort = 34567
	<-svc.SaveSettings(value)
	got := svc.State().Snapshot().Settings
	if got.Theme != "dark" || got.Material != "acrylic" || got.MixedPort != 34567 {
		t.Fatalf("settings overwrote appearance: %+v", got)
	}
}

func TestSaveSettingsAppliesLoginPreference(t *testing.T) {
	for _, fails := range []bool{false, true} {
		name := "success"
		if fails {
			name = "platform failure"
		}
		t.Run(name, func(t *testing.T) {
			svc, _ := newTestService(t)
			called := false
			svc.AttachLoginSetter(func(enabled bool) error {
				called = enabled
				if fails {
					return errors.New("platform refused")
				}
				return nil
			})
			v := svc.State().Snapshot().Settings
			v.OpenAtLogin = true
			<-svc.SaveSettings(v)
			snap := svc.State().Snapshot()
			if !called || snap.Settings.OpenAtLogin == fails {
				t.Fatalf("called=%t stored=%t fails=%t", called, snap.Settings.OpenAtLogin, fails)
			}
			if fails && snap.SettingsError == "" {
				t.Fatal("platform failure was hidden")
			}
		})
	}
}

// 有效端口仍可保存。
func TestSaveSettingsAcceptsPortRange(t *testing.T) {
	for _, port := range []int{1, 65535, 2080} {
		svc, _ := newTestService(t)
		v := svc.State().Snapshot().Settings
		v.MixedPort = port
		<-svc.SaveSettings(v)
		waitSettings(t, svc, func(s SettingsView) bool { return s.MixedPort == port })
	}
}

// 自检始终会给出平台和数据目录，且绝不会返回零行。
func TestSelfCheckHasTheBasics(t *testing.T) {
	svc, _ := newTestService(t)
	checks := svc.SelfCheck()
	if len(checks) < 3 {
		t.Fatalf("checks = %d (%+v), want at least 3", len(checks), checks)
	}
	var sawPlatform, sawData, sawTun bool
	for _, c := range checks {
		if c.Name == "" {
			t.Fatalf("a check has no name: %+v", c)
		}
		switch c.State {
		case CheckOK, CheckWarn, CheckFail:
		default:
			t.Fatalf("check %q has state %q", c.Name, c.State)
		}
		switch c.Name {
		case "运行平台":
			sawPlatform = true
			if c.Detail == "" {
				t.Fatal("the platform check has no detail")
			}
		case "数据目录":
			sawData = true
		case "TUN 模式":
			sawTun = true
		}
	}
	if !sawPlatform || !sawData || !sawTun {
		t.Fatalf("missing checks: platform=%v data=%v tun=%v", sawPlatform, sawData, sawTun)
	}
}

// 端口被占用会报告为警告，因此连接失败不会令人意外。
func TestSelfCheckFlagsBusyPort(t *testing.T) {
	svc, _ := newTestService(t)
	port := reservePort(t)
	v := svc.State().Snapshot().Settings
	v.MixedPort = port
	svc.SaveSettings(v)
	waitSettings(t, svc, func(s SettingsView) bool { return s.MixedPort == port })

	checks := svc.SelfCheck()
	for _, c := range checks {
		if c.Name == "本地端口 "+itoa(port) {
			if c.State != CheckWarn {
				t.Fatalf("busy port check = %+v, want warn", c)
			}
			return
		}
	}
	t.Fatalf("no check for port %d: %+v", port, checks)
}

// portInUse 的结果与实际监听器一致。
func TestPortInUse(t *testing.T) {
	port := reservePort(t)
	if !portInUse(port) {
		t.Fatalf("port %d is bound but reported free", port)
	}
	if portInUse(0) || portInUse(99999) {
		t.Fatal("an invalid port was reported in use")
	}
}
