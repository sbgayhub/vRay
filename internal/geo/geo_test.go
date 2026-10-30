package geo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// payload 是测试用的伪数据内容。
const payload = "fake geo data for tests"

// sha 返回 payload 的预期校验和。
func sha() string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// server 提供 payload 和可选校验和。
func server(t *testing.T, withChecksum bool, checksum string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".sha256sum"):
			if !withChecksum {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, "%s  %s\n", checksum, "file")
		case strings.HasSuffix(r.URL.Path, ".dat"):
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			fmt.Fprint(w, payload)
		default:
			http.NotFound(w, r)
		}
	}))
}

// newForServer 让下载器指向测试服务器的 a.dat。
func newForServer(t *testing.T, srv *httptest.Server, dir string) *Downloader {
	t.Helper()
	d := New(dir)
	d.Source = srv.URL
	d.Files = []string{"a.dat"}
	d.Client = &http.Client{Timeout: 5 * time.Second}
	return d
}

// waitFor 轮询直到状态匹配，并返回最终状态。
func waitFor(t *testing.T, d *Downloader, state string) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s := d.Status()
		if s.State == state {
			return s
		}
		if s.State == "failed" && state != "failed" {
			t.Fatalf("download failed: %s", s.Err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state = %q, want %q", d.Status().State, state)
	return Status{}
}

// 校验和正确的文件会下载并写入目标位置。
func TestFetchVerifiesChecksum(t *testing.T) {
	srv := server(t, true, sha())
	defer srv.Close()

	dir := t.TempDir()
	d := newForServer(t, srv, dir)

	if err := d.fetch(t.Context(), "a.dat"); err != nil {
		t.Fatalf("fetch: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "a.dat"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("content = %q, want %q", got, payload)
	}
	// 不留下临时文件。
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "a.dat" {
			t.Fatalf("leftover file: %s", e.Name())
		}
	}
}

// 校验和错误时会拒绝下载，不写入任何内容。
func TestFetchRejectsBadChecksum(t *testing.T) {
	srv := server(t, true, strings.Repeat("0", 64))
	defer srv.Close()

	dir := t.TempDir()
	d := newForServer(t, srv, dir)

	err := d.fetch(t.Context(), "a.dat")
	if err == nil {
		t.Fatal("expected a checksum mismatch")
	}
	if !strings.Contains(err.Error(), "校验和不匹配") {
		t.Fatalf("err = %v, want it to mention the checksum", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "a.dat")); statErr == nil {
		t.Fatal("file was written despite a bad checksum")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Fatalf("leftover file after a failed fetch: %s", e.Name())
	}
}

// 没有发布校验和时，下载仍会成功。
func TestFetchWithoutChecksum(t *testing.T) {
	srv := server(t, false, "")
	defer srv.Close()

	dir := t.TempDir()
	d := newForServer(t, srv, dir)

	if err := d.fetch(t.Context(), "a.dat"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.dat")); err != nil {
		t.Fatalf("file missing: %v", err)
	}
}

// 404 会被报告，而不会被静默接受。
func TestFetchReports404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(http.NotFound))
	defer srv.Close()

	d := newForServer(t, srv, t.TempDir())
	err := d.fetch(t.Context(), "a.dat")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want a 404", err)
	}
}

// 上下文取消会中止下载。
func TestFetchHonoursCancel(t *testing.T) {
	srv := server(t, true, sha())
	defer srv.Close()

	d := newForServer(t, srv, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := d.fetch(ctx, "a.dat"); err == nil {
		t.Fatal("fetch succeeded on a cancelled context")
	}
}

// 在两个文件都存在之前，Present 返回 false。
func TestPresent(t *testing.T) {
	dir := t.TempDir()
	d := New(dir)
	d.Files = []string{"a.dat", "b.dat"}

	if d.Present() {
		t.Fatal("Present on an empty directory")
	}
	os.WriteFile(filepath.Join(dir, "a.dat"), []byte("x"), 0o644)
	if d.Present() {
		t.Fatal("Present with one file")
	}
	os.WriteFile(filepath.Join(dir, "b.dat"), []byte("x"), 0o644)
	if !d.Present() {
		t.Fatal("Present should be true with both files")
	}
}

// 空文件不算存在：中断的下载必须重试。
func TestPresentIgnoresEmptyFile(t *testing.T) {
	dir := t.TempDir()
	d := New(dir)
	d.Files = []string{"a.dat"}

	os.WriteFile(filepath.Join(dir, "a.dat"), nil, 0o644)
	if d.Present() {
		t.Fatal("an empty file counts as present")
	}
}

// Start 会报告进度，并以 ready 状态结束。
func TestStartReachesReady(t *testing.T) {
	srv := server(t, true, sha())
	defer srv.Close()

	d := newForServer(t, srv, t.TempDir())

	var sawDownloading bool
	d.SetProgress(func(s Status) {
		if s.State == "downloading" {
			sawDownloading = true
		}
	})

	d.Start()
	if got := waitFor(t, d, "ready"); got.Err != "" {
		t.Fatalf("ready with an error: %s", got.Err)
	}
	if !sawDownloading {
		t.Fatal("no progress was reported")
	}
}

// 一次运行期间再次调用 Start 不会产生任何操作。
func TestStartIsIdempotent(t *testing.T) {
	srv := server(t, true, sha())
	defer srv.Close()

	d := newForServer(t, srv, t.TempDir())
	d.Start()
	d.Start()
	d.Start()
	waitFor(t, d, "ready")
}

// 服务器失败时，状态变为 failed 并保留原因。
func TestStartReportsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	d := newForServer(t, srv, t.TempDir())
	d.Start()

	if got := waitFor(t, d, "failed"); got.Err == "" {
		t.Fatal("failed without a reason")
	}
}

// Cancel 会停止运行，不留下写了一半的文件。
func TestCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256sum") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", "1000000")
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-release
	}))
	defer srv.Close()
	defer close(release)

	dir := t.TempDir()
	d := newForServer(t, srv, dir)
	d.Start()
	waitFor(t, d, "downloading")
	d.Cancel()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.Status().State != "downloading" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := d.Status().State; got == "downloading" {
		t.Fatalf("still downloading after Cancel")
	}
	// 取消下载不应作为用户需要看到的错误。
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		t.Fatalf("leftover file after cancel: %s", e.Name())
	}
}

// 可以覆盖 Files，以便测试下载顺序。
func TestFilesOverride(t *testing.T) {
	if got := len(New("x").filesOf()); got != len(files) {
		t.Fatalf("default file count = %d, want %d", got, len(files))
	}
	d := New("x")
	d.Files = []string{"only.dat"}
	if got := d.filesOf(); len(got) != 1 || got[0] != "only.dat" {
		t.Fatalf("filesOf = %v", got)
	}
}

// sourceOf 在未设置时回退到默认值，因此零值 Downloader 也能正常工作。
func TestSourceDefault(t *testing.T) {
	d := &Downloader{}
	if got := d.sourceOf(); got != DefaultSource {
		t.Fatalf("sourceOf = %q, want %q", got, DefaultSource)
	}
	d.Source = "http://example.com/dl/"
	if got := d.sourceOf(); got != "http://example.com/dl" {
		t.Fatalf("sourceOf = %q, want the trailing slash trimmed", got)
	}
}
