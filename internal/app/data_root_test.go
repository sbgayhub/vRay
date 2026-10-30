package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sbgayhub/vray/internal/paths"
)

// waitDataRootResult 等待目录操作结束，避免测试只看到保存中的中间状态。
func waitDataRootResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("等待数据目录操作超时")
		return nil
	}
}

// 选择新目录后只写入定位文件并发布可重启状态。
func TestSetDataRootWritesPendingSelection(t *testing.T) {
	svc, _ := newTestService(t)
	target := filepath.Join(t.TempDir(), "new-data")

	if err := waitDataRootResult(t, svc.SetDataRoot(target)); err != nil {
		t.Fatalf("SetDataRoot: %v", err)
	}
	snap := svc.State().Snapshot()
	if snap.DataRootSaving {
		t.Fatal("操作完成后仍显示保存中")
	}
	if !snap.DataRootPending {
		t.Fatal("成功后应标记为等待重启迁移")
	}
	if snap.DataRootError != "" {
		t.Fatalf("成功后出现错误：%s", snap.DataRootError)
	}
	if snap.DataRoot == target {
		t.Fatal("重启前不应提前切换当前数据目录")
	}

	raw, err := os.ReadFile(svc.paths.LocationFile)
	if err != nil {
		t.Fatalf("读取定位文件：%v", err)
	}
	var stored map[string]string
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("解析定位文件：%v", err)
	}
	if stored["root"] != target {
		t.Fatalf("定位文件 root = %q, want %q", stored["root"], target)
	}
	if stored["migrateFrom"] != svc.paths.Root {
		t.Fatalf("定位文件 migrateFrom = %q, want %q", stored["migrateFrom"], svc.paths.Root)
	}
}

// 目标已有 vRay 数据时拒绝，避免覆盖或混合两套配置。
func TestSetDataRootRejectsExistingDatabase(t *testing.T) {
	svc, _ := newTestService(t)
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, paths.DBFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := waitDataRootResult(t, svc.SetDataRoot(target))
	if err == nil {
		t.Fatal("已有数据库的目标目录不应被接受")
	}
	snap := svc.State().Snapshot()
	if snap.DataRootSaving {
		t.Fatal("失败后仍显示保存中")
	}
	if snap.DataRootPending {
		t.Fatal("失败不应标记为等待重启")
	}
	if !strings.Contains(snap.DataRootError, paths.DBFile) {
		t.Fatalf("错误未说明冲突文件：%q", snap.DataRootError)
	}
}

// 环境变量等固定模式下，服务层也必须拒绝绕过 UI 的修改请求。
func TestSetDataRootRejectsFixedMode(t *testing.T) {
	svc, _ := newTestService(t)
	svc.paths.Mode = paths.ModeEnvironment

	err := waitDataRootResult(t, svc.SetDataRoot(filepath.Join(t.TempDir(), "new-data")))
	if err == nil {
		t.Fatal("固定模式不应允许修改数据目录")
	}
	if !strings.Contains(svc.State().Snapshot().DataRootError, "VRAY_DATA") {
		t.Fatalf("错误未说明固定原因：%q", svc.State().Snapshot().DataRootError)
	}
}

// 恢复默认目录同样只写定位文件，并等待重启后迁移。
func TestResetDataRootWritesPendingSelection(t *testing.T) {
	svc, _ := newTestService(t)
	defaultRoot := filepath.Join(t.TempDir(), "default")
	svc.paths.DefaultRoot = defaultRoot
	svc.paths.LocationFile = filepath.Join(defaultRoot, paths.LocationName)
	svc.paths.Mode = paths.ModeCustom
	if err := os.MkdirAll(defaultRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultRoot, paths.DBFile), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := waitDataRootResult(t, svc.ResetDataRoot()); err != nil {
		t.Fatalf("ResetDataRoot: %v", err)
	}
	snap := svc.State().Snapshot()
	if !snap.DataRootPending || snap.DataRootSaving || snap.DataRootError != "" {
		t.Fatalf("恢复默认目录后的状态 = %+v", snap)
	}
	raw, err := os.ReadFile(svc.paths.LocationFile)
	if err != nil {
		t.Fatalf("读取定位文件：%v", err)
	}
	var stored map[string]string
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("解析定位文件：%v", err)
	}
	if stored["root"] != defaultRoot {
		t.Fatalf("定位文件 root = %q, want %q", stored["root"], defaultRoot)
	}
}

// 创建状态时发布默认目录、当前模式与可编辑性。
func TestLoadPublishesDataRootDetails(t *testing.T) {
	svc, _ := newTestService(t)
	snap := svc.State().Snapshot()
	if snap.DataRootDefault != svc.paths.DefaultRoot {
		t.Fatalf("DataRootDefault = %q, want %q", snap.DataRootDefault, svc.paths.DefaultRoot)
	}
	if !snap.DataRootEditable {
		t.Fatal("默认目录应可编辑")
	}
	if snap.DataRootNotice != "" {
		t.Fatalf("默认目录不应有警告：%q", snap.DataRootNotice)
	}
}
