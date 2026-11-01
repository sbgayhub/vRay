package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sbgayhub/vray/internal/gbkx"
)

// damaged 返回 s 的乱码形式：将其 UTF-8 字节按本代码页解码，即待修复的损坏。
func damaged(t *testing.T, s string) string {
	t.Helper()
	out, ok := gbkx.Decode([]byte(s))
	if !ok {
		t.Fatalf("%q does not read through this code page", s)
	}
	return out
}

// UTF-8 文本经 GBK 往返转换后产生乱码，修复后应还原为原始文本。
func TestRepairRestoresMojibake(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")

	original := "节点缺少凭据"
	bad := damaged(t, original)
	if bad == original {
		t.Skip("this code page maps the text to itself; nothing to repair")
	}

	src := "package a\n\nvar msg = \"" + bad + "\"\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Repair(path)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !changed {
		t.Fatal("nothing was repaired")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), original) {
		t.Fatalf("got %q, want %q", string(got), original)
	}
}

// 干净文件应保持逐字节不变，确保对整个目录运行修复器不会破坏正常代码。
func TestRepairLeavesCleanTextAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.go")
	clean := "package a\n\n// 节点缺少凭据 — a clean line.\nvar msg = \"节点缺少凭据\"\n"
	if err := os.WriteFile(path, []byte(clean), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Repair(path)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if changed {
		t.Fatal("a clean file was rewritten")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != clean {
		t.Fatalf("file changed:\n%s", string(got))
	}
}

// 验证代码页本身：以 GBK 字节写入的文本应能读回相同内容。
func TestCodePageRoundTrip(t *testing.T) {
	for _, s := range []string{"节点", "缺少", "凭据", "代理模式", "vRay 客户端"} {
		b, ok := gbkx.Encode(s)
		if !ok {
			t.Fatalf("Encode(%q) failed", s)
		}
		back, ok := gbkx.Decode(b)
		if !ok {
			t.Fatalf("Decode(% X) failed", b)
		}
		if back != s {
			t.Errorf("round trip of %q = %q", s, back)
		}
	}
}

// decodeLine 执行损坏的逆操作，返回原始文本。
func TestDecodeLine(t *testing.T) {
	for _, want := range []string{"节点", "缺少", "凭据", "代理模式"} {
		bad := damaged(t, want)
		got, ok := decodeLine(bad)
		if !ok || got != want {
			t.Errorf("decodeLine(%q) = %q, want %q", bad, got, want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("decodeLine(%q) is not valid UTF-8", bad)
		}
	}
}

// 截断行（有损步骤吞掉了一个字节）不得按猜测结果写回。
func TestTruncatedLineIsLeftAlone(t *testing.T) {
	bad := damaged(t, "节点") + "\xef\xbf\xbd"
	if got, ok := decodeLine(bad); ok {
		t.Fatalf("decodeLine rewrote a truncated line: %q", got)
	}
}
