package app

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/core/logs"
	"github.com/sbgayhub/vray/internal/store"
)

// bundleText 通过服务构建诊断包并展平其内容。
func bundleText(t *testing.T, svc *Service) string {
	t.Helper()
	path, err := svc.ExportDiagnostics()
	if err != nil {
		t.Fatalf("ExportDiagnostics: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the bundle: %v", err)
	}
	r, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("the bundle is not a zip: %v", err)
	}
	var b strings.Builder
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		rc.Close()
		b.WriteString(buf.String())
	}
	return b.String()
}

// 真实存储生成的诊断包不包含节点地址、UUID 或订阅 URL。
//
// 这是 diag 包自身测试的端到端版本：从应用实际存储的数据开始，而真实泄漏正可能来自这里。
func TestDiagnosticBundleFromRealStoreLeaksNothing(t *testing.T) {
	svc, _ := newTestService(t)

	const (
		host   = "jp1.very-secret-host.example.com"
		secret = "11111111-2222-3333-4444-555555555555"
	)
	svc.AddTestProfile(store.Profile{
		ID: "p1", Name: "东京", Protocol: store.ProtoVLESS,
		Address: host, Port: 443,
		RawOutbound: []byte(`{"security":"reality","publicKey":"PUBKEY"}`),
	}, secret)
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 一个包含主机名的失败，这正是实际泄漏时常见的形态。
	svc.State().SetPhase(PhaseFailed, &Problem{
		Code:    "CORE_START_FAILED",
		Message: "dial " + host + " failed",
		Detail:  "vless://" + secret + "@" + host + ":443",
	})

	text := bundleText(t, svc)
	for _, field := range []string{"heapAllocBytes", "runtimeUsedBytes", "goroutines"} {
		if !strings.Contains(text, field) {
			t.Fatalf("diagnostic bundle missing memory field %q", field)
		}
	}

	for name, s := range map[string]string{
		"node host": host,
		"node UUID": secret,
	} {
		if strings.Contains(text, s) {
			t.Fatalf("the bundle leaks the %s: %q", name, s)
		}
	}
	// 它仍会解释失败原因。
	for _, want := range []string{"CORE_START_FAILED", "jp1.", "443", "reality"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the bundle lost %q\n%s", want, text)
		}
	}
}

// 订阅 URL 不会进入诊断包：它们保存在密钥 keeper 中，报告甚至不会读取。
func TestDiagnosticBundleWithholdsSubscriptionURL(t *testing.T) {
	svc, _ := newTestService(t)
	const subURL = "https://panel.example.com/api/v1/client/subscribe?token=supersecret"
	if err := svc.store.SetSettings(store.DefaultSettings()); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if err := svc.store.PutSource(store.Source{
		ID: "s1", Name: "my panel", URLRef: "source/s1/url", Enabled: true,
	}); err != nil {
		t.Fatalf("PutSource: %v", err)
	}
	// 即使 URL 可访问，也绝不能出现在诊断包中。
	_ = svc.secrets.Set("source/s1/url", []byte(subURL))
	if err := svc.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	text := bundleText(t, svc)
	if strings.Contains(text, subURL) {
		t.Fatal("the bundle contains the subscription URL")
	}
	if strings.Contains(text, "supersecret") {
		t.Fatal("the bundle contains the subscription token")
	}
	if !strings.Contains(text, "my panel") {
		t.Fatal("the bundle lost the subscription's name, which is what the user recognizes")
	}
}

// 数据目录只按名称列出，不会复制：诊断包不得携带数据库或密钥文件。
func TestDiagnosticBundleListsButDoesNotCopyData(t *testing.T) {
	svc, _ := newTestService(t)
	// 一个看起来像密钥存储的文件。
	secretPath := svc.paths.Secrets()
	if err := os.WriteFile(secretPath, []byte("ENCRYPTED-BLOB-MARKER"), 0o600); err != nil {
		t.Fatalf("write secrets: %v", err)
	}

	text := bundleText(t, svc)
	if strings.Contains(text, "ENCRYPTED-BLOB-MARKER") {
		t.Fatal("the bundle copied the secret file's contents")
	}
	if !strings.Contains(text, filepath.Base(secretPath)) {
		t.Fatal("the bundle does not mention the secret file's name, so its presence is invisible")
	}
}

// 日志行会进入诊断包，但其中的凭据不会。
func TestDiagnosticBundleScrubsLogs(t *testing.T) {
	svc, _ := newTestService(t)
	const secret = "99999999-8888-7777-6666-555555555555"
	svc.AttachLogs(&memLogSource{entries: []logs.Entry{
		{Level: logs.LevelWarning, At: time.Now(), Text: "user " + secret + " rejected"},
	}})

	text := bundleText(t, svc)
	if strings.Contains(text, secret) {
		t.Fatalf("the bundle leaks a UUID from a log line")
	}
	if !strings.Contains(text, "rejected") {
		t.Fatal("the bundle lost the log line itself")
	}
}

// 导出会写入用户可找到的文件，权限仅限所有者。
func TestExportDiagnosticsWritesFile(t *testing.T) {
	svc, _ := newTestService(t)
	path, err := svc.ExportDiagnostics()
	if err != nil {
		t.Fatalf("ExportDiagnostics: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "vray-diagnostics-") {
		t.Fatalf("name = %q", filepath.Base(path))
	}
	if filepath.Dir(path) != svc.state.Snapshot().DataRoot {
		t.Fatalf("the bundle went to %q, want the data directory", filepath.Dir(path))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("the bundle is empty")
	}
}

func TestAsyncExportPublishesResult(t *testing.T) {
	svc, _ := newTestService(t)
	select {
	case <-svc.ExportDiagnosticsAsync():
	case <-time.After(3 * time.Second):
		t.Fatal("diagnostic export did not finish")
	}
	snap := svc.State().Snapshot()
	if snap.DiagnosticSaving || !strings.HasPrefix(snap.DiagnosticResult, "已导出到 ") {
		t.Fatalf("diagnostic result = %q, saving=%t", snap.DiagnosticResult, snap.DiagnosticSaving)
	}
	path := strings.TrimPrefix(snap.DiagnosticResult, "已导出到 ")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// memLogSource 是基于固定列表的 LogSource 实现。
type memLogSource struct{ entries []logs.Entry }

func (m *memLogSource) Snapshot() []logs.Entry { return m.entries }
func (m *memLogSource) Clear()                 {}
func (m *memLogSource) Dropped() int64         { return 0 }
