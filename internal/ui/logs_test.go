package ui

import (
	"sync"
	"testing"
	"time"

	"github.com/ELY-Open-Source/CrychicUI/data"
	"github.com/egoist/mygo/ui"

	"github.com/sbgayhub/vray/internal/app"
	"github.com/sbgayhub/vray/internal/core/logs"
)

// memLogs 是测试可填充的 LogSource。
type memLogs struct {
	mu      sync.Mutex
	entries []logs.Entry
	dropped int64
}

func (m *memLogs) Snapshot() []logs.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]logs.Entry(nil), m.entries...)
}

func (m *memLogs) Stamp() (int, logs.Entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		return 0, logs.Entry{}
	}
	return len(m.entries), m.entries[len(m.entries)-1]
}

func (m *memLogs) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = nil
}

func (m *memLogs) Dropped() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dropped
}

func (m *memLogs) add(level logs.Level, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, logs.Entry{At: time.Now(), Level: level, Text: text})
}

// newLogsRoot 构建一个日志页有内容可显示的根视图。
func newLogsRoot(t *testing.T) (*Root, *memLogs) {
	t.Helper()
	r, svc := newRoot(t)
	svc.State().Update(func(sn *app.Snapshot) { sn.Settings.LogLevel = "debug" })
	src := &memLogs{}
	src.add(logs.LevelInfo, "Xray 26.3.27 started")
	src.add(logs.LevelWarning, "failed to dial example.com")
	src.add(logs.LevelError, "connection refused")
	svc.AttachLogs(src)

	r.cur, r.route = PageLogs, pageRoutes[PageLogs].ID
	r.logRaw = true
	return r, src
}

// 日志页列出收集到的行。
func TestLogsPageShowsLines(t *testing.T) {
	r, _ := newLogsRoot(t)

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("Xray 26.3.27 started") {
		t.Fatal("a log line is not shown")
	}
	if !tr.HasText("3 行") {
		t.Fatal("the line count is missing")
	}
}

// 没有收集到内容时，页面会说明将显示什么，而不是显示空框。
func TestLogsPageEmptyState(t *testing.T) {
	r, svc := newRoot(t)
	svc.AttachLogs(&memLogs{})
	r.cur, r.route = PageLogs, pageRoutes[PageLogs].ID

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("暂无日志") {
		t.Fatal("the empty state is missing")
	}
}

// 级别过滤器隐藏较低级别的行，并保留其余行。
func TestLogsPageLevelFilter(t *testing.T) {
	r, _ := newLogsRoot(t)
	r.logLevel = logs.LevelWarning

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if tr.HasText("Xray 26.3.27 started") {
		t.Fatal("an info line survived a warning filter")
	}
	if !tr.HasText("failed to dial example.com") || !tr.HasText("connection refused") {
		t.Fatal("warning and error lines were filtered out")
	}
	if !tr.HasText("显示 2 / 3 行") {
		t.Fatal("the count does not reflect the filter")
	}
}

// 文本过滤器匹配消息。
func TestLogsPageTextFilter(t *testing.T) {
	r, _ := newLogsRoot(t)
	r.logQuery = "refused"

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("connection refused") {
		t.Fatal("the matching line is missing")
	}
	if tr.HasText("Xray 26.3.27 started") {
		t.Fatal("a non-matching line survived")
	}
}

// 无匹配项的过滤器会明确提示。
func TestLogsPageNoMatch(t *testing.T) {
	r, _ := newLogsRoot(t)
	r.logQuery = "definitely-not-present"

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("没有匹配的日志行") {
		t.Fatal("an empty result is not explained")
	}
}

// 丢弃的行会被报告：静默漏行的视图在说谎。
func TestLogsPageReportsDropped(t *testing.T) {
	r, src := newLogsRoot(t)
	src.mu.Lock()
	src.dropped = 7
	src.mu.Unlock()

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if !tr.HasText("7 行") {
		t.Fatal("dropped lines are not reported")
	}
}

// filterLogs 只保留最新的行，因此长时间会话不会拖慢帧渲染。
func TestFilterLogsBounded(t *testing.T) {
	lines := make([]logs.Entry, logPageMax+50)
	for i := range lines {
		lines[i] = logs.Entry{Level: logs.LevelInfo, Text: "line"}
	}
	got := filterLogs(lines, "", logs.LevelDebug)
	if len(got) != logPageMax {
		t.Fatalf("filtered to %d lines, want %d", len(got), logPageMax)
	}
}

// filterLogs 不得返回调用方的切片：环形缓冲区的副本是只读的。
func TestFilterLogsCopies(t *testing.T) {
	lines := []logs.Entry{{Level: logs.LevelInfo, Text: "one"}}
	got := filterLogs(lines, "", logs.LevelDebug)
	got[0].Text = "changed"
	if lines[0].Text != "one" {
		t.Fatal("filterLogs returned the caller's slice")
	}
}

// 计数标签区分“全部”和“已过滤”。
func TestLogCountLabel(t *testing.T) {
	if got := logCountLabel(4, 4); got != "4 行" {
		t.Fatalf("logCountLabel(4,4) = %q", got)
	}
	if got := logCountLabel(2, 7); got != "显示 2 / 7 行" {
		t.Fatalf("logCountLabel(2,7) = %q", got)
	}
}

// toLogLines 保留时间、文本和级别，并将控件会因此崩溃的级别限制到有效范围。
func TestToLogLines(t *testing.T) {
	at := time.Date(2026, 10, 27, 13, 45, 6, 0, time.UTC)
	in := []logs.Entry{
		{At: at, Level: logs.LevelDebug, Text: "d"},
		{At: at, Level: logs.LevelError, Text: "e"},
		{At: at, Level: logs.Level(99), Text: "out-of-range"},
	}
	got := toLogLines(in)
	if len(got) != 3 {
		t.Fatalf("got %d lines", len(got))
	}
	if got[0].Time != "13:45:06" {
		t.Fatalf("time = %q", got[0].Time)
	}
	if got[2].Level != data.LevelError {
		t.Fatalf("an out-of-range level was not clamped: %v", got[2].Level)
	}
}

// 从页面清除会清空当前显示的缓冲区。
func TestLogsPageClear(t *testing.T) {
	r, src := newLogsRoot(t)
	r.svc.ClearLogs()

	tr := ui.NewTester(r.View, 960, 640)
	tr.Frame()
	if len(src.Snapshot()) != 0 {
		t.Fatal("the buffer was not cleared")
	}
	if !tr.HasText("暂无日志") {
		t.Fatal("the empty state is missing after clearing")
	}
}
