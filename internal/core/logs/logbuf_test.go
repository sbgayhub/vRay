package logs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
	"weak"

	"github.com/xtls/xray-core/common/log"
)

// infoMsg 构造 info 级别的 GeneralMessage。
func infoMsg(text string) log.Message {
	return &log.GeneralMessage{Severity: log.Severity_Info, Content: text}
}

// warnMsg 构造 warning 级别的 GeneralMessage。
func warnMsg(text string) log.Message {
	return &log.GeneralMessage{Severity: log.Severity_Warning, Content: text}
}

type unexpectedFormat struct{ t *testing.T }

func (v unexpectedFormat) String() string {
	v.t.Error("filtered log was formatted")
	return "filtered"
}

func TestCollectorFiltersBeforeFormatting(t *testing.T) {
	c, err := NewCollector(10, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetLevel(LevelWarning)
	c.Handle(&log.GeneralMessage{Severity: log.Severity_Info, Content: unexpectedFormat{t}})
}

// 即使固定容量的环形缓冲区仍在，Clear 也必须释放字符串。
func TestRingClearReleasesBackingStrings(t *testing.T) {
	r := NewRing(4)
	put := func() weak.Pointer[byte] {
		text := strings.Repeat("x", 1<<20)
		r.Add(Entry{Text: text})
		return weak.Make(unsafe.StringData(text))
	}
	old := put()
	r.Clear()
	runtime.GC()
	if old.Value() != nil {
		t.Fatal("cleared ring retained old log text")
	}
	runtime.KeepAlive(r)
}

// waitLines 轮询直到收集器至少持有 n 条记录。
func waitLines(t *testing.T, c *Collector, n int) []Entry {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.Snapshot(); len(got) >= n {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("collector holds %d entries, want at least %d", len(c.Snapshot()), n)
	return nil
}

// Handle 接收记录，记录会按顺序可见。
func TestCollectorKeepsLinesInOrder(t *testing.T) {
	c, err := NewCollector(10, "")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	defer c.Close()

	c.Handle(infoMsg("first"))
	c.Handle(infoMsg("second"))
	c.Handle(infoMsg("third"))

	got := waitLines(t, c, 3)
	want := []string{"first", "second", "third"}
	for i, w := range want {
		if got[i].Text != w {
			t.Fatalf("entry %d = %q, want %q", i, got[i].Text, w)
		}
	}
}

// 环形缓冲区只保留最新的行：长时间会话不能无限增长。
func TestCollectorRingDropsOldest(t *testing.T) {
	c, err := NewCollector(3, "")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	defer c.Close()

	for _, s := range []string{"a", "b", "c", "d", "e"} {
		c.Handle(infoMsg(s))
	}
	waitLines(t, c, 3)
	// 等待五条全部被消费，然后检查存活下来的三条。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && c.Snapshot()[2].Text != "e" {
		time.Sleep(5 * time.Millisecond)
	}
	got := c.Snapshot()
	if len(got) != 3 {
		t.Fatalf("ring holds %d entries, want 3", len(got))
	}
	for i, w := range []string{"c", "d", "e"} {
		if got[i].Text != w {
			t.Fatalf("entry %d = %q, want %q", i, got[i].Text, w)
		}
	}
}

// 级别过滤会丢弃低于阈值的行，且只丢弃这些行。
func TestCollectorLevelFilter(t *testing.T) {
	c, err := NewCollector(50, "")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	defer c.Close()

	c.SetLevel(LevelWarning)
	c.Handle(infoMsg("dropped"))
	c.Handle(warnMsg("kept-warning"))
	c.Handle(&log.GeneralMessage{Severity: log.Severity_Error, Content: "kept-error"})

	got := waitLines(t, c, 2)
	for _, e := range got {
		if e.Text == "dropped" {
			t.Fatal("a line below the threshold was kept")
		}
	}
	if got[0].Text != "kept-warning" || got[1].Text != "kept-error" {
		t.Fatalf("kept %q, %q", got[0].Text, got[1].Text)
	}
	if got[1].Level != LevelError {
		t.Fatalf("severity not mapped: %v", got[1].Level)
	}
}

// 即使写入方跟不上，Handle 也绝不能阻塞：它运行在代理的写入路径上。
func TestHandleNeverBlocks(t *testing.T) {
	// 写入 goroutine 尚未启动的收集器无法排空队列；
	// Handle 仍必须返回，并丢弃行而不是等待。
	c := &Collector{
		ring:  NewRing(10),
		queue: make(chan Entry, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	c.min.Store(int32(LevelDebug))

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			c.Handle(infoMsg("flood"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Handle blocked when the queue was full")
	}
	if c.Dropped() == 0 {
		t.Fatal("a full queue should have dropped lines")
	}
}

// 给定路径时，收集器会写入真实文件。
func TestCollectorWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "app.log")

	c, err := NewCollector(10, path)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	c.Handle(infoMsg("written-to-disk"))
	waitLines(t, c, 1)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(raw), "written-to-disk") {
		t.Fatalf("log file does not hold the line: %q", raw)
	}
	// 文件行带有完整日期，因此跨天保留的日志仍可阅读。
	if !strings.Contains(string(raw), "[信息]") {
		t.Fatalf("log line is missing its level: %q", raw)
	}
}

// 关闭会刷新已排队的记录：关闭前的最后几行
// 正是解释崩溃所需的内容。
func TestCloseFlushesQueue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	c, err := NewCollector(100, path)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}

	for i := 0; i < 50; i++ {
		c.Handle(infoMsg("line"))
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if n := strings.Count(string(raw), "line"); n != 50 {
		t.Fatalf("wrote %d of 50 lines; Close did not drain the queue", n)
	}
}

// Close 幂等，因此重复执行关闭流程不会有害。
func TestCloseIsIdempotent(t *testing.T) {
	c, err := NewCollector(10, "")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// 文件超过上限后会轮转，并保留上一代文件，
// 使长期运行的安装不会占满磁盘。
func TestFileWriterRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w, err := NewFileWriter(path)
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	w.MaxBytes = 200
	defer w.Close()

	for i := 0; i < 20; i++ {
		w.Write(Entry{At: time.Now(), Level: LevelInfo, Text: "a line long enough to fill the budget"})
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current log file missing: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() >= w.MaxBytes*2 {
		t.Fatalf("log file grew to %d bytes without rotating", info.Size())
	}

	backup, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	if backup.Size() == 0 {
		t.Fatal("rotated file is empty")
	}
}

// 轮转重命名失败（Windows 上备份文件正被其他进程读取）后，写入必须继续
// 且不再逐行重试轮转；句柄绝不能停在已关闭的文件上。
func TestFileWriterRotateFailureKeepsWriting(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("重命名失败依赖 Windows 的文件锁语义")
	}
	path := filepath.Join(t.TempDir(), "app.log")

	// 占住备份路径：Windows 下对正被打开的文件重命名会失败。
	backup, err := os.OpenFile(path+".1", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backup.Close()

	w, err := NewFileWriter(path)
	if err != nil {
		t.Fatalf("NewFileWriter: %v", err)
	}
	w.MaxBytes = 64
	defer w.Close()

	line := strings.Repeat("x", 32)
	for i := 0; i < 20; i++ {
		w.Write(Entry{At: time.Now(), Level: LevelInfo, Text: line})
	}

	// 轮转失败后日志必须完整落到原文件，而不是丢进已关闭的句柄。
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() < 20*40 {
		t.Fatalf("log lost lines after failed rotation: %d bytes", info.Size())
	}
}

// Snapshot 返回副本：UI 不能破坏缓冲区。
func TestSnapshotIsACopy(t *testing.T) {
	r := NewRing(5)
	r.Add(Entry{Text: "one"})
	got := r.Snapshot()
	got[0].Text = "changed"
	if r.Snapshot()[0].Text != "one" {
		t.Fatal("Snapshot returned the ring's own storage")
	}
}

// Clear 清空环形缓冲区，但不触碰文件。
func TestClearOnlyClearsRing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	c, err := NewCollector(10, path)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	c.Handle(infoMsg("before-clear"))
	waitLines(t, c, 1)

	c.Clear()
	if n := len(c.Snapshot()); n != 0 {
		t.Fatalf("ring holds %d entries after Clear", n)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "before-clear") {
		t.Fatal("Clear removed the line from the file as well")
	}
}

// 多个 goroutine 并发调用 Handle 时不得发生竞态或丢失环形数据。
func TestConcurrentHandle(t *testing.T) {
	c, err := NewCollector(2000, "")
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	defer c.Close()
	c.SetLevel(LevelDebug)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Handle(infoMsg("concurrent"))
			}
		}()
	}
	wg.Wait()

	// 等待写入方跟上：每一行要么在环形缓冲区中，要么
	// 被计入丢弃数，两者之和必须等于总数。
	deadline := time.Now().Add(3 * time.Second)
	var kept int
	for time.Now().Before(deadline) {
		kept = len(c.Snapshot())
		if kept+int(c.Dropped()) >= 800 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// 不丢失是正常情况：队列远大于一次突发，
	// 且写入方排空很快。若发生丢弃，说明容量预算有误。
	if dropped := c.Dropped(); dropped != 0 {
		t.Fatalf("%d of 800 lines were dropped; the queue is too small for a burst", dropped)
	}
	if kept != 800 {
		t.Fatalf("ring holds %d of 800 lines", kept)
	}
}

// ParseLevel 是全函数：未知名称回退到 info，而不是 panic。
func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug":     LevelDebug,
		"info":      LevelInfo,
		"warning":   LevelWarning,
		"warn":      LevelWarning,
		"error":     LevelError,
		"":          LevelInfo,
		"nonsense":  LevelInfo,
		" WARNING ": LevelWarning,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

// 名称通过 Xray 接受的配置拼写完成往返转换。
func TestLevelStringRoundTrip(t *testing.T) {
	for _, l := range []Level{LevelDebug, LevelInfo, LevelWarning, LevelError} {
		if got := ParseLevel(l.String()); got != l {
			t.Fatalf("%v round-tripped to %v", l, got)
		}
	}
}
