// geo 包负责下载 Xray 的 geoip.dat 和 geosite.dat。
//
// 两者合计 26 MB，缺少它们时 Xray 无法读取 geoip:/geosite: 规则。
// 应用不会因等待它们而阻塞：先使用普通规则启动，在后台下载，准备就绪后再提示。
package geo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultSource 是数据文件的来源。
//
// Loyalsoldier/v2ray-rules-dat 每次发布都会提供 geoip.dat、geosite.dat
// 以及匹配的 .sha256sum，因此下载内容可以验证，而不必直接信任。
const DefaultSource = "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download"

// files 是两个数据文件，按下载顺序排列（较小者在前，便于用户更早看到进度）。
var files = []string{"geosite.dat", "geoip.dat"}

// Status 是 UI 显示的下载状态。
type Status struct {
	// State 为 "idle"、"downloading"、"ready" 或 "failed"。
	State string
	// 当 State 为 "downloading" 时，File 是正在下载的文件名。
	File string
	// Done 和 Total 的单位是字节；服务器未提供长度时 Total 为 0。
	Done, Total int64
	// Err 说明失败原因。
	Err string
}

// Downloader 将数据文件逐个下载到 Dir。
type Downloader struct {
	Dir string
	// Source 是获取文件的目录 URL；为空时使用 DefaultSource。
	Source string
	Client *http.Client
	// Files 是要获取的文件名；为 nil 时使用包默认值。
	Files []string

	mu         sync.Mutex
	status     Status
	cancel     context.CancelFunc
	generation uint64
	onProgress func(Status)
}

// New 返回一个写入 dir 的下载器。
func New(dir string) *Downloader {
	return &Downloader{
		Dir:    dir,
		Source: DefaultSource,
		Client: &http.Client{
			// Xray 的数据文件每个只有几 MB；慢速链路很常见，因此只限制整个请求的时长，
			// 不限制空闲时长。
			Timeout: 30 * time.Minute,
		},
		status: Status{State: "idle"},
	}
}

// filesOf 返回要下载的文件名。
func (d *Downloader) filesOf() []string {
	if len(d.Files) > 0 {
		return d.Files
	}
	return files
}

// sourceOf 返回基础 URL，未设置时使用默认值。
func (d *Downloader) sourceOf() string {
	if d.Source != "" {
		return strings.TrimRight(d.Source, "/")
	}
	return DefaultSource
}

// Status 报告当前状态。
func (d *Downloader) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// SetProgress 设置每个数据块到达时调用的回调，供 UI 使用。
func (d *Downloader) SetProgress(fn func(Status)) {
	d.mu.Lock()
	d.onProgress = fn
	d.mu.Unlock()
}

func (d *Downloader) setStatus(s Status) {
	d.mu.Lock()
	d.status = s
	fn := d.onProgress
	d.mu.Unlock()
	if fn != nil {
		fn(s)
	}
}

// Present 报告两个文件是否都已位于 Dir 中。
func (d *Downloader) Present() bool {
	for _, name := range d.filesOf() {
		info, err := os.Stat(filepath.Join(d.Dir, name))
		if err != nil || info.IsDir() || info.Size() == 0 {
			return false
		}
	}
	return true
}

// Start 在后台开始下载并立即返回。下载运行期间再次调用不会执行任何操作。
func (d *Downloader) Start() {
	d.mu.Lock()
	if d.cancel != nil {
		d.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.generation++
	gen := d.generation
	d.mu.Unlock()

	go func() {
		err := d.run(ctx)

		// 只在本次任务仍为当前任务时清理其句柄。RetryGeo 会取消当前任务并立即启动新任务，
		// 因此较晚结束的旧任务不能清除新任务的 cancel，也不能覆盖其状态。
		d.mu.Lock()
		current := d.generation == gen
		if current {
			d.cancel = nil
		}
		d.mu.Unlock()
		if !current {
			return
		}

		switch {
		case errors.Is(err, context.Canceled):
			// 取消不算失败，但 UI 必须退出 "downloading" 状态，因此无论如何都报告 idle。
			d.setStatus(Status{State: "idle"})
		case err != nil:
			d.setStatus(Status{State: "failed", Err: err.Error()})
		default:
			d.setStatus(Status{State: "ready"})
		}
	}()
}

// Cancel 停止正在运行的下载。句柄会立即清除，因此紧随其后的 Start 会开始新任务，
// 而不会被忽略。
func (d *Downloader) Cancel() {
	d.mu.Lock()
	cancel := d.cancel
	d.cancel = nil
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// run 下载所有缺失的文件。
func (d *Downloader) run(ctx context.Context) error {
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return fmt.Errorf("地理数据：创建目录 %s 失败：%w", d.Dir, err)
	}
	for _, name := range d.filesOf() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if present(filepath.Join(d.Dir, name)) {
			continue
		}
		if err := d.fetch(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

// fetch 下载一个文件，在发布方提供校验和时进行验证，然后将其移动到目标位置。
func (d *Downloader) fetch(ctx context.Context, name string) error {
	wantSum, _ := d.checksum(ctx, name) // 缺少校验和不会导致失败

	url := d.sourceOf() + "/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("地理数据：创建 %s 下载请求失败：%w", name, err)
	}

	resp, err := d.Client.Do(req)
	if err != nil {
		return fmt.Errorf("地理数据：请求 %s 失败：%w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("地理数据：请求 %s 返回 %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(d.Dir, "."+name+"-*")
	if err != nil {
		return fmt.Errorf("地理数据：创建临时文件失败：%w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	hasher := sha256.New()
	total := resp.ContentLength
	var done int64

	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				tmp.Close()
				return fmt.Errorf("地理数据：写入 %s 失败：%w", name, werr)
			}
			hasher.Write(buf[:n])
			done += int64(n)
			d.setStatus(Status{State: "downloading", File: name, Done: done, Total: total})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tmp.Close()
			return fmt.Errorf("地理数据：读取 %s 失败：%w", name, readErr)
		}
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("地理数据：关闭 %s 失败：%w", name, err)
	}

	if wantSum != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(got, wantSum) {
			return fmt.Errorf("地理数据：%s 校验和不匹配，实际为 %s，期望为 %s", name, got, wantSum)
		}
	}

	final := filepath.Join(d.Dir, name)
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("地理数据：重命名 %s 失败：%w", name, err)
	}
	return nil
}

// checksum 读取发布方为 name 提供的 .sha256sum。
func (d *Downloader) checksum(ctx context.Context, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.sourceOf()+"/"+name+".sha256sum", nil)
	if err != nil {
		return "", err
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("地理数据：获取 %s 校验和返回 %s", name, resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	// 每行格式为 "<hex>  <name>"。
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return "", fmt.Errorf("地理数据：%s 的校验和为空", name)
	}
	return fields[0], nil
}

// present 报告 path 是否存在且非空。
func present(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}
