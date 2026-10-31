package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *File {
	t.Helper()
	return NewFile(filepath.Join(t.TempDir(), "db.json"))
}

// 新存储会写入默认值，并能将其读回。
func TestLoadCreatesDefaults(t *testing.T) {
	s := newStore(t)
	d, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if d.SchemaVersion != SchemaVersion {
		t.Fatalf("schema = %d, want %d", d.SchemaVersion, SchemaVersion)
	}
	if d.Settings.MixedPort != DefaultSettings().MixedPort {
		t.Fatalf("settings not defaulted: %+v", d.Settings)
	}
	if d.Settings.LatencyConcurrency != DefaultSettings().LatencyConcurrency ||
		d.Settings.LatencyTimeoutMS != DefaultSettings().LatencyTimeoutMS {
		t.Fatalf("latency settings not defaulted: %+v", d.Settings)
	}
	if !d.Settings.AutoUpdateChecks {
		t.Fatal("new profiles should enable the once-daily update check")
	}
	if d.Settings.TermsAcceptedVersion != 0 {
		t.Fatal("new profiles must require the legal agreement")
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

// 节点配置可往返保存读取，更新会替换而不是重复添加。
func TestPutProfileInsertsThenUpdates(t *testing.T) {
	s := newStore(t)
	p := Profile{ID: "p1", Name: "tokyo", Protocol: ProtoVLESS, Address: "jp.example.com", Port: 443}
	if err := s.PutProfile(p); err != nil {
		t.Fatalf("PutProfile: %v", err)
	}
	p.Name = "tokyo-2"
	if err := s.PutProfile(p); err != nil {
		t.Fatalf("PutProfile update: %v", err)
	}

	d, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(d.Profiles) != 1 {
		t.Fatalf("%d profiles after update, want 1", len(d.Profiles))
	}
	if d.Profiles[0].Name != "tokyo-2" {
		t.Fatalf("name = %q, want tokyo-2", d.Profiles[0].Name)
	}
	if d.Profiles[0].UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt not stamped")
	}
}

// GetProfile 对未知 id 报告 ErrNotFound。
func TestGetProfileNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.GetProfile("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// 删除节点配置时只会移除指定的项。
func TestDeleteProfiles(t *testing.T) {
	s := newStore(t)
	for _, id := range []string{"a", "b", "c"} {
		if err := s.PutProfile(Profile{ID: id, Name: id}); err != nil {
			t.Fatalf("PutProfile %s: %v", id, err)
		}
	}
	if err := s.DeleteProfiles([]string{"b", "unknown"}); err != nil {
		t.Fatalf("DeleteProfiles: %v", err)
	}
	d, _ := s.Load()
	if len(d.Profiles) != 2 {
		t.Fatalf("%d profiles left, want 2", len(d.Profiles))
	}
	for _, p := range d.Profiles {
		if p.ID == "b" {
			t.Fatal("b was not deleted")
		}
	}
}

func TestNodeDetailsFollowRetainedProfileIDs(t *testing.T) {
	s := newStore(t)
	d, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	d.Profiles = []Profile{{ID: "a"}, {ID: "b"}}
	d.NodeDetails = map[string]NodeIPInfo{
		"a": {IP: "8.8.8.8"}, "b": {IP: "8.8.4.4"}, "orphan": {IP: "1.1.1.1"},
	}
	d.NodeSpeeds = map[string]DownloadSpeed{"a": {BytesPerSecond: 100}, "b": {BytesPerSecond: 200}, "orphan": {BytesPerSecond: 300}}
	if err := s.Save(d); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Load()
	if len(d.NodeDetails) != 2 || len(d.NodeSpeeds) != 2 {
		t.Fatal("orphan metadata was persisted")
	}
	if err := s.PutProfile(Profile{ID: "a", Name: "Changed", Address: "changed.example.com", Port: 443}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfiles([]string{"b"}); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Load()
	if len(d.NodeDetails) != 1 || d.NodeDetails["a"].IP != "8.8.8.8" {
		t.Fatal("profile edit or deletion did not preserve the correct metadata")
	}
	if len(d.NodeSpeeds) != 1 || d.NodeSpeeds["a"].BytesPerSecond != 100 {
		t.Fatal("profile edit/deletion did not retain the correct speed")
	}
	d.Profiles = nil
	if err := s.Save(d); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Load()
	if len(d.NodeDetails) != 0 || len(d.NodeSpeeds) != 0 {
		t.Fatal("whole-data profile removal left exit metadata behind")
	}
}

// 订阅源和设置可往返保存读取。
func TestSourcesAndSettings(t *testing.T) {
	s := newStore(t)
	if err := s.PutSource(Source{ID: "s1", Name: "sub", Enabled: true}); err != nil {
		t.Fatalf("PutSource: %v", err)
	}
	want := DefaultSettings()
	want.MixedPort = 7890
	if err := s.SetSettings(want); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}

	d, _ := s.Load()
	if len(d.Sources) != 1 || d.Sources[0].ID != "s1" {
		t.Fatalf("sources = %+v", d.Sources)
	}
	if d.Settings.MixedPort != 7890 {
		t.Fatalf("port = %d, want 7890", d.Settings.MixedPort)
	}

	if err := s.DeleteSource("s1"); err != nil {
		t.Fatalf("DeleteSource: %v", err)
	}
	if d, _ := s.Load(); len(d.Sources) != 0 {
		t.Fatal("source not deleted")
	}
}

// 来自更新版本构建的文件会被拒绝，而不会静默降级。
func TestMigrationRefusesNewerSchema(t *testing.T) {
	s := newStore(t)
	raw := []byte(`{"schemaVersion": 99, "settings": {"mixedPort": 2080}}`)
	if err := os.WriteFile(s.Path(), raw, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("expected an error for a newer schema")
	}
}

// 没有 schemaVersion（版本 0）的文件会被升级并补上默认值。
func TestMigrationFillsDefaultsForOldFile(t *testing.T) {
	s := newStore(t)
	raw := []byte(`{"profiles": [{"id": "p1", "name": "old"}]}`)
	if err := os.WriteFile(s.Path(), raw, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if d.SchemaVersion != SchemaVersion {
		t.Fatalf("schema = %d, want %d", d.SchemaVersion, SchemaVersion)
	}
	if d.Settings.MixedPort == 0 {
		t.Fatal("settings were not defaulted")
	}
	if d.Settings.LatencyConcurrency == 0 || d.Settings.LatencyTimeoutMS == 0 {
		t.Fatalf("latency settings were not defaulted: %+v", d.Settings)
	}
	if len(d.Profiles) != 1 {
		t.Fatal("profiles were lost")
	}
}

func TestMigrationSchemaOneEnablesDailyUpdateChecks(t *testing.T) {
	s := newStore(t)
	raw := []byte(`{"schemaVersion":1,"settings":{"mixedPort":2080,"autoUpdateChecks":false}}`)
	if err := os.WriteFile(s.Path(), raw, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if d.SchemaVersion != SchemaVersion {
		t.Fatalf("schema = %d, want %d", d.SchemaVersion, SchemaVersion)
	}
	if !d.Settings.AutoUpdateChecks {
		t.Fatal("schema 1 migration did not enable daily update checks")
	}
	if d.Settings.TermsAcceptedVersion != 0 {
		t.Fatal("migration accepted legal terms on the user's behalf")
	}
}

// 重复保存是安全的，并会留下有效 JSON。
func TestSaveIsRepeatableAndValid(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 50; i++ {
		if err := s.PutProfile(Profile{ID: "p", Name: "n"}); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	// 原子写入不会留下临时文件。
	entries, _ := os.ReadDir(filepath.Dir(s.Path()))
	for _, e := range entries {
		if e.Name() != "db.json" {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}
