// Package effects 记录我们对系统所做的变更，并负责撤销。
//
// 任何改动本机设置的代理客户端都背负一个进程自身无法解决的问题：进程被
// 杀死后，机器会留下失效的代理配置，运气不好连 DNS 也不可用。三层机制
// 应对它：
//
//  1. 进程内 defer，应对正常退出；
//  2. 应用退出钩子，应对菜单或窗口发起的退出；
//  3. 磁盘上的本日志，每次启动时读取，应对 kill -9 或断电。
//
// 第三层正是文件存在的原因。它在变更发生“之前”写入，只有全部还原后才删除；
// 因此启动时存在非空日志就意味着“上次运行没来得及清理”。
package effects

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Kind 命名一类变更。它的值会写入日志，因此必须跨版本保持稳定：
// 改名会让旧日志在最糟糕的时刻变得不可读。
type Kind string

// vRay 会做的变更类型。
//
// system-proxy 与 dns 由平台层写入；routes 与 tun 由辅助程序安装。把它们
// 集中列出，让日志拥有封闭词汇表：未知类型是启动错误，而不是悄悄跳过。
const (
	KindSystemProxy Kind = "system-proxy"
	KindDNS         Kind = "dns"
	KindRoute       Kind = "route"
	KindTun         Kind = "tun"
)

// Known 报告当前构建是否认识类型 k。
func (k Kind) Known() bool {
	switch k {
	case KindSystemProxy, KindDNS, KindRoute, KindTun:
		return true
	default:
		return false
	}
}

// Entry 是一条变更及其撤销方式。
//
// Restore 是不透明命令字符串而不是回调：磁盘上的日志必须能被非写入进程
// 重放，因此撤销方式必须是数据而非代码。各平台层知道如何执行自己的命令；
// 见 Restorer。
type Entry struct {
	Kind Kind `json:"kind"`
	// Target 命名被变更的对象，例如 "Wi-Fi" 或 "utun7 / 0.0.0.0/0"。
	Target string `json:"target"`
	// Restore 是撤销该变更的命令。
	Restore string `json:"restore"`
	// Backup 保存原值，用于通过写入而非命令来还原。
	Backup  string    `json:"backup,omitempty"`
	Created time.Time `json:"created"`
}

// Restorer 执行一次撤销，由平台层提供。
//
// 返回错误会把条目留在日志中，下次启动再试：因网络中断而失败过一次的
// DNS 还原不应被遗忘。
type Restorer func(Entry) error

// Journal 是效果文件：有序的变更列表，最新的在最后。
//
// 条目按最新优先还原。这个顺序不是装饰：系统代理可能指向 DNS 条目添加的
// DNS 服务器，若按变更发生顺序撤销，会把代理还原到一个已不再响应的地址。
type Journal struct {
	path  string
	mu    sync.Mutex
	entry []Entry
	// save 是写入函数，可注入以便测试无需磁盘。
	save func(path string, data []byte) error
}

// Open 读取 path 处的日志，文件缺失时创建空日志。损坏文件不是调用方能修复
// 的错误，因此与空日志一起报告：调用方应把报告的条目视为未知并发出警告。
func Open(path string) (*Journal, []Entry, error) {
	j := &Journal{path: path, save: writeFileAtomic}

	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return j, nil, nil
	}
	if err != nil {
		return j, nil, fmt.Errorf("副作用日志：读取失败：%w", err)
	}
	// 手工编辑的文件常带字节序标记，而 JSON 不允许。在这里剥掉它，
	// 后续读取只需处理一种形态。
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	if len(raw) == 0 {
		return j, nil, nil
	}

	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		// 键顺序无关紧要，多余的字节序标记也不该让用户失去恢复机会：
		// 在判定文件损坏前先尝试结构等价的形态。（设计文档展示的是按类型
		// 为键的对象，因此旧版或手写日志可能采用该形态。）
		entries, err = decodeLegacy(raw)
		if err != nil {
			// 保留不可读文件：它是上次运行做了什么的唯一证据，
			// 人类可能仍想查看。
			quarantine := path + ".corrupt"
			_ = os.Rename(path, quarantine)
			return j, nil, fmt.Errorf("副作用日志：原文件不可读，已移动到 %s：%w", quarantine, err)
		}
	}
	for _, e := range entries {
		if !e.Kind.Known() {
			return j, entries, fmt.Errorf("副作用日志：包含未知类型 %q", e.Kind)
		}
	}
	j.entry = entries
	return j, entries, nil
}

// Path 返回日志所在位置。
func (j *Journal) Path() string { return j.path }

// Entries 返回当前日志的副本。
func (j *Journal) Entries() []Entry {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Entry(nil), j.entry...)
}

// Pending 报告是否有记录。启动时日志非空意味着上次运行未完成清理。
func (j *Journal) Pending() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.entry) > 0
}

// Record 在调用方执行变更“之前”把变更写入磁盘。
//
// 顺序就是全部意义：变更之后才写条目，意味着崩溃可能让变更未记录，
// 而这正是本日志要避免的状态。
func (j *Journal) Record(e Entry) error {
	if !e.Kind.Known() {
		return fmt.Errorf("副作用日志：未知类型 %q", e.Kind)
	}
	if e.Created.IsZero() {
		e.Created = time.Now()
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	next := append(append([]Entry(nil), j.entry...), e)
	if err := j.flush(next); err != nil {
		return err
	}
	j.entry = next
	return nil
}

// RestoreAll 用给定还原器撤销所有条目，最新的优先。
//
// 还原失败的条目留在日志中，错误会被收集：一条卡住的条目不得阻止其余条目
// 被撤销。只有全部成功才删除日志，因此“没有日志”始终意味着“没有遗留变更”。
func (j *Journal) RestoreAll(restore Restorer) error {
	j.mu.Lock()
	entries := append([]Entry(nil), j.entry...)
	j.mu.Unlock()

	if len(entries) == 0 {
		return nil
	}
	if restore == nil {
		return errors.New("副作用日志：没有可用的还原器")
	}

	// 记录还原失败的索引，让幸存条目保持原顺序，重试时仍按最新优先撤销。
	failedAt := make(map[int]bool, len(entries))
	var errs []error
	for i := len(entries) - 1; i >= 0; i-- {
		if err := restore(entries[i]); err != nil {
			failedAt[i] = true
			errs = append(errs, fmt.Errorf("%s %s: %w", entries[i].Kind, entries[i].Target, err))
		}
	}
	remaining := make([]Entry, 0, len(failedAt))
	for i := range entries {
		if failedAt[i] {
			remaining = append(remaining, entries[i])
		}
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if len(remaining) == 0 {
		if err := os.Remove(j.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("副作用日志：删除失败：%w", err)
		}
		j.entry = nil
		return nil
	}
	if err := j.flush(remaining); err != nil {
		errs = append(errs, err)
	}
	j.entry = remaining
	return errors.Join(errs...)
}

// Clear 在不撤销的情况下遗忘全部内容。
//
// 仅适用于已自行撤销变更、并确认日志已过期的调用方——例如成功还原后的
// 第二次还原，或测试。
func (j *Journal) Clear() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := os.Remove(j.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("副作用日志：删除失败：%w", err)
	}
	j.entry = nil
	return nil
}

// flush 写入条目，必要时创建目录。
func (j *Journal) flush(entries []Entry) error {
	if err := os.MkdirAll(filepath.Dir(j.path), 0o755); err != nil {
		return fmt.Errorf("副作用日志：创建目录失败：%w", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("副作用日志：编码失败：%w", err)
	}
	saver := j.save
	if saver == nil {
		saver = writeFileAtomic
	}
	if err := saver(j.path, raw); err != nil {
		return fmt.Errorf("副作用日志：写入失败：%w", err)
	}
	return nil
}

// decodeLegacy 读取日志除普通数组之外的两种形态。
//
// 先剥掉字节序标记：编辑器而非 vRay 写入的文件常有它，
// 因为三个字节的前缀而失去恢复能力实在荒谬。
func decodeLegacy(raw []byte) ([]Entry, error) {
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})

	// 形态 2：以类型为键的对象，如设计文档所描绘。
	var byKind map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byKind); err == nil {
		kinds := make([]string, 0, len(byKind))
		for k := range byKind {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		var out []Entry
		for _, k := range kinds {
			// 值可能是单条记录或记录列表。
			var one Entry
			if err := json.Unmarshal(byKind[k], &one); err == nil && one.Kind != "" {
				out = append(out, one)
				continue
			}
			var many []Entry
			if err := json.Unmarshal(byKind[k], &many); err == nil {
				out = append(out, many...)
				continue
			}
			return nil, fmt.Errorf("副作用日志：无法读取 %q 段", k)
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	// 形态 3：单独的一条记录。
	var one Entry
	if err := json.Unmarshal(raw, &one); err == nil && one.Kind != "" {
		return []Entry{one}, nil
	}
	return nil, errors.New("副作用日志：没有可读取的条目")
}

// writeFileAtomic 先写临时文件再重命名到位，因此写入中途崩溃不会留下
// 半截日志——那会在最要紧的时刻被读成损坏。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".effects-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	// 日志精确记录了本机发生了什么变更，其他任何人都不应读取它。
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// RecoverFromCrash 是启动自检：日志有条目说明上次运行未清理。
// 它还原这些条目并报告所做的事情。
//
// 返回的条目就是被撤销的内容，外壳因此能告诉用户
// （“上次没有正常退出，系统代理已还原”），而不是悄悄改回设置。
func RecoverFromCrash(path string, restore Restorer) (recovered []Entry, err error) {
	j, entries, openErr := Open(path)
	if openErr != nil && len(entries) == 0 {
		return nil, openErr
	}
	if len(entries) == 0 {
		return nil, openErr
	}
	restoreErr := j.RestoreAll(restore)
	return entries, errors.Join(openErr, restoreErr)
}
