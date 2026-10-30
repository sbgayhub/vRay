package effects

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func journalPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "effects.json")
}

// 记录在重启后仍存在，因此崩溃会留下证据。
//
// 这正是该文件存在的意义：写入条目的进程已消失，
// 下一个进程必须能够读取它。
func TestRecordSurvivesReopen(t *testing.T) {
	path := journalPath(t)
	j, _, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if j.Pending() {
		t.Fatal("a fresh journal reports entries")
	}
	if err := j.Record(Entry{
		Kind: KindSystemProxy, Target: "Wi-Fi", Restore: "netsh ... disable",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// 一个新进程。
	j2, entries, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(entries) != 1 || entries[0].Target != "Wi-Fi" {
		t.Fatalf("entries = %+v", entries)
	}
	if !j2.Pending() {
		t.Fatal("the reopened journal reports nothing pending")
	}
	if entries[0].Created.IsZero() {
		t.Fatal("the entry has no timestamp")
	}
}

// 恢复按最新优先执行，完成后日志被清除。
//
// 顺序很重要，因为后一次变更可能依赖前一次：
// 如果在 DNS 条目之前先撤销它所指向的代理，
// 代理就会指向一个已不再响应的地址。
func TestRestoreAllIsNewestFirstAndClears(t *testing.T) {
	path := journalPath(t)
	j, _, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, target := range []string{"first", "second", "third"} {
		if err := j.Record(Entry{Kind: KindRoute, Target: target, Restore: "undo " + target}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	var order []string
	err = j.RestoreAll(func(e Entry) error {
		order = append(order, e.Target)
		return nil
	})
	if err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
	want := []string{"third", "second", "first"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if j.Pending() {
		t.Fatal("the journal still has entries after a clean restore")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the journal file still exists: %v", err)
	}
}

// 恢复失败的条目会保留，下次启动时会再次尝试。
func TestFailedRestoreIsKept(t *testing.T) {
	path := journalPath(t)
	j, _, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, target := range []string{"ok", "stuck"} {
		if err := j.Record(Entry{Kind: KindDNS, Target: target}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	err = j.RestoreAll(func(e Entry) error {
		if e.Target == "stuck" {
			return errors.New("permission denied")
		}
		return nil
	})
	if err == nil {
		t.Fatal("a failed restore was reported as success")
	}
	if !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("error = %v, want it to name the stuck entry", err)
	}

	entries := j.Entries()
	if len(entries) != 1 || entries[0].Target != "stuck" {
		t.Fatalf("remaining = %+v, want only the stuck entry", entries)
	}
	// 而且它已写入磁盘，因此重试能跨重启保留。
	_, reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(reopened) != 1 || reopened[0].Target != "stuck" {
		t.Fatalf("reopened = %+v", reopened)
	}
}

// 带字节顺序标记的日志文件仍可读取。
//
// 手工编辑（或由编辑器而非 vRay 写入）的文件常带有 BOM；
// 因三个字节的前缀而丢失崩溃恢复能力实在荒谬。
func TestJournalToleratesByteOrderMark(t *testing.T) {
	path := journalPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := `[{"kind":"system-proxy","target":"E2E","restore":"none","backup":"direct"}]`
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, body...), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, entries, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(entries) != 1 || entries[0].Target != "E2E" {
		t.Fatalf("entries = %+v", entries)
	}
	if _, err := os.Stat(path + ".corrupt"); err == nil {
		t.Fatal("a readable file with a BOM was quarantined")
	}
}

// 损坏的日志会被隔离而不是删除：它是上一次运行所做操作的
// 唯一记录。
func TestCorruptJournalIsQuarantined(t *testing.T) {
	path := journalPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, entries, err := Open(path)
	if err == nil {
		t.Fatal("a corrupt journal opened without error")
	}
	if len(entries) != 0 {
		t.Fatalf("a corrupt journal produced entries: %+v", entries)
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("the corrupt file was not kept: %v", err)
	}
}

// 未知类型会被拒绝：静默跳过会让一项变更永久留在系统中。
func TestRecordRefusesUnknownKind(t *testing.T) {
	j, _, err := Open(journalPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := j.Record(Entry{Kind: "telepathy", Target: "x"}); err == nil {
		t.Fatal("an unknown kind was recorded")
	}
	if j.Pending() {
		t.Fatal("the refused entry was written anyway")
	}
}

// 没有待处理条目时，RecoverFromCrash 不执行任何操作。
func TestRecoverFromCrashNoop(t *testing.T) {
	recovered, err := RecoverFromCrash(journalPath(t), func(Entry) error {
		t.Fatal("the restorer ran with nothing to restore")
		return nil
	})
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	if len(recovered) != 0 {
		t.Fatalf("recovered = %+v", recovered)
	}
}

// RecoverFromCrash 会执行恢复并报告撤销内容，让外壳程序能告知用户，
// 而不是静默修改其设置。
func TestRecoverFromCrashReportsWhatItUndid(t *testing.T) {
	path := journalPath(t)
	j, _, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := j.Record(Entry{Kind: KindSystemProxy, Target: "Wi-Fi", Restore: "off"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := j.Record(Entry{Kind: KindDNS, Target: "Wi-Fi", Restore: "auto"}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var undid []string
	recovered, err := RecoverFromCrash(path, func(e Entry) error {
		undid = append(undid, string(e.Kind))
		return nil
	})
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	if len(recovered) != 2 {
		t.Fatalf("recovered = %d entries, want 2", len(recovered))
	}
	if strings.Join(undid, ",") != "dns,system-proxy" {
		t.Fatalf("undid = %v, want newest first", undid)
	}
}

// 机器上的其他用户不能读取该日志。
//
// Windows 没有 group/other 权限位可检查：此处的文件访问由
// 目录继承的 ACL 控制，因此该检查仅在 POSIX 系统上有意义。
func TestJournalIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not exist on Windows; access is inherited from the directory ACL")
	}
	path := journalPath(t)
	j, _, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := j.Record(Entry{Kind: KindTun, Target: "utun7"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("permissions = %o, want no group or other access", perm)
	}
}

// Clear 会在不执行撤销的情况下清除日志：供已经完成撤销的调用方使用。
func TestClear(t *testing.T) {
	path := journalPath(t)
	j, _, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := j.Record(Entry{Kind: KindRoute, Target: "0.0.0.0/0"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := j.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if j.Pending() {
		t.Fatal("Clear left entries")
	}
	if err := j.Clear(); err != nil {
		t.Fatalf("Clear twice: %v", err)
	}
}
