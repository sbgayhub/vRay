package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// render 输出一条日志并返回其文本，便于断言。
func render(t *testing.T, record slog.Record) string {
	t.Helper()
	var buf bytes.Buffer
	h := NewConsoleHandler(&buf, ConsoleOptions{})
	if err := h.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	return buf.String()
}

func TestConsoleHandlerFormat(t *testing.T) {
	at := time.Date(2026, 10, 30, 12, 0, 0, 123000000, time.Local)
	got := render(t, slog.NewRecord(at, slog.LevelInfo, "代理已启动", 0))
	want := "2026-10-30 12:00:00.123 信息 代理已启动\n"
	if got != want {
		t.Fatalf("输出格式不符：got %q, want %q", got, want)
	}
}

func TestConsoleHandlerAttrsAndGroups(t *testing.T) {
	var buf bytes.Buffer
	h := NewConsoleHandler(&buf, ConsoleOptions{}).WithAttrs([]slog.Attr{
		slog.String("模块", "core"),
	}).WithGroup("节点")
	logger := slog.New(h)
	logger.Error("连接失败", slog.String("名称", "东京 01"), slog.Int("延迟", 120))

	out := buf.String()
	want := "错误 连接失败 模块=core 节点.名称=\"东京 01\" 节点.延迟=120\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("属性与组输出不符：got %q, want suffix %q", out, want)
	}
	if strings.Contains(out, "节点.模块") {
		t.Fatalf("后设的组不应影响此前保存的属性：%q", out)
	}
}

func TestConsoleHandlerGroupSnapshot(t *testing.T) {
	var buf bytes.Buffer
	h := NewConsoleHandler(&buf, ConsoleOptions{}).
		WithAttrs([]slog.Attr{slog.Int("一", 1)}).
		WithGroup("请求").
		WithAttrs([]slog.Attr{slog.Int("二", 2)}).
		WithGroup("网络")

	slog.New(h).Info("完成", slog.Int("三", 3))

	out := buf.String()
	want := "信息 完成 一=1 请求.二=2 请求.网络.三=3\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("组作用域不符：got %q, want suffix %q", out, want)
	}
}

func TestConsoleHandlerNestedGroupValue(t *testing.T) {
	var buf bytes.Buffer
	h := NewConsoleHandler(&buf, ConsoleOptions{}).
		WithGroup("请求").
		WithAttrs([]slog.Attr{
			slog.Group(
				"上游",
				slog.String("主机", "example.com"),
				slog.Int("端口", 443),
			),
		})

	slog.New(h).Info("完成", slog.Group("节点", slog.String("名称", "东京 01")))

	out := buf.String()
	want := "信息 完成 请求.上游.主机=example.com 请求.上游.端口=443 " +
		"请求.节点.名称=\"东京 01\"\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("嵌套组输出不符：got %q, want suffix %q", out, want)
	}
}

func TestConsoleHandlerLevelFilter(t *testing.T) {
	h := NewConsoleHandler(&bytes.Buffer{}, ConsoleOptions{Level: slog.LevelWarn})
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Info 不应通过 Warn 级别的过滤")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("Error 应通过 Warn 级别的过滤")
	}
}

func TestConsoleHandlerWriteError(t *testing.T) {
	h := NewConsoleHandler(failWriter{}, ConsoleOptions{})
	err := h.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelError, "x", 0))
	if err == nil || !errors.Is(err, errWrite) {
		t.Fatalf("应返回底层写入错误，got %v", err)
	}
}

type failWriter struct{}

var errWrite = errors.New("写入失败")

func (failWriter) Write([]byte) (int, error) { return 0, errWrite }
