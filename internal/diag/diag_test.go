package diag

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// 报告中预置的已知凭据。若任何一项进入诊断包，测试会指出具体是哪一项。
const (
	secretUUID = "11111111-2222-3333-4444-555555555555"
	secretPass = "hunter2-SECRET"
	secretHost = "jp1.secret-provider.example.com"
	secretSub  = "https://panel.example.com/api/v1/client/subscribe?token=deadbeefcafe1234567890"
	secretBody = "dmxlc3M6Ly8xMTExMTExMS0yMjIyLTMzMzMtNDQ0NC01NTU1NTU1NTU1NTVAanAxLnNlY3JldC1wcm92aWRlci5leGFtcGxlLmNvbTo0NDM"
)

// reportWithSecrets 返回应用在脱敏前会构建的报告：每个字段都保存真实值。
func reportWithSecrets() Report {
	return Report{
		App:      "0.1.0",
		Platform: "windows/amd64 go1.27.3",
		Phase:    "failed",
		Mode:     "system-proxy",
		GeoState: "ready",
		Problem: &Problem{
			Code:    "CORE_START_FAILED",
			Message: "dial " + secretHost + " failed",
			Detail:  "vless://" + secretUUID + "@" + secretHost + ":443?security=reality&pbk=KEY",
		},
		Nodes: []NodeSummary{
			{Protocol: "vless", Address: secretHost, Port: 443, Security: "reality", Source: "link"},
		},
		Sources: []SourceSummary{
			{Name: "my panel", Enabled: true, URL: secretSub, NodeCount: 12},
		},
		Checks: []Check{
			{Name: "platform", State: "ok", Detail: "windows/amd64"},
			{Name: "port", State: "warn", Detail: "probe http://127.0.0.1:2080/" + secretBody},
		},
		Logs: []LogLine{
			{Level: "info", Time: "2026-10-27T10:00:00Z",
				Text: "accepted tcp:" + secretHost + ":443 [proxy]"},
			{Level: "warning", Time: "2026-10-27T10:00:01Z",
				Text: "user id " + secretUUID + " rejected"},
			{Level: "error", Time: "2026-10-27T10:00:02Z",
				Text: "trojan://" + secretPass + "@" + secretHost + ":8443 failed"},
			{Level: "debug", Time: "2026-10-27T10:00:03Z",
				Text: "subscription body " + secretBody},
		},
		Files: []FileInfo{{Name: "data/db.json", Size: 4096}},
	}
}

// zipText 把 ZIP 中的每个成员展开为一个字符串，便于搜索。
func zipText(t *testing.T, raw []byte) string {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("the bundle is not a readable zip: %v", err)
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
		b.WriteString("\n")
	}
	return b.String()
}

// 没有任何凭据进入诊断包。
//
// 这是整个包存在的核心测试：诊断包会上传到公开的问题单，泄露 UUID 就是
// 泄露节点，泄露订阅 URL 就是泄露账号。
func TestBundleLeaksNoCredential(t *testing.T) {
	raw, err := Bytes(reportWithSecrets())
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	text := zipText(t, raw)

	for name, secret := range map[string]string{
		"UUID":               secretUUID,
		"password":           secretPass,
		"full host name":     secretHost,
		"subscription URL":   secretSub,
		"subscription token": "deadbeefcafe1234567890",
		"base64 body":        secretBody,
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("the bundle contains the %s: %q", name, secret)
		}
	}
}

// 诊断包仍保留维护者需要的信息。
//
// 若把一切都脱敏，虽然能通过泄露测试，却毫无用处；此测试检查解释问题所需的
// 事实仍然存在。
func TestBundleKeepsTheDiagnosis(t *testing.T) {
	raw, err := Bytes(reportWithSecrets())
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	text := zipText(t, raw)

	for _, want := range []string{
		"CORE_START_FAILED",
		"vless",
		"443",
		"reality",
		"windows/amd64",
		"my panel",
		"data/db.json",
		RedactedUUID,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the bundle lost %q\n%s", want, text)
		}
	}
}

// ZIP 包含读取方预期的两个成员。
func TestBundleMembers(t *testing.T) {
	raw, err := Bytes(Report{App: "0.1.0"})
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	r, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	names := map[string]bool{}
	for _, f := range r.File {
		names[f.Name] = true
	}
	for _, want := range []string{"report.json", "summary.txt"} {
		if !names[want] {
			t.Fatalf("missing %s; got %v", want, names)
		}
	}
}

// Scrub 能处理真实消息中的各种形式。
func TestScrub(t *testing.T) {
	cases := map[string]string{
		// 任意位置的 UUID。
		"user " + secretUUID + " rejected": "user " + RedactedUUID + " rejected",
		// 分享链接中的用户信息。
		"vless://" + secretUUID + "@host:443": "vless://" + RedactedUUID + "@" + RedactedHost + "/" + RedactedPath,
		// URL 保留协议部分以及曾包含凭据这一事实；主机和路径会被移除，
		// 因为分享链接的主机就是服务器，而订阅链接的路径就是令牌。
		secretSub: "https://" + RedactedHost + "/" + RedactedPath,
		// 长 base64 字符串就是令牌。
		"body " + secretBody: "body " + RedactedURL,
		// 普通文本保持不变。
		"accepted tcp:1.2.3.4:443": "accepted tcp:1.2.3.4:443",
		"":                         "",
	}
	for in, want := range cases {
		if got := Scrub(in); got != want {
			t.Fatalf("Scrub(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

// MaskAddress 会隐藏足够多的主机信息，使其无法作为地址使用，同时保留足以
// 区分节点的信息。
func TestMaskAddress(t *testing.T) {
	cases := map[string]string{
		"jp1.example.com":   "jp1." + RedactedHost + ".com",
		"a.b.c.example.org": "a." + RedactedHost + ".org",
		"203.0.113.9":       "203.x.x.9",
		"localhost":         RedactedHost,
		"":                  "",
	}
	for in, want := range cases {
		if got := MaskAddress(in); got != want {
			t.Fatalf("MaskAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

// 同一服务商的两个节点脱敏后仍可区分，否则诊断包无法显示其中哪一个配置有误。
func TestMaskAddressKeepsNodesDistinct(t *testing.T) {
	a := MaskAddress("jp1.example.com")
	b := MaskAddress("jp2.example.com")
	if a == b {
		t.Fatalf("both masked to %q", a)
	}
}

// 订阅 URL 会被整体替换，而不只是缩短，因为其路径就是令牌。
func TestSourceURLIsReplaced(t *testing.T) {
	r := Report{Sources: []SourceSummary{{Name: "x", URL: secretSub}}}
	ScrubReport(&r)
	if r.Sources[0].URL != RedactedURL {
		t.Fatalf("url = %q", r.Sources[0].URL)
	}
}
