// Package paths 决定 vRay 把文件保存在哪里。
//
// 默认使用当前用户的数据目录：Windows 为 %LOCALAPPDATA%\vRay，
// Linux 为 ~/.config/vRay，macOS 为 ~/Library/Application Support/vRay。
// 这样安装目录只读、多用户隔离和卸载清理都符合平台习惯，也避免把
// 约 28 MB 的 geo 数据放进会跨设备同步的漫游目录。
//
// 用户可以在设置中选择其他数据目录。自定义位置写入默认目录中的
// location.json，因此解析路径时不依赖任何已经打开的配置或数据库。
// 需要便携使用时，在可执行文件旁放一个名为 `portable` 的空文件，
// 数据就会落到可执行文件旁的 `data` 目录。环境变量 VRAY_DATA 可以
// 显式指定任意目录，优先级最高。首次切换到新目录时，旧目录中的文件
// 会在下一次启动、打开数据库之前复制过去，原目录保留。迁回默认目录时，
// 默认目录中的旧数据会先移到同级备份目录，避免两套状态混用。
package paths

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// 数据目录内的文件名。
const (
	DBFile      = "vray.json"
	SecretsFile = "secrets.enc"
	EffectsFile = "effects.json"
	GeoIPFile   = "geoip.dat"
	GeoSiteFile = "geosite.dat"
	LogsDir     = "logs"
)

// LocationName 是默认数据目录中的自定义位置文件。
//
// 它不能放进自定义目录本身：启动时必须先找到数据目录，才能读取
// 其中的任何配置。
const LocationName = "location.json"

// 便携模式标记文件与数据目录覆盖变量。
const (
	portableMarker = "portable"
	dataDirEnv     = "VRAY_DATA"
)

// Mode 表示本次运行的数据目录是如何选定的。
type Mode string

const (
	ModeDefault     Mode = "default"
	ModeCustom      Mode = "custom"
	ModeEnvironment Mode = "environment"
	ModeDevelopment Mode = "development"
	ModePortable    Mode = "portable"
	ModeFallback    Mode = "fallback"
)

// Paths 是一次运行解析出的全部位置。
type Paths struct {
	// Root 是数据目录本身。
	Root string
	// DefaultRoot 是平台默认数据目录，也是 location.json 所在目录。
	DefaultRoot string
	// LocationFile 是保存自定义目录选择的稳定定位文件。
	LocationFile string
	// Mode 说明当前目录由哪种规则选定。
	Mode Mode
	// Warning 记录不阻止启动、但应告知用户的问题。
	Warning string
	// Migrated 表示本次启动完成了待迁移目录的复制。
	Migrated bool
	// MigrationSource 是本次迁移的来源目录。
	MigrationSource string
	// MigrationBackup 是迁回默认目录时旧数据的备份目录。
	MigrationBackup string
}

// Resolve 选择数据目录。
//
// 优先级从高到低：
//  1. VRAY_DATA 环境变量；
//  2. `go run` 的临时构建目录使用当前工作目录（开发态）；
//  3. 可执行文件旁存在 `portable` 标记时使用 `<exe>/data`；
//  4. 当前用户标准数据目录中的 location.json 指定的自定义目录；
//  5. 当前用户的标准数据目录。
//
// 用户目录不可写时退回可执行文件旁的 data；两者都不可写才报错。
func Resolve() (Paths, error) {
	// 显式覆盖优先于开发态和便携模式，保证自动化与调试行为可预测。
	if dir := strings.TrimSpace(os.Getenv(dataDirEnv)); dir != "" {
		if !filepath.IsAbs(dir) {
			wd, err := os.Getwd()
			if err != nil {
				return Paths{}, fmt.Errorf("路径：解析 %s 相对路径失败：%w", dataDirEnv, err)
			}
			dir = filepath.Join(wd, dir)
		}
		return fixedPaths(filepath.Clean(dir), ModeEnvironment, "")
	}

	exe, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("路径：定位可执行文件失败：%w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return Paths{}, fmt.Errorf("路径：解析可执行文件绝对路径失败：%w", err)
	}
	base := filepath.Dir(exe)

	// 开发态：go run 的二进制在临时构建目录，数据落到工作目录。
	if isTempBuildDir(base) {
		wd, err := os.Getwd()
		if err != nil {
			return Paths{}, fmt.Errorf("路径：读取工作目录失败：%w", err)
		}
		return fixedPaths(filepath.Join(wd, "data"), ModeDevelopment, "")
	}

	if fileExists(filepath.Join(base, portableMarker)) {
		return fixedPaths(filepath.Join(base, "data"), ModePortable, "")
	}

	userRoot, err := userDataRoot()
	if err != nil {
		p, fallbackErr := fixedPaths(filepath.Join(base, "data"), ModeFallback, "")
		if fallbackErr != nil {
			return Paths{}, fallbackErr
		}
		p.Warning = fmt.Sprintf("无法确定用户数据目录，已回退到程序旁目录：%v", err)
		return p, nil
	}
	return resolveUser(filepath.Join(base, "data"), userRoot)
}

// resolveUser 从默认目录和其中的定位文件解析最终数据目录。
//
// 单独抽出这个函数，使测试可以为默认目录使用临时路径，而不会写入
// 开发机的真实用户目录。
func resolveUser(legacy, defaultRoot string) (Paths, error) {
	defaultRoot = filepath.Clean(defaultRoot)
	locationPath := filepath.Join(defaultRoot, LocationName)

	loc, found, err := readLocation(locationPath)
	if err != nil {
		return Paths{}, err
	}
	if !found {
		if err := ensureWritable(defaultRoot); err != nil {
			return fallbackPaths(legacy, defaultRoot, err)
		}
		migrated := migrateLegacy(legacy, defaultRoot)
		p := Paths{
			Root:         defaultRoot,
			DefaultRoot:  defaultRoot,
			LocationFile: locationPath,
			Mode:         ModeDefault,
			Migrated:     migrated,
		}
		if migrated {
			p.MigrationSource = legacy
		}
		return p, nil
	}

	root, err := normalizeRoot(loc.Root, defaultRoot)
	if err != nil {
		return Paths{}, fmt.Errorf("路径：定位文件中的自定义目录无效：%w", err)
	}
	if !samePath(root, defaultRoot) &&
		(pathWithin(root, defaultRoot) || pathWithin(defaultRoot, root)) {
		return Paths{}, errors.New("路径：自定义数据目录不能与默认数据目录互相包含")
	}
	if err := ensureWritable(root); err != nil {
		return Paths{}, fmt.Errorf("路径：自定义数据目录不可写 %s：%w", root, err)
	}

	mode := ModeCustom
	if samePath(root, defaultRoot) {
		mode = ModeDefault
	}
	p := Paths{
		Root:         root,
		DefaultRoot:  defaultRoot,
		LocationFile: locationPath,
		Mode:         mode,
	}

	migrationCleared := true
	if strings.TrimSpace(loc.MigrateFrom) != "" {
		source, err := normalizeRoot(loc.MigrateFrom, defaultRoot)
		if err != nil {
			return Paths{}, fmt.Errorf("路径：定位文件中的迁移来源无效：%w", err)
		}
		if !samePath(source, root) {
			copied, backup, err := migratePending(source, root, defaultRoot)
			if err != nil {
				return Paths{}, err
			}
			p.Migrated = copied
			p.MigrationBackup = backup
			if copied {
				p.MigrationSource = source
			}
			if backup != "" {
				p.Warning = joinWarning(p.Warning, fmt.Sprintf("默认目录中的旧数据已备份到 %s", backup))
			}
		}
		loc.MigrateFrom = ""
		if err := writeLocation(locationPath, loc); err != nil {
			migrationCleared = false
			p.Warning = joinWarning(p.Warning, fmt.Sprintf("数据已迁移，但无法清除待迁移标记：%v", err))
		}
	}

	// 回到默认目录后不再需要定位文件，避免下次启动留下无意义的间接层。
	if mode == ModeDefault && migrationCleared && loc.MigrateFrom == "" {
		if err := os.Remove(locationPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.Warning = joinWarning(p.Warning, fmt.Sprintf("无法清理数据目录定位文件：%v", err))
		}
	}
	return p, nil
}

// fallbackPaths 在默认用户目录不可用时回退到程序旁目录。
func fallbackPaths(legacy, defaultRoot string, cause error) (Paths, error) {
	p, err := fixedPaths(legacy, ModeFallback, defaultRoot)
	if err != nil {
		return Paths{}, err
	}
	p.Warning = fmt.Sprintf(
		"用户数据目录不可写，已回退到程序旁目录 %s：%v",
		p.Root, cause,
	)
	return p, nil
}

// fixedPaths 返回由环境、便携或开发态固定的数据目录。
func fixedPaths(root string, mode Mode, defaultRoot string) (Paths, error) {
	if err := ensureWritable(root); err != nil {
		return Paths{}, fmt.Errorf("路径：数据目录不可写 %s：%w", root, err)
	}
	p := Paths{Root: root, DefaultRoot: defaultRoot, Mode: mode}
	if defaultRoot != "" {
		p.LocationFile = filepath.Join(defaultRoot, LocationName)
	}
	return p, nil
}

// userDataRoot 返回平台惯用的每用户数据目录。
//
// Windows 使用 LocalAppData 而不是 Roaming：geo 数据接近 28 MB，
// 不应在域账户之间漫游同步。其余平台使用 UserConfigDir，
// 即 Linux 的 ~/.config 与 macOS 的 Application Support。
func userDataRoot() (string, error) {
	if runtime.GOOS == "windows" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "vRay"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "vRay"), nil
}

// isTempBuildDir 报告 dir 是否位于 go 的构建缓存中，也就是 go run 的临时目录。
// 只识别 go-build 前缀，避免误判普通构建目录。
func isTempBuildDir(dir string) bool {
	dir = filepath.Clean(dir)
	parts := strings.Split(dir, string(filepath.Separator))
	for _, part := range parts {
		if strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	return false
}

// ensureWritable 创建 dir 并实际写入一个探针文件，证明目录可写。
func ensureWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".write-probe-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	probe.Close()
	return os.Remove(name)
}

// location 是 location.json 的磁盘格式。
type location struct {
	Root        string `json:"root"`
	MigrateFrom string `json:"migrateFrom,omitempty"`
}

// readLocation 读取定位文件；不存在并不算错误。
func readLocation(path string) (location, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return location{}, false, nil
	}
	if err != nil {
		return location{}, false, fmt.Errorf("路径：读取数据目录定位文件失败：%w", err)
	}
	var loc location
	if err := json.Unmarshal(raw, &loc); err != nil {
		return location{}, false, fmt.Errorf("路径：解析数据目录定位文件 %s 失败：%w", path, err)
	}
	if strings.TrimSpace(loc.Root) == "" {
		return location{}, false, fmt.Errorf("路径：数据目录定位文件 %s 缺少 root", path)
	}
	return loc, true, nil
}

// writeLocation 原子写入定位文件，避免断电后留下半截 JSON。
func writeLocation(path string, loc location) error {
	raw, err := json.MarshalIndent(loc, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".location-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	remove := true
	defer func() {
		_ = temp.Close()
		if remove {
			_ = os.Remove(name)
		}
	}()

	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	remove = false
	return nil
}

// SetCustomRoot 记录下一次启动要使用的数据目录。
//
// 这里只写入定位文件，不移动正在使用的文件；真正的复制由下一次
// Resolve 在数据库、日志和密钥文件打开之前完成。
func (p Paths) SetCustomRoot(target string) error {
	if !p.Editable() {
		return errors.New(p.ChangeDisabledReason())
	}
	current, err := normalizeRoot(p.Root, "")
	if err != nil {
		return fmt.Errorf("路径：当前数据目录无效：%w", err)
	}
	target, err = normalizeRoot(target, "")
	if err != nil {
		return fmt.Errorf("路径：新数据目录无效：%w", err)
	}
	return p.setRoot(current, target)
}

// SetDefaultRoot 记录下一次启动迁回平台默认数据目录。
func (p Paths) SetDefaultRoot() error {
	if !p.Editable() {
		return errors.New(p.ChangeDisabledReason())
	}
	current, err := normalizeRoot(p.Root, "")
	if err != nil {
		return fmt.Errorf("路径：当前数据目录无效：%w", err)
	}
	target, err := normalizeRoot(p.DefaultRoot, "")
	if err != nil {
		return fmt.Errorf("路径：默认数据目录无效：%w", err)
	}
	if samePath(current, target) {
		return errors.New("当前已经使用默认数据目录")
	}
	return p.setRoot(current, target)
}

// setRoot 校验目录关系、写入定位文件，并留下待迁移来源。
func (p Paths) setRoot(current, target string) error {
	current = canonicalPath(current)
	target = canonicalPath(target)
	defaultRoot := canonicalPath(p.DefaultRoot)
	locationPath := p.locationPath()
	if locationPath == "" {
		return errors.New("当前数据目录不支持修改")
	}
	if samePath(current, target) {
		return errors.New("新数据目录不能与当前数据目录相同")
	}
	if pathWithin(target, current) || pathWithin(current, target) {
		return errors.New("新旧数据目录不能互相包含，请选择彼此独立的目录")
	}
	if !samePath(target, defaultRoot) &&
		(pathWithin(target, defaultRoot) || pathWithin(defaultRoot, target)) {
		return errors.New("新数据目录不能与默认数据目录互相包含")
	}
	if !samePath(target, defaultRoot) {
		for _, name := range []string{DBFile, SecretsFile, EffectsFile} {
			if pathExists(filepath.Join(target, name)) {
				return fmt.Errorf("目标目录已包含 vRay 数据（%s），请选择其他目录", name)
			}
		}
	}
	if err := ensureWritable(target); err != nil {
		return fmt.Errorf("新数据目录不可写 %s：%w", target, err)
	}

	if err := writeLocation(locationPath, location{
		Root:        target,
		MigrateFrom: current,
	}); err != nil {
		return fmt.Errorf("保存数据目录设置失败：%w", err)
	}
	return nil
}

// Editable 报告设置页是否可以修改数据目录。
func (p Paths) Editable() bool {
	if p.DefaultRoot == "" || p.locationPath() == "" {
		return false
	}
	return p.Mode == "" || p.Mode == ModeDefault || p.Mode == ModeCustom
}

// ChangeDisabledReason 返回设置页应显示的中文原因。
func (p Paths) ChangeDisabledReason() string {
	switch p.Mode {
	case ModeEnvironment:
		return "当前数据目录由环境变量 VRAY_DATA 固定，无法在应用内修改"
	case ModeDevelopment:
		return "开发运行模式固定使用工作目录下的 data，无法在应用内修改"
	case ModePortable:
		return "便携模式固定使用程序旁的 data，无法在应用内修改"
	case ModeFallback:
		return "用户数据目录不可用，当前已回退到程序旁目录，无法在应用内修改"
	}
	if !p.Editable() {
		return "当前构建没有可写的用户数据目录，无法修改"
	}
	return ""
}

// locationPath 返回稳定定位文件路径。
func (p Paths) locationPath() string {
	if p.LocationFile != "" {
		return p.LocationFile
	}
	if p.DefaultRoot != "" {
		return filepath.Join(p.DefaultRoot, LocationName)
	}
	return ""
}

// normalizeRoot 展开相对路径并返回绝对、干净的路径。
func normalizeRoot(value, base string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("目录不能为空")
	}
	if !filepath.IsAbs(value) {
		if base == "" {
			var err error
			base, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		value = filepath.Join(base, value)
	}
	value, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(value), nil
}

// canonicalPath 尽量解析符号链接，并保留尚不存在的尾部路径。
func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	path = filepath.Clean(path)

	existing := path
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(existing); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return path
		}
		tail = append(tail, filepath.Base(existing))
		existing = parent
	}
}

// samePath 比较两个已经展开的目录，Windows 下忽略大小写。
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	a, b = comparablePath(canonicalPath(a)), comparablePath(canonicalPath(b))
	return a == b
}

// pathWithin 报告 path 是否等于 root 或位于 root 之内。
func pathWithin(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	path = comparablePath(canonicalPath(path))
	root = comparablePath(canonicalPath(root))
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." ||
		(rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func comparablePath(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func joinWarning(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + "；" + next
}

// migratePending 把用户选择的旧数据目录复制到新目录。
//
// 已有文件不覆盖，因此迁移中断后可以安全重试；定位文件本身不进入
// 数据目录。旧目录始终保留，用户确认迁移完成后可以自行清理。
//
// 迁回默认目录时，默认目录中通常还留着第一次迁出前的旧数据库。此时
// 先把旧内容移到同级备份目录，再复制当前目录，避免启动后继续读到旧状态。
func migratePending(source, target, defaultRoot string) (bool, string, error) {
	source = canonicalPath(source)
	target = canonicalPath(target)
	defaultRoot = canonicalPath(defaultRoot)
	if samePath(source, target) {
		return false, "", errors.New("路径：迁移来源与目标目录相同")
	}
	if pathWithin(target, source) || pathWithin(source, target) {
		return false, "", errors.New("路径：迁移来源与目标目录不能互相包含")
	}
	// 回到默认目录是允许的；其他目标不能与默认目录互相包含。
	if !samePath(target, defaultRoot) &&
		(pathWithin(target, defaultRoot) || pathWithin(defaultRoot, target)) {
		return false, "", errors.New("路径：目标数据目录不能与默认数据目录互相包含")
	}

	info, err := os.Stat(source)
	if errors.Is(err, os.ErrNotExist) {
		// 源目录在复制完成后被清理或所在磁盘暂时离线。只要目标已有
		// 数据库，就把这次迁移视为已完成，避免应用永久无法启动。
		if pathExists(filepath.Join(target, DBFile)) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("路径：原数据目录不存在 %s", source)
	}
	if err != nil {
		return false, "", fmt.Errorf("路径：读取原数据目录 %s 失败：%w", source, err)
	}
	if !info.IsDir() {
		return false, "", fmt.Errorf("路径：原数据位置不是目录 %s", source)
	}
	if err := ensureWritable(target); err != nil {
		return false, "", fmt.Errorf("路径：目标数据目录不可写 %s：%w", target, err)
	}

	var backup string
	if samePath(target, defaultRoot) {
		backup, err = archiveDefaultData(target)
		if err != nil {
			return false, "", fmt.Errorf("路径：备份默认数据目录失败：%w", err)
		}
	}
	copied, err := copyMissingTreeExcept(target, os.DirFS(source), skipAppFile)
	if err != nil {
		return false, backup, fmt.Errorf("路径：迁移数据目录失败：%w", err)
	}
	return copied, backup, nil
}

// archiveDefaultData 把默认目录中的旧内容移到同级备份目录。
//
// location.json 必须留在原位，供本次启动继续完成迁移；临时写入探针也
// 不进入备份。备份目录与应用数据目录分离，不会在后续启动中被当成数据。
func archiveDefaultData(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	backup, err := newBackupDir(root)
	if err != nil {
		return "", err
	}
	moved := make([]string, 0, len(entries))
	for _, entry := range entries {
		if skipAppFile(entry.Name()) {
			continue
		}
		from := filepath.Join(root, entry.Name())
		to := filepath.Join(backup, entry.Name())
		if err := os.Rename(from, to); err != nil {
			rollbackErr := rollbackMoves(root, backup, moved)
			_ = os.Remove(backup)
			if rollbackErr != nil {
				return "", fmt.Errorf("移动 %s 失败：%w；回滚失败：%v", from, err, rollbackErr)
			}
			return "", err
		}
		moved = append(moved, entry.Name())
	}
	if len(moved) == 0 {
		_ = os.Remove(backup)
		return "", nil
	}
	return backup, nil
}

// newBackupDir 在默认目录同级创建唯一备份目录，避免多个备份互相覆盖。
func newBackupDir(root string) (string, error) {
	parent := filepath.Dir(root)
	base := filepath.Base(root)
	stamp := time.Now().Format("20060102-150405")
	for i := 0; ; i++ {
		suffix := ""
		if i > 0 {
			suffix = fmt.Sprintf("-%d", i)
		}
		dir := filepath.Join(parent, fmt.Sprintf("%s-backup-%s%s", base, stamp, suffix))
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
}

// rollbackMoves 在备份中途失败时，把已经移动的条目放回默认目录。
func rollbackMoves(root, backup string, moved []string) error {
	var firstErr error
	for i := len(moved) - 1; i >= 0; i-- {
		name := moved[i]
		if err := os.Rename(filepath.Join(backup, name), filepath.Join(root, name)); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// migrateLegacy 在目标目录尚无数据库时，把旧 data 目录中的内容合并过去。
//
// 只在旧目录包含数据库、凭据、效果日志或 geo 数据时执行，避免搬来
// 空目录。已有同名文件保持不动，因此中断后的下一次启动可以继续补齐。
// 复制失败不阻止启动：新目录仍可用，用户也可以手工迁移。
func migrateLegacy(legacy, root string) bool {
	if samePath(legacy, root) || fileExists(filepath.Join(root, DBFile)) {
		return false
	}
	if !hasLegacyData(legacy) {
		return false
	}
	copied, err := copyMissingTreeExcept(root, os.DirFS(legacy), skipAppFile)
	if err != nil || !copied {
		return false
	}
	return true
}

// skipAppFile 排除定位文件和写入探针，避免内部状态进入数据目录。
func skipAppFile(name string) bool {
	base := filepath.Base(name)
	return name == LocationName ||
		strings.HasPrefix(base, ".location-") ||
		strings.HasPrefix(base, ".write-probe-")
}

// hasLegacyData 判断旧目录是否包含值得迁移的已知数据文件。
func hasLegacyData(legacy string) bool {
	for _, name := range []string{DBFile, SecretsFile, EffectsFile, GeoIPFile, GeoSiteFile} {
		if fileExists(filepath.Join(legacy, name)) {
			return true
		}
	}
	return false
}

// copyMissingTreeExcept 复制 src 中尚不存在的普通文件，并跳过已有目标文件。
//
// 使用 fs.WalkDir 而不是拼接系统路径，既拒绝越界名称，也避免跟随符号链接。
func copyMissingTreeExcept(dst string, src fs.FS, skip func(string) bool) (bool, error) {
	copied := false
	err := fs.WalkDir(src, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." || (skip != nil && skip(name)) {
			return nil
		}

		target := filepath.Join(dst, filepath.FromSlash(name))
		if entry.IsDir() {
			if info, err := os.Lstat(target); err == nil {
				if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
					return fmt.Errorf("目标路径不是普通目录：%s", target)
				}
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() || pathExists(target) {
			return nil
		}
		if err := copyFile(target, src, name); err != nil {
			return err
		}
		copied = true
		return nil
	})
	return copied, err
}

// copyFile 写入单个文件，失败时不留下半截目标文件。
func copyFile(target string, src fs.FS, name string) error {
	source, err := src.Open(name)
	if err != nil {
		return err
	}
	defer source.Close()

	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	destination, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(target)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	return nil
}

// DB 返回存储文件路径。
func (p Paths) DB() string { return filepath.Join(p.Root, DBFile) }

// Secrets 返回机密密封文件路径。
func (p Paths) Secrets() string { return filepath.Join(p.Root, SecretsFile) }

// Effects 返回崩溃恢复用的副作用日志路径。
func (p Paths) Effects() string { return filepath.Join(p.Root, EffectsFile) }

// Logs 返回日志目录。
func (p Paths) Logs() string { return filepath.Join(p.Root, LogsDir) }

// GeoIP 返回 Xray 查找 geoip.dat 的位置。
func (p Paths) GeoIP() string { return filepath.Join(p.Root, GeoIPFile) }

// GeoSite 返回 Xray 查找 geosite.dat 的位置。
func (p Paths) GeoSite() string { return filepath.Join(p.Root, GeoSiteFile) }

// HasGeoData 报告两个 geo 数据文件是否都存在。
func (p Paths) HasGeoData() bool {
	return fileExists(p.GeoIP()) && fileExists(p.GeoSite())
}

// Prepare 创建 vRay 需要写入的目录。
func (p Paths) Prepare() error {
	for _, dir := range []string{p.Root, p.Logs()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("路径：创建目录 %s 失败：%w", dir, err)
		}
	}
	return nil
}

// fileExists 报告 path 是否为已存在的普通文件。
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// pathExists 报告任意类型的路径是否存在。
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
