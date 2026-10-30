package effects

// 仅凭文件也必须能从崩溃中恢复。
//
// 这些测试不会复用内存中的 Journal：每个阶段都像新进程一样打开文件。
// 这是验证第三层恢复机制的唯一方式，因为写入条目的进程正是
// 该机制所防范场景中已经消失的那个进程。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// 完整的崩溃恢复周期：记录、“崩溃”、重新打开、恢复、消失。
func TestCrashCycleThroughTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "effects.json")

	// 阶段 1：应用运行并修改机器状态。
	run1, _, err := Open(path)
	if err != nil {
		t.Fatalf("Open run1: %v", err)
	}
	if err := run1.Record(Entry{
		Kind: KindSystemProxy, Target: "WinINET",
		Restore: "disable", Backup: "direct",
	}); err != nil {
		t.Fatalf("Record proxy: %v", err)
	}
	if err := run1.Record(Entry{
		Kind: KindDNS, Target: "Wi-Fi",
		Restore: "dhcp", Backup: "automatic",
	}); err != nil {
		t.Fatalf("Record dns: %v", err)
	}
	// 没有 Cleanup，也没有 defer：进程在此处被终止。

	// 阶段 2：新进程启动并发现恢复日志。
	var undone []Entry
	recovered, err := RecoverFromCrash(path, func(e Entry) error {
		undone = append(undone, e)
		return nil
	})
	if err != nil {
		t.Fatalf("RecoverFromCrash: %v", err)
	}
	if len(recovered) != 2 {
		t.Fatalf("recovered %d entries, want 2", len(recovered))
	}
	// DNS 是第二个记录的，因此最先撤销。
	if undone[0].Kind != KindDNS || undone[1].Kind != KindSystemProxy {
		t.Fatalf("undone order = %v, want dns then system-proxy", undone)
	}
	// 备份随条目一起保存，因此才能在另一个进程中完成
	// 属于写入（而非命令）的恢复。
	if undone[0].Backup != "automatic" || undone[1].Backup != "direct" {
		t.Fatalf("backups lost: %+v", undone)
	}

	// 阶段 3：恢复日志已消失，因此下次启动是干净的。
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the journal survived a clean recovery: %v", err)
	}
	again, err := RecoverFromCrash(path, func(Entry) error {
		t.Fatal("a second recovery found entries")
		return nil
	})
	if err != nil || len(again) != 0 {
		t.Fatalf("second recovery = %+v, %v", again, err)
	}
}

// 恢复到一半失败时，剩余条目会留待下次处理，
// 已撤销的条目也不会丢失。
func TestPartialRestoreKeepsOnlyTheFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "effects.json")
	j, _, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, target := range []string{"a", "b", "c"} {
		if err := j.Record(Entry{Kind: KindRoute, Target: target}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}

	// "b" 失败；"a" 和 "c" 成功。
	err = j.RestoreAll(func(e Entry) error {
		if e.Target == "b" {
			return errors.New("route busy")
		}
		return nil
	})
	if err == nil {
		t.Fatal("the failure was not reported")
	}

	// 新进程只会看到剩下的一条条目。
	_, remaining, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Target != "b" {
		t.Fatalf("remaining = %+v, want only b", remaining)
	}
}

// 条目不仅在内存中保持顺序，通过文件后仍保持顺序。
func TestOrderSurvivesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "effects.json")
	j, _, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, target := range []string{"one", "two", "three"} {
		if err := j.Record(Entry{Kind: KindTun, Target: target}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	_, entries, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(entries) != 3 || entries[0].Target != "one" || entries[2].Target != "three" {
		t.Fatalf("entries = %+v", entries)
	}
}
