// Package logs 为 UI 收集 Xray 的日志输出。
//
// Xray 的日志处理器运行在代理自身的写入路径上：每个产生日志的连接都会
// 同步调用它。因此 Handle 绝不能阻塞、解析或碰磁盘——它只给日志打上
// 时间戳并投入带缓冲的通道，由独立 goroutine 完成写入。
package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtls/xray-core/common/log"
)

// Level 表示日志的响度，按从小到大排序，过滤器可直接用 <= 比较。
type Level int

// 各日志级别，对应 Xray 从最安静到最响的严重程度。
const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarning
	LevelError
	LevelNone
)

// ParseLevel 解析级别名称，无法识别时回退到 Info。
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warning", "warn":
		return LevelWarning
	case "error":
		return LevelError
	case "none":
		return LevelNone
	default:
		return LevelInfo
	}
}

// String 返回配置文件中使用的级别名称。
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelWarning:
		return "warning"
	case LevelError:
		return "error"
	case LevelNone:
		return "none"
	default:
		return "info"
	}
}

// Label 是应用内展示的中文级别名。
func (l Level) Label() string {
	switch l {
	case LevelDebug:
		return "调试"
	case LevelInfo:
		return "信息"
	case LevelWarning:
		return "警告"
	case LevelError:
		return "错误"
	case LevelNone:
		return "关闭"
	default:
		return "未知"
	}
}

// Entry 是一条日志。
type Entry struct {
	At    time.Time `json:"at"`
	Level Level     `json:"level"`
	Text  string    `json:"text"`
}

// String 把日志渲染为展示或写入文件的文本。
func (e Entry) String() string {
	return e.At.Format("15:04:05") + " [" + e.Level.Label() + "] " + e.Text
}

// DefaultRing 是 UI 可回看的日志行数。
const DefaultRing = 2000

// queueSize 是等待写入器处理的最大行数。
//
// 通过代理打开网页会产生一阵访问日志突发；突发必须能装进队列，否则写入器
// 只是忙、并未卡死时就会丢日志。4096 条只有几百 KB，足以从容覆盖此类突发，
// 又能在磁盘停顿时限制内存。
const queueSize = 4096

// Collector 既是 Xray 的日志处理器，又带环形缓冲和可选文件输出。
//
// 它可安全并发使用：Handle 会被代理写入路径上的多个 goroutine 调用，
// UI 读取快照，另有一个 goroutine 负责写入。
type Collector struct {
	ring *Ring
	file *FileWriter

	// queue 是 Handle 到写入器的交接通道。刻意带缓冲，队列满即丢弃：
	// 在这里阻塞会阻塞 Xray。
	queue chan Entry

	// min 是保留的最低级别；更安静的日志会提前丢弃。
	min atomic.Int32

	// dropped 统计因队列满而丢失的行数，让 UI 中的空缺有据可查，
	// 而不是看起来什么都没发生。
	dropped atomic.Int64

	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// Attach 把 c 设为进程级日志处理器，替换现有处理器。
//
// 必须在实例启动后调用，原因值得了解：Xray 的 app/log 在构建配置时会
// 自行调用 log.RegisterHandler，因此 StartInstance 之前注册的处理器会被
// 悄悄替换、收不到任何日志。RegisterHandler 是纯 Set，没有链式调用，
// 外部也无法观察上一个处理器。
//
// 这在这里没有代价：本收集器负责写日志文件和供给日志页，而配置生成器让
// Xray 自身日志留在 stdout（见 configgen.logConfig），同一行不会写进两个文件。
func (c *Collector) Attach() {
	log.RegisterHandler(c)
}

// NewCollector 返回一个收集器：内存中保留最近 size 行，path 非空时追加写入。
func NewCollector(size int, path string) (*Collector, error) {
	if size <= 0 {
		size = DefaultRing
	}
	c := &Collector{
		ring:  NewRing(size),
		queue: make(chan Entry, queueSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	c.min.Store(int32(LevelInfo))

	if path != "" {
		f, err := NewFileWriter(path)
		if err != nil {
			return nil, err
		}
		c.file = f
	}

	go c.write()
	return c, nil
}

// SetLevel 设置保留的最低级别。
func (c *Collector) SetLevel(l Level) { c.min.Store(int32(l)) }

// Level 返回当前阈值。
func (c *Collector) Level() Level { return Level(c.min.Load()) }

// Dropped 返回因写入器落后而丢失的行数。
func (c *Collector) Dropped() int64 { return c.dropped.Load() }

// Handle 实现 log.Handler。它必须保持廉价：运行在 Xray 的写入路径上，
// 这里的任何延迟都会延迟用户的流量。
func (c *Collector) Handle(msg log.Message) {
	if msg == nil {
		return
	}
	level := LevelInfo

	// GeneralMessage 自带严重程度；其他消息按 info 处理并使用其渲染文本。
	if gm, ok := msg.(*log.GeneralMessage); ok {
		level = levelOf(gm.Severity)
	}

	if level < c.Level() {
		return
	}
	// 访问日志和一般消息可能格式化地址与包装错误。只在过滤后、每条保留
	// 消息上执行一次。
	var text string
	if gm, ok := msg.(*log.GeneralMessage); ok {
		text = fmt.Sprint(gm.Content)
	} else {
		text = msg.String()
	}
	e := Entry{At: time.Now(), Level: level, Text: text}

	select {
	case c.queue <- e:
	default:
		// 写入器落后了。丢弃是较小的恶：在这里阻塞会拖住记录日志的连接。
		c.dropped.Add(1)
	}
}

// levelOf 把 Xray 的严重程度映射到本包的级别。
func levelOf(s log.Severity) Level {
	switch s {
	case log.Severity_Debug:
		return LevelDebug
	case log.Severity_Warning:
		return LevelWarning
	case log.Severity_Error:
		return LevelError
	default:
		return LevelInfo
	}
}

// write 把队列排空到环形缓冲和文件。
func (c *Collector) write() {
	defer close(c.done)
	for {
		select {
		case <-c.stop:
			// 排空已入队的内容，避免丢失关闭前的最后几行。
			for {
				select {
				case e := <-c.queue:
					c.persist(e)
				default:
					return
				}
			}
		case e := <-c.queue:
			c.persist(e)
		}
	}
}

// persist 保存一条日志。只有写入 goroutine 会调用它。
func (c *Collector) persist(e Entry) {
	c.ring.Add(e)
	if c.file != nil {
		c.file.Write(e)
	}
}

// Snapshot 返回当前持有的日志，最旧的在前。
func (c *Collector) Snapshot() []Entry { return c.ring.Snapshot() }

// Clear 清空环形缓冲但保留文件：文件是记录，环形缓冲只是窗口展示的内容。
func (c *Collector) Clear() { c.ring.Clear() }

// Close 停止写入器并关闭文件，可重复调用。
func (c *Collector) Close() error {
	var err error
	c.stopOnce.Do(func() {
		close(c.stop)
		<-c.done
		if c.file != nil {
			err = c.file.Close()
		}
	})
	return err
}

// Ring 是容量固定的缓冲区，写满后丢弃最旧的条目。
type Ring struct {
	mu    sync.Mutex
	buf   []Entry
	start int
	count int
}

// NewRing 返回最多容纳 n 条记录的环形缓冲。
func NewRing(n int) *Ring {
	if n <= 0 {
		n = DefaultRing
	}
	return &Ring{buf: make([]Entry, n)}
}

// Add 追加一条记录，写满后丢弃最旧的。
func (r *Ring) Add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count < len(r.buf) {
		r.buf[(r.start+r.count)%len(r.buf)] = e
		r.count++
		return
	}
	r.buf[r.start] = e
	r.start = (r.start + 1) % len(r.buf)
}

// Snapshot 返回记录的副本，最旧的在前。
func (r *Ring) Snapshot() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, r.count)
	for i := 0; i < r.count; i++ {
		out[i] = r.buf[(r.start+i)%len(r.buf)]
	}
	return out
}

// Len 返回当前持有的条目数。
func (r *Ring) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// Clear 丢弃全部条目。
func (r *Ring) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.buf)
	r.start, r.count = 0, 0
}

// FileWriter 把日志追加到文件，超过 MaxBytes 后轮转，避免长期运行的
// 安装把磁盘写满。
type FileWriter struct {
	mu       sync.Mutex
	f        *os.File
	path     string
	written  int64
	MaxBytes int64
}

// DefaultMaxLogBytes 是单个日志文件轮转前的大小上限。
const DefaultMaxLogBytes = 8 << 20 // 8 MiB

// NewFileWriter 以追加方式打开 path，必要时创建目录。
func NewFileWriter(path string) (*FileWriter, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("日志：创建目录 %s 失败：%w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("日志：打开 %s 失败：%w", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("日志：读取 %s 信息失败：%w", path, err)
	}
	return &FileWriter{f: f, path: path, written: info.Size(), MaxBytes: DefaultMaxLogBytes}, nil
}

// Write 追加一条记录，文件过大时先轮转。
func (w *FileWriter) Write(e Entry) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return
	}
	line := e.At.Format("2006-01-02 15:04:05") + " [" + e.Level.Label() + "] " + e.Text + "\n"
	n, err := w.f.WriteString(line)
	if err != nil {
		// 磁盘故障不能拖停代理；环形缓冲里仍有这一行。
		return
	}
	w.written += int64(n)
	if w.MaxBytes > 0 && w.written >= w.MaxBytes {
		w.rotate()
	}
}

// rotate 把当前文件移到一旁并新建文件。旧文件保留为 "<name>.1"：
// 一代备份足以诊断崩溃，又不让日志无限增长。
func (w *FileWriter) rotate() {
	w.f.Close()
	backup := w.path + ".1"
	_ = os.Remove(backup)
	if err := os.Rename(w.path, backup); err != nil {
		// 重命名失败：继续追加，而不是丢失文件句柄。
		if f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			w.f = f
		}
		return
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	w.f = f
	w.written = 0
}

// Close 刷新并关闭文件，可重复调用。
func (w *FileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
