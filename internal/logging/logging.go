// Package logging 提供基于 log/slog 的应用日志。
//
// ConsoleHandler 把记录渲染成一行人类可读的中文控制台文本：
//
//	2026-10-30 12:00:00.123 信息 代理已启动 模块=core 端口=10808
//
// 级别名固定两个汉字，输出天然对齐；属性按键值对跟在消息后面，
// 含空格的值会加引号，便于直接阅读，也便于日后按需切换为 JSON。
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strconv"
	"sync"
)

// 级别到中文名的映射，两字宽保证多行日志对齐。
var levelNames = map[slog.Level]string{
	slog.LevelDebug: "调试",
	slog.LevelInfo:  "信息",
	slog.LevelWarn:  "警告",
	slog.LevelError: "错误",
}

// timeLayout 是控制台输出的时间格式，精确到毫秒。
const timeLayout = "2006-01-02 15:04:05.000"

// ConsoleOptions 配置 ConsoleHandler。
type ConsoleOptions struct {
	// Level 是最低输出级别，nil 表示 Info。
	Level slog.Leveler
	// AddSource 为 true 时在每行末尾附上调用位置。
	AddSource bool
}

// ConsoleHandler 把 slog 记录渲染为单行控制台文本。
//
// 它是并发安全的：多个 goroutine 可以共用同一个 handler。
type ConsoleHandler struct {
	mu        *sync.Mutex
	w         io.Writer
	level     slog.Leveler
	addSource bool
	attrs     []slog.Attr
	groups    []string
}

// NewConsoleHandler 创建控制台 handler，输出写入 w。
func NewConsoleHandler(w io.Writer, opts ConsoleOptions) *ConsoleHandler {
	level := opts.Level
	if level == nil {
		level = slog.LevelInfo
	}
	return &ConsoleHandler{
		mu:        &sync.Mutex{},
		w:         w,
		level:     level,
		addSource: opts.AddSource,
	}
}

// Enabled 报告该级别是否会输出。
func (h *ConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// Handle 渲染一条记录并写入底层输出。
func (h *ConsoleHandler) Handle(_ context.Context, r slog.Record) error {
	var line []byte
	line = r.Time.AppendFormat(line, timeLayout)
	line = append(line, ' ')
	line = append(line, levelName(r.Level)...)
	line = append(line, ' ')
	line = append(line, r.Message...)

	// 前缀属性来自 WithAttrs，记录属性跟在其后，保持出现顺序。
	for _, a := range h.attrs {
		line = h.appendAttr(line, a, nil)
	}
	r.Attrs(func(a slog.Attr) bool {
		line = h.appendAttr(line, a, h.groups)
		return true
	})
	if h.addSource && r.PC != 0 {
		frame := sourceFrame(r.PC)
		if frame != "" {
			line = append(line, " 位置="...)
			line = append(line, frame...)
		}
	}
	line = append(line, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(line)
	return err
}

// WithAttrs 返回附带固定属性的新 handler。
func (h *ConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	next := h.clone()
	next.attrs = appendAttrs(next.attrs, attrs, h.groups)
	return next
}

// WithGroup 返回属性带组名前缀的新 handler。
func (h *ConsoleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	next := h.clone()
	next.groups = append(next.groups, name)
	return next
}

// clone 复制 handler；输出锁在副本间共享，保证写入互斥。
func (h *ConsoleHandler) clone() *ConsoleHandler {
	return &ConsoleHandler{
		mu:        h.mu,
		w:         h.w,
		level:     h.level,
		addSource: h.addSource,
		attrs:     append([]slog.Attr(nil), h.attrs...),
		groups:    append([]string(nil), h.groups...),
	}
}

// appendAttr 把一个属性渲染为 " key=value" 并追加到 line。
//
// 固定属性在 WithAttrs 时已经写入组名前缀，因此传入 nil；
// 记录属性仍使用当前组名。
func (h *ConsoleHandler) appendAttr(line []byte, a slog.Attr, groups []string) []byte {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return line
	}
	if a.Value.Kind() == slog.KindGroup {
		nextGroups := groups
		if a.Key != "" {
			nextGroups = append(append([]string(nil), groups...), a.Key)
		}
		for _, child := range a.Value.Group() {
			line = h.appendAttr(line, child, nextGroups)
		}
		return line
	}
	line = append(line, ' ')
	for _, g := range groups {
		line = append(line, g...)
		line = append(line, '.')
	}
	line = append(line, a.Key...)
	line = append(line, '=')
	return appendValue(line, a.Value)
}

// appendAttrs 拷贝属性并补上组名前缀，供 WithAttrs 使用。
func appendAttrs(dst, attrs []slog.Attr, groups []string) []slog.Attr {
	for _, a := range attrs {
		a.Value = a.Value.Resolve()
		if a.Equal(slog.Attr{}) {
			continue
		}
		if len(groups) > 0 {
			key := ""
			for _, g := range groups {
				key += g + "."
			}
			a.Key = key + a.Key
		}
		dst = append(dst, a)
	}
	return dst
}

// appendValue 渲染一个属性值，含空格或引号的字符串值加引号。
func appendValue(line []byte, v slog.Value) []byte {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if !needsQuote(s) {
			return append(line, s...)
		}
		return strconv.AppendQuote(line, s)
	case slog.KindInt64:
		return strconv.AppendInt(line, v.Int64(), 10)
	case slog.KindUint64:
		return strconv.AppendUint(line, v.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.AppendFloat(line, v.Float64(), 'g', -1, 64)
	case slog.KindBool:
		return strconv.AppendBool(line, v.Bool())
	case slog.KindDuration:
		return append(line, v.Duration().String()...)
	case slog.KindTime:
		return v.Time().AppendFormat(line, timeLayout)
	default:
		s := fmt.Sprint(v.Any())
		if needsQuote(s) {
			return strconv.AppendQuote(line, s)
		}
		return append(line, s...)
	}
}

// needsQuote 报告字符串是否包含需要加引号的字符。
func needsQuote(s string) bool {
	if len(s) == 0 {
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == '"' || s[i] == '=' {
			return true
		}
	}
	return false
}

// levelName 返回级别的中文名，自定义级别回退到级别数值。
func levelName(level slog.Level) string {
	if name, ok := levelNames[level]; ok {
		return name
	}
	return level.String()
}

// sourceFrame 从程序计数器提取 "文件:行号"，失败时返回空串。
func sourceFrame(pc uintptr) string {
	fs := runtime.CallersFrames([]uintptr{pc})
	frame, _ := fs.Next()
	if frame.File == "" {
		return ""
	}
	short := frame.File
	if i := len(frame.File) - 1; i >= 0 {
		for j := i; j >= 0; j-- {
			if frame.File[j] == '/' || frame.File[j] == '\\' {
				short = frame.File[j+1:]
				break
			}
		}
	}
	return short + ":" + strconv.Itoa(frame.Line)
}
