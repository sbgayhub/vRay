package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Resolve 始终返回可写的数据目录。
func TestResolveIsWritable(t *testing.T) {
	t.Setenv(dataDirEnv, t.TempDir())
	p, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Root == "" {
		t.Fatal("empty data root")
	}
	if err := p.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for _, dir := range []string{p.Root, p.Logs()} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("%s missing: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}
}

// VRAY_DATA 覆盖必须优先于默认用户目录。
func TestResolveEnvOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "custom-data")
	t.Setenv(dataDirEnv, root)

	p, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Root != root {
		t.Fatalf("Resolve root = %q, want %q", p.Root, root)
	}
	if p.Migrated {
		t.Fatal("环境变量覆盖不应触发旧目录迁移")
	}
}

// 相对路径的 VRAY_DATA 以当前工作目录为基准，而不是可执行文件目录。
func TestResolveRelativeEnvOverride(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	t.Setenv(dataDirEnv, "custom-data")

	p, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(work, "custom-data"); p.Root != want {
		t.Fatalf("Resolve root = %q, want %q", p.Root, want)
	}
}

// 各文件路径都挂在 Root 下，并使用约定名称。
func TestFileLayout(t *testing.T) {
	p := Paths{Root: filepath.Join("base", "data")}
	cases := map[string]string{
		p.DB():      "vray.json",
		p.Secrets(): "secrets.enc",
		p.Effects(): "effects.json",
		p.GeoIP():   "geoip.dat",
		p.GeoSite(): "geosite.dat",
		p.Logs():    "logs",
	}
	for got, want := range cases {
		if filepath.Base(got) != want {
			t.Fatalf("path %q does not end in %q", got, want)
		}
		if filepath.Dir(got) != p.Root {
			t.Fatalf("path %q is not under %q", got, p.Root)
		}
	}
}

// 只有两个 geo 文件都存在时 HasGeoData 才为真。
func TestHasGeoData(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Root: dir}

	if p.HasGeoData() {
		t.Fatal("geo data reported present in an empty directory")
	}

	if err := os.WriteFile(p.GeoIP(), []byte("x"), 0o644); err != nil {
		t.Fatalf("write geoip: %v", err)
	}
	if p.HasGeoData() {
		t.Fatal("geo data reported present with only one file")
	}

	if err := os.WriteFile(p.GeoSite(), []byte("x"), 0o644); err != nil {
		t.Fatalf("write geosite: %v", err)
	}
	if !p.HasGeoData() {
		t.Fatal("geo data reported missing with both files present")
	}
}

// go run 检测器只对构建缓存路径生效。
func TestIsTempBuildDir(t *testing.T) {
	yes := []string{
		filepath.Join("/tmp", "go-build123", "b001", "exe"),
		filepath.Join("C:", "Users", "x", "AppData", "Local", "Temp", "go-build2048361161", "b001"),
	}
	no := []string{
		filepath.Join("/opt", "vray"),
		filepath.Join("C:", "Program Files", "vRay"),
		filepath.Join("/home", "me", "build"),
	}
	for _, d := range yes {
		if !isTempBuildDir(d) {
			t.Fatalf("isTempBuildDir(%q) = false, want true", d)
		}
	}
	for _, d := range no {
		if isTempBuildDir(d) {
			t.Fatalf("isTempBuildDir(%q) = true, want false", d)
		}
	}
}

// 迁移会把旧目录的内容复制到新目录，并在已有数据库时停止重复复制。
func TestMigrateLegacy(t *testing.T) {
	legacy := t.TempDir()
	root := filepath.Join(t.TempDir(), "vRay")
	if err := os.WriteFile(filepath.Join(legacy, DBFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(legacy, LogsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, LogsDir, "a.log"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !migrateLegacy(legacy, root) {
		t.Fatal("首次迁移应返回 true")
	}
	for _, path := range []string{filepath.Join(root, DBFile), filepath.Join(root, LogsDir, "a.log")} {
		if !fileExists(path) {
			t.Fatalf("迁移后缺少 %s", path)
		}
	}
	if migrateLegacy(legacy, root) {
		t.Fatal("已有数据库时不应再次迁移")
	}
}

// 没有已知数据文件的旧目录不迁移，避免复制无关文件。
func TestMigrateLegacySkipsWithoutDatabase(t *testing.T) {
	legacy := t.TempDir()
	root := filepath.Join(t.TempDir(), "vRay")
	if err := os.WriteFile(filepath.Join(legacy, "other.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if migrateLegacy(legacy, root) {
		t.Fatal("缺少数据库时不应迁移")
	}
	if fileExists(filepath.Join(root, "other.txt")) {
		t.Fatal("无关文件不应被复制")
	}
}

// 只有 geo 数据时也应迁移，避免用户重新下载约 28 MB 文件。
func TestMigrateLegacyWithGeoOnly(t *testing.T) {
	legacy := t.TempDir()
	root := filepath.Join(t.TempDir(), "vRay")
	if err := os.WriteFile(filepath.Join(legacy, GeoIPFile), []byte("geoip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, GeoSiteFile), []byte("geosite"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !migrateLegacy(legacy, root) {
		t.Fatal("geo 数据应迁移到新目录")
	}
	if !fileExists(filepath.Join(root, GeoIPFile)) || !fileExists(filepath.Join(root, GeoSiteFile)) {
		t.Fatal("迁移后缺少 geo 数据")
	}
}

// 合并迁移不能覆盖新目录中已经写入的文件。
func TestMigrateLegacyPreservesExistingFiles(t *testing.T) {
	legacy := t.TempDir()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, DBFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, GeoIPFile), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, GeoIPFile), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !migrateLegacy(legacy, root) {
		t.Fatal("缺少其他文件时仍应执行合并迁移")
	}
	got, err := os.ReadFile(filepath.Join(root, GeoIPFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("已有文件被覆盖为 %q", got)
	}
	if !fileExists(filepath.Join(root, DBFile)) {
		t.Fatal("缺失文件应在合并迁移中补齐")
	}
}

// 用户数据目录以应用名结尾。
func TestUserDataRootUsesAppName(t *testing.T) {
	root, err := userDataRoot()
	if err != nil {
		t.Fatalf("userDataRoot: %v", err)
	}
	if filepath.Base(root) != "vRay" {
		t.Fatalf("userDataRoot = %q, 应以 vRay 结尾", root)
	}
	if runtime.GOOS == "windows" {
		cache, err := os.UserCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(cache, "vRay"); root != want {
			t.Fatalf("userDataRoot = %q, want %q", root, want)
		}
	}
}

// 定位文件可以把下一次启动恢复到用户选择的数据目录。
func TestResolveUserUsesCustomLocation(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "app", "data")
	defaultRoot := filepath.Join(t.TempDir(), "default")
	customRoot := filepath.Join(t.TempDir(), "custom")
	if err := os.MkdirAll(customRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeLocation(filepath.Join(defaultRoot, LocationName), location{
		Root: customRoot,
	}); err != nil {
		t.Fatal(err)
	}

	p, err := resolveUser(legacy, defaultRoot)
	if err != nil {
		t.Fatalf("resolveUser: %v", err)
	}
	if p.Root != customRoot {
		t.Fatalf("Root = %q, want %q", p.Root, customRoot)
	}
	if p.DefaultRoot != defaultRoot {
		t.Fatalf("DefaultRoot = %q, want %q", p.DefaultRoot, defaultRoot)
	}
	if p.Mode != ModeCustom {
		t.Fatalf("Mode = %q, want %q", p.Mode, ModeCustom)
	}
	if p.Migrated {
		t.Fatal("没有待迁移来源时不应报告迁移")
	}
	if !p.Editable() {
		t.Fatal("自定义目录仍应允许再次修改")
	}
}

// 启动时会先复制待迁移文件，再清理定位文件中的待迁移标记。
func TestResolveUserMigratesPendingLocation(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "app", "data")
	defaultRoot := filepath.Join(t.TempDir(), "default")
	customRoot := filepath.Join(t.TempDir(), "custom")
	if err := os.MkdirAll(filepath.Join(defaultRoot, LogsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultRoot, DBFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultRoot, LogsDir, "vray.log"), []byte("日志"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeLocation(filepath.Join(defaultRoot, LocationName), location{
		Root:        customRoot,
		MigrateFrom: defaultRoot,
	}); err != nil {
		t.Fatal(err)
	}

	p, err := resolveUser(legacy, defaultRoot)
	if err != nil {
		t.Fatalf("resolveUser: %v", err)
	}
	if p.Root != customRoot || !p.Migrated || p.MigrationSource != defaultRoot {
		t.Fatalf("迁移结果 = %+v", p)
	}
	for _, name := range []string{DBFile, filepath.Join(LogsDir, "vray.log")} {
		if !fileExists(filepath.Join(customRoot, name)) {
			t.Fatalf("目标目录缺少 %s", name)
		}
	}
	if fileExists(filepath.Join(customRoot, LocationName)) {
		t.Fatal("定位文件不应被复制到数据目录")
	}
	if !fileExists(filepath.Join(defaultRoot, DBFile)) {
		t.Fatal("迁移后原目录应保留")
	}
	loc, found, err := readLocation(filepath.Join(defaultRoot, LocationName))
	if err != nil || !found {
		t.Fatalf("读取定位文件失败：found=%v err=%v", found, err)
	}
	if loc.MigrateFrom != "" {
		t.Fatalf("待迁移标记未清除：%q", loc.MigrateFrom)
	}
}

// 迁回默认目录后复制文件并删除无用的定位文件。
func TestResolveUserReturnsToDefaultLocation(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "app", "data")
	defaultRoot := filepath.Join(t.TempDir(), "default")
	customRoot := filepath.Join(t.TempDir(), "custom")
	if err := os.MkdirAll(customRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customRoot, DBFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	locationPath := filepath.Join(defaultRoot, LocationName)
	if err := writeLocation(locationPath, location{
		Root:        defaultRoot,
		MigrateFrom: customRoot,
	}); err != nil {
		t.Fatal(err)
	}

	p, err := resolveUser(legacy, defaultRoot)
	if err != nil {
		t.Fatalf("resolveUser: %v", err)
	}
	if p.Root != defaultRoot || p.Mode != ModeDefault || !p.Migrated {
		t.Fatalf("迁回结果 = %+v", p)
	}
	if !fileExists(filepath.Join(defaultRoot, DBFile)) {
		t.Fatal("默认目录缺少迁移后的数据库")
	}
	if pathExists(locationPath) {
		t.Fatal("迁回默认目录后应清理定位文件")
	}
	if !fileExists(filepath.Join(customRoot, DBFile)) {
		t.Fatal("迁回后原自定义目录应保留")
	}
}

// 新目录与当前目录互相包含时拒绝切换，避免递归复制。
func TestSetCustomRootRejectsOverlappingDirectories(t *testing.T) {
	current := filepath.Join(t.TempDir(), "current")
	defaultRoot := filepath.Join(t.TempDir(), "default")
	p := Paths{
		Root:         current,
		DefaultRoot:  defaultRoot,
		LocationFile: filepath.Join(defaultRoot, LocationName),
		Mode:         ModeDefault,
	}
	for _, target := range []string{
		current,
		filepath.Join(current, "nested"),
		filepath.Dir(current),
		filepath.Join(defaultRoot, "nested"),
	} {
		if err := p.SetCustomRoot(target); err == nil {
			t.Fatalf("应拒绝重叠目录 %q", target)
		}
	}
}

// 目标已有状态文件时拒绝合并，避免两套真实数据悄悄混在一起。
func TestSetCustomRootRejectsExistingState(t *testing.T) {
	for _, name := range []string{DBFile, SecretsFile, EffectsFile} {
		t.Run(name, func(t *testing.T) {
			current := filepath.Join(t.TempDir(), "current")
			defaultRoot := filepath.Join(t.TempDir(), "default")
			target := filepath.Join(t.TempDir(), "target")
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, name), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			p := Paths{
				Root:         current,
				DefaultRoot:  defaultRoot,
				LocationFile: filepath.Join(defaultRoot, LocationName),
				Mode:         ModeDefault,
			}
			if err := p.SetCustomRoot(target); err == nil {
				t.Fatalf("已有 %s 的目标目录不应被接受", name)
			}
		})
	}
}

// 选择目录只写定位文件，真实复制留到下一次启动。
func TestSetCustomRootWritesPendingLocation(t *testing.T) {
	current := filepath.Join(t.TempDir(), "current")
	defaultRoot := filepath.Join(t.TempDir(), "default")
	target := filepath.Join(t.TempDir(), "target")
	p := Paths{
		Root:         current,
		DefaultRoot:  defaultRoot,
		LocationFile: filepath.Join(defaultRoot, LocationName),
		Mode:         ModeDefault,
	}
	if err := p.SetCustomRoot(target); err != nil {
		t.Fatalf("SetCustomRoot: %v", err)
	}
	loc, found, err := readLocation(p.LocationFile)
	if err != nil || !found {
		t.Fatalf("读取定位文件失败：found=%v err=%v", found, err)
	}
	if loc.Root != target || loc.MigrateFrom != current {
		t.Fatalf("定位文件 = %+v", loc)
	}
}

// 设置页写入的定位文件必须在模拟重启时真的触发一次迁移。
func TestSetCustomRootThenResolveMigrates(t *testing.T) {
	current := filepath.Join(t.TempDir(), "current")
	defaultRoot := filepath.Join(t.TempDir(), "default")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(current, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, DBFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Paths{
		Root:         current,
		DefaultRoot:  defaultRoot,
		LocationFile: filepath.Join(defaultRoot, LocationName),
		Mode:         ModeDefault,
	}
	if err := p.SetCustomRoot(target); err != nil {
		t.Fatalf("SetCustomRoot: %v", err)
	}

	resolved, err := resolveUser(filepath.Join(t.TempDir(), "legacy"), defaultRoot)
	if err != nil {
		t.Fatalf("resolveUser: %v", err)
	}
	if resolved.Root != target || !resolved.Migrated {
		t.Fatalf("重启结果 = %+v", resolved)
	}
	if !fileExists(filepath.Join(target, DBFile)) {
		t.Fatal("重启后目标目录缺少数据库")
	}
}

// 从自定义目录恢复默认目录时也写入待迁移来源。
func TestSetDefaultRootWritesPendingLocation(t *testing.T) {
	current := filepath.Join(t.TempDir(), "custom")
	defaultRoot := filepath.Join(t.TempDir(), "default")
	p := Paths{
		Root:         current,
		DefaultRoot:  defaultRoot,
		LocationFile: filepath.Join(defaultRoot, LocationName),
		Mode:         ModeCustom,
	}
	if err := p.SetDefaultRoot(); err != nil {
		t.Fatalf("SetDefaultRoot: %v", err)
	}
	loc, found, err := readLocation(p.LocationFile)
	if err != nil || !found {
		t.Fatalf("读取定位文件失败：found=%v err=%v", found, err)
	}
	if loc.Root != defaultRoot || loc.MigrateFrom != current {
		t.Fatalf("定位文件 = %+v", loc)
	}
}

// 默认目录保留第一次迁出前的旧数据时，恢复默认目录仍应成功写入选址。
func TestSetDefaultRootAllowsExistingDefaultData(t *testing.T) {
	current := filepath.Join(t.TempDir(), "custom")
	defaultRoot := filepath.Join(t.TempDir(), "default")
	if err := os.MkdirAll(defaultRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultRoot, DBFile), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Paths{
		Root:         current,
		DefaultRoot:  defaultRoot,
		LocationFile: filepath.Join(defaultRoot, LocationName),
		Mode:         ModeCustom,
	}
	if err := p.SetDefaultRoot(); err != nil {
		t.Fatalf("SetDefaultRoot: %v", err)
	}
	loc, found, err := readLocation(p.LocationFile)
	if err != nil || !found {
		t.Fatalf("读取定位文件失败：found=%v err=%v", found, err)
	}
	if loc.Root != defaultRoot || loc.MigrateFrom != current {
		t.Fatalf("定位文件 = %+v", loc)
	}
}

// 迁回默认目录前先备份旧默认数据，避免复制跳过后继续读到旧数据库。
func TestResolveUserBacksUpDefaultDataBeforeReturning(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), "app", "data")
	defaultRoot := filepath.Join(t.TempDir(), "default")
	customRoot := filepath.Join(t.TempDir(), "custom")
	if err := os.MkdirAll(defaultRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(defaultRoot, LogsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultRoot, DBFile), []byte("old-db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defaultRoot, LogsDir, "old.log"), []byte("old-log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(customRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customRoot, DBFile), []byte("new-db"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeLocation(filepath.Join(defaultRoot, LocationName), location{
		Root:        defaultRoot,
		MigrateFrom: customRoot,
	}); err != nil {
		t.Fatal(err)
	}

	p, err := resolveUser(legacy, defaultRoot)
	if err != nil {
		t.Fatalf("resolveUser: %v", err)
	}
	if p.MigrationBackup == "" {
		t.Fatal("迁回默认目录后应报告旧数据备份位置")
	}
	if !strings.Contains(p.Warning, p.MigrationBackup) {
		t.Fatalf("警告未包含备份位置：%q", p.Warning)
	}
	db, err := os.ReadFile(filepath.Join(defaultRoot, DBFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(db) != "new-db" {
		t.Fatalf("默认目录数据库 = %q, want new-db", db)
	}
	oldDB, err := os.ReadFile(filepath.Join(p.MigrationBackup, DBFile))
	if err != nil {
		t.Fatalf("备份目录缺少旧数据库：%v", err)
	}
	if string(oldDB) != "old-db" {
		t.Fatalf("备份数据库 = %q, want old-db", oldDB)
	}
	if _, err := os.Stat(filepath.Join(p.MigrationBackup, LogsDir, "old.log")); err != nil {
		t.Fatalf("备份目录缺少旧日志：%v", err)
	}
	if pathExists(filepath.Join(defaultRoot, LocationName)) {
		t.Fatal("迁回默认目录后应清理定位文件")
	}
}

// 环境变量、便携和开发态固定选择时，设置页不能覆盖。
func TestFixedModesCannotChangeDataRoot(t *testing.T) {
	for _, mode := range []Mode{ModeEnvironment, ModeDevelopment, ModePortable, ModeFallback} {
		p := Paths{
			Root:         filepath.Join(t.TempDir(), "current"),
			DefaultRoot:  filepath.Join(t.TempDir(), "default"),
			LocationFile: filepath.Join(t.TempDir(), "location.json"),
			Mode:         mode,
		}
		if p.Editable() {
			t.Fatalf("%s 模式不应允许修改", mode)
		}
		if p.ChangeDisabledReason() == "" {
			t.Fatalf("%s 模式缺少中文禁用原因", mode)
		}
		if err := p.SetCustomRoot(filepath.Join(t.TempDir(), "target")); err == nil {
			t.Fatalf("%s 模式不应写入定位文件", mode)
		}
	}
}
