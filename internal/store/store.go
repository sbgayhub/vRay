// Package store 保存 vRay 的数据：节点、订阅、规则与设置。
//
// 数据保存在一个 JSON 文件中，保存时原子重写。曾计划使用 SQLite，但每个
// 方案都需要 cgo、随包 C 库或 4 MB 二进制，而用户的节点列表很小；
// Store 是接口，后端可以更换而不影响调用方。
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SchemaVersion 是磁盘文件的当前布局版本。
const SchemaVersion = 2

// LegalVersion 是当前免责声明与条款版本。提升它会要求每位用户重新阅读
// 并接受一次新文本。
const LegalVersion = 1

// ErrNotFound 表示记录不在存储中。
var ErrNotFound = errors.New("存储：未找到")

// Protocol 命名节点类型，与 Xray 出站协议一致。
type Protocol string

// vRay 可从分享链接导入的协议。
const (
	ProtoVLESS       Protocol = "vless"
	ProtoVMess       Protocol = "vmess"
	ProtoTrojan      Protocol = "trojan"
	ProtoShadowsocks Protocol = "shadowsocks"
	ProtoHysteria2   Protocol = "hysteria2"
	ProtoSOCKS       Protocol = "socks"
	ProtoHTTP        Protocol = "http"
)

// Profile 是一个节点。Secret 保存密码或 UUID；设置 SecretsRef 时它不会
// 写入文件。
type Profile struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Protocol    Protocol        `json:"protocol"`
	Address     string          `json:"address"`
	Port        int             `json:"port"`
	SourceID    string          `json:"sourceId,omitempty"`
	GroupID     string          `json:"groupId,omitempty"`
	Favorite    bool            `json:"favorite,omitempty"`
	SecretRef   string          `json:"secretRef,omitempty"`
	RawOutbound json.RawMessage `json:"rawOutbound,omitempty"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

// Transfer 是单个节点累计测得的流量。
type Transfer struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}

// NodeIPInfo 是节点最近一次成功的出口观测。请求状态、错误和凭据不会进入
// 这份持久记录。它永不过期。
type NodeIPInfo struct {
	IP        string    `json:"ip"`
	Country   string    `json:"country,omitempty"`
	Region    string    `json:"region,omitempty"`
	City      string    `json:"city,omitempty"`
	ASN       string    `json:"asn,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Type      string    `json:"type,omitempty"`
	Score     string    `json:"score,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

// DownloadSpeed 是最近一次成功保存的单连接测速结果。
type DownloadSpeed struct {
	BytesPerSecond int64         `json:"bytesPerSecond"`
	Bytes          int64         `json:"bytes"`
	Duration       time.Duration `json:"durationNanos"`
	CheckedAt      time.Time     `json:"checkedAt"`
	Endpoint       string        `json:"endpoint"`
}

// Source 是 vRay 从中获取节点的订阅。
type Source struct {
	Revision        uint64    `json:"revision,omitempty"`
	Host            string    `json:"host,omitempty"`
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	URLRef          string    `json:"urlRef,omitempty"` // Secrets 中的键
	UserAgent       string    `json:"userAgent,omitempty"`
	IntervalMinutes int       `json:"intervalMinutes,omitempty"`
	Upload          int64     `json:"upload,omitempty"`
	Download        int64     `json:"download,omitempty"`
	Total           int64     `json:"total,omitempty"`
	ExpireAt        time.Time `json:"expireAt,omitempty"`
	LastFetchedAt   time.Time `json:"lastFetchedAt,omitempty"`
	LastError       string    `json:"lastError,omitempty"`
	Enabled         bool      `json:"enabled"`
}

// Settings 保存非机密的用户偏好。
type Settings struct {
	ActiveGroup          string    `json:"activeGroup,omitempty"`
	Language             string    `json:"language,omitempty"`
	LookupKeyConfigured  bool      `json:"ipLookupConfigured,omitempty"`
	Accent               string    `json:"accent,omitempty"`      // system 或自定义 #rrggbb
	Theme                string    `json:"theme,omitempty"`       // 主题：system | light | dark
	Material             string    `json:"material,omitempty"`    // 材质：none | mica | micaAlt | acrylic | liquidGlass
	Mode                 string    `json:"mode"`                  // 模式：system-proxy | tun | direct
	RoutingMode          string    `json:"routingMode,omitempty"` // 路由：rule | global | direct
	MixedPort            int       `json:"mixedPort"`
	ActiveProfile        string    `json:"activeProfile,omitempty"`
	CloseToTray          bool      `json:"closeToTray"`
	OpenAtLogin          bool      `json:"openAtLogin"`
	AutoReconnect        bool      `json:"autoReconnect"`
	AutoUpdateChecks     bool      `json:"autoUpdateChecks"`
	TermsAcceptedVersion int       `json:"termsAcceptedVersion,omitempty"`
	TermsAcceptedAt      time.Time `json:"termsAcceptedAt,omitempty"`
	LogLevel             string    `json:"logLevel"`
	ProbeURL             string    `json:"probeUrl"`
	LatencyConcurrency   int       `json:"latencyConcurrency,omitempty"`
	LatencyTimeoutMS     int       `json:"latencyTimeoutMs,omitempty"`
}

// DefaultSettings 是文件新建时使用的默认值。
func DefaultSettings() Settings {
	return Settings{
		Language:           "zh-Hans",
		Accent:             "system",
		Theme:              "system",
		Material:           "none",
		Mode:               "system-proxy",
		RoutingMode:        "rule",
		MixedPort:          2080,
		CloseToTray:        true,
		AutoReconnect:      true,
		AutoUpdateChecks:   true,
		LogLevel:           "warning",
		ProbeURL:           "https://www.gstatic.com/generate_204",
		LatencyConcurrency: 8,
		LatencyTimeoutMS:   5000,
	}
}

// Data 是文件保存的全部内容。
type Data struct {
	Groups        []PolicyGroup            `json:"groups,omitempty"`
	NodeSpeeds    map[string]DownloadSpeed `json:"nodeSpeeds,omitempty"`
	NodeDetails   map[string]NodeIPInfo    `json:"nodeDetails,omitempty"`
	Usage         map[string]Transfer      `json:"usage,omitempty"`
	SchemaVersion int                      `json:"schemaVersion"`
	Profiles      []Profile                `json:"profiles"`
	Sources       []Source                 `json:"sources"`
	Rules         []Rule                   `json:"rules,omitempty"`
	Settings      Settings                 `json:"settings"`
}

// PolicyGroup 保存限定在单个订阅内的稳定节点/分组引用。
type PolicyGroup struct {
	ID       string   `json:"id"`
	SourceID string   `json:"sourceId"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Members  []string `json:"members"`
	Selected string   `json:"selected,omitempty"`
	URL      string   `json:"url,omitempty"`
	Interval int      `json:"interval,omitempty"`
}

// Rule 是规则页编辑的一条用户路由规则。
//
// 匹配值保留用户输入的字符串：Xray 解析的是路由器语法（domain:、full:、
// geoip:、CIDR、端口范围），在这里解析只会丢失本代码不认识的形态。
type Rule struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Enabled  bool     `json:"enabled"`
	Domains  []string `json:"domains,omitempty"`
	IPs      []string `json:"ips,omitempty"`
	Ports    string   `json:"ports,omitempty"`
	Process  []string `json:"process,omitempty"`
	Networks []string `json:"networks,omitempty"`
	// Outbound 是 direct、proxy 或 block。
	Outbound string `json:"outbound"`
	// Order 是规则位置，越小越先执行。路由器是首匹配语义，
	// 因此顺序是规则含义的一部分，而不是展示细节。
	Order int `json:"order"`
}

// Store 是持久化契约；当前由 JSON 文件实现。
type Store interface {
	// Load 读取全部内容，文件不存在时创建默认文件。
	Load() (Data, error)
	// Save 原子地替换全部内容。
	Save(Data) error
	// PutProfile 插入或更新一个节点。
	PutProfile(Profile) error
	// DeleteProfiles 按 ID 删除节点。
	DeleteProfiles(ids []string) error
	// GetProfile 返回一个节点。
	GetProfile(id string) (Profile, error)
	// PutSource 插入或更新一个订阅。
	PutSource(Source) error
	// DeleteSource 删除一个订阅。
	DeleteSource(id string) error
	// SetSettings 替换设置。
	SetSettings(Settings) error
	// SetRules 替换整个规则列表并重新编号。
	SetRules([]Rule) error
}

// File 是由单个 JSON 文件支撑的 Store。
type File struct {
	path string
	mu   sync.Mutex
	// 原始字节缓存：Load 发生在几乎每个业务操作里，而文件只在写路径
	// 变化。以 mtime+size 校验命中，省去重复读盘；解析仍每次进行，
	// 调用方拿到的 Data 依旧各自独立，不存在共享可变状态。
	cacheMtime time.Time
	cacheSize  int64
	cacheRaw   []byte
}

// NewFile 返回 path 处的存储；文件不必已经存在。
func NewFile(path string) *File { return &File{path: path} }

// Path 返回存储写入位置。
func (f *File) Path() string { return f.path }

// Load 读取文件，缺失时用默认值创建。
func (f *File) Load() (Data, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.load()
}

func (f *File) load() (Data, error) {
	info, err := os.Stat(f.path)
	if errors.Is(err, os.ErrNotExist) {
		f.cacheRaw, f.cacheMtime, f.cacheSize = nil, time.Time{}, 0
		d := Data{SchemaVersion: SchemaVersion, Settings: DefaultSettings()}
		if err := f.write(d); err != nil {
			return Data{}, err
		}
		return d, nil
	}
	if err != nil {
		return Data{}, fmt.Errorf("存储：读取 %s 失败：%w", f.path, err)
	}

	var raw []byte
	if f.cacheRaw != nil && info.ModTime().Equal(f.cacheMtime) && info.Size() == f.cacheSize {
		raw = f.cacheRaw
	} else {
		raw, err = os.ReadFile(f.path)
		if err != nil {
			return Data{}, fmt.Errorf("存储：读取 %s 失败：%w", f.path, err)
		}
		f.cacheRaw, f.cacheMtime, f.cacheSize = raw, info.ModTime(), info.Size()
	}

	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		return Data{}, fmt.Errorf("存储：解析 %s 失败：%w", f.path, err)
	}
	if err := migrate(&d); err != nil {
		return Data{}, err
	}
	if d.Settings.MixedPort == 0 {
		d.Settings = DefaultSettings()
	}
	if d.Settings.Theme == "" {
		d.Settings.Theme = "system"
	}
	if d.Settings.Language == "" {
		d.Settings.Language = "zh-Hans"
	}
	if d.Settings.Accent == "" {
		d.Settings.Accent = "system"
	}
	if d.Settings.Material == "" {
		d.Settings.Material = "none"
	}
	if d.Settings.RoutingMode == "" {
		d.Settings.RoutingMode = "rule"
	}
	if d.Settings.LatencyConcurrency <= 0 {
		d.Settings.LatencyConcurrency = DefaultSettings().LatencyConcurrency
	}
	if d.Settings.LatencyTimeoutMS <= 0 {
		d.Settings.LatencyTimeoutMS = DefaultSettings().LatencyTimeoutMS
	}
	return d, nil
}

// migrate 把旧文件升级到当前版本，对当前版本文件调用也安全。
func migrate(d *Data) error {
	if d.SchemaVersion > SchemaVersion {
		return fmt.Errorf("存储：文件架构版本为 %d，当前程序仅支持 %d，请升级 vRay", d.SchemaVersion, SchemaVersion)
	}
	// 版本 0 是“没有 schemaVersion 的早期文件”。版本 2 增加了自动更新检查
	// 与带版本的法律条款接受标记。
	if d.SchemaVersion < 2 && d.Settings.MixedPort != 0 {
		d.Settings.AutoUpdateChecks = true
	}
	d.SchemaVersion = SchemaVersion
	return nil
}

// Save 替换整个文件。
func (f *File) Save(d Data) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d.SchemaVersion = SchemaVersion
	return f.write(d)
}

// write 先序列化到临时文件再重命名，因此崩溃不会留下半截文件。
func (f *File) write(d Data) error {
	// 节点删除可能来自 DeleteProfiles，也可能来自订阅的整库 Save。
	// 让所有写入路径的清理行为保持一致。
	if len(d.NodeDetails) > 0 {
		kept := make(map[string]NodeIPInfo, len(d.NodeDetails))
		for _, profile := range d.Profiles {
			if info, exists := d.NodeDetails[profile.ID]; exists {
				kept[profile.ID] = info
			}
		}
		d.NodeDetails = kept
	}
	if len(d.NodeSpeeds) > 0 {
		kept := make(map[string]DownloadSpeed, len(d.NodeSpeeds))
		for _, profile := range d.Profiles {
			if speed, exists := d.NodeSpeeds[profile.ID]; exists {
				kept[profile.ID] = speed
			}
		}
		d.NodeSpeeds = kept
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return fmt.Errorf("存储：创建目录失败：%w", err)
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("存储：编码失败：%w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(f.path), ".db-*.json")
	if err != nil {
		return fmt.Errorf("存储：创建临时文件失败：%w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("存储：写入失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("存储：关闭失败：%w", err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		return fmt.Errorf("存储：重命名失败：%w", err)
	}
	// 写路径是文件内容变化的唯一来源，但 mtime 粒度不可靠，直接失效缓存，
	// 让下一次 Load 重新校验。
	f.cacheRaw, f.cacheMtime, f.cacheSize = nil, time.Time{}, 0
	return nil
}

// update 在单把锁下执行读-改-写。
func (f *File) update(fn func(*Data) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	d, err := f.load()
	if err != nil {
		return err
	}
	if err := fn(&d); err != nil {
		return err
	}
	d.SchemaVersion = SchemaVersion
	return f.write(d)
}

// PutProfile 插入或更新一个节点。
func (f *File) PutProfile(p Profile) error {
	return f.update(func(d *Data) error {
		p.UpdatedAt = time.Now().UTC()
		for i := range d.Profiles {
			if d.Profiles[i].ID == p.ID {
				d.Profiles[i] = p
				return nil
			}
		}
		d.Profiles = append(d.Profiles, p)
		return nil
	})
}

// DeleteProfiles 按 ID 删除节点；未知 ID 会被忽略。
func (f *File) DeleteProfiles(ids []string) error {
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	return f.update(func(d *Data) error {
		kept := d.Profiles[:0]
		for _, p := range d.Profiles {
			if !drop[p.ID] {
				kept = append(kept, p)
			}
		}
		d.Profiles = kept
		return nil
	})
}

// GetProfile 返回一个节点。
func (f *File) GetProfile(id string) (Profile, error) {
	d, err := f.Load()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range d.Profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, ErrNotFound
}

// PutSource 插入或更新一个订阅。
func (f *File) PutSource(s Source) error {
	return f.update(func(d *Data) error {
		for i := range d.Sources {
			if d.Sources[i].ID == s.ID {
				d.Sources[i] = s
				return nil
			}
		}
		d.Sources = append(d.Sources, s)
		return nil
	})
}

// DeleteSource 删除订阅，但保留其节点。
func (f *File) DeleteSource(id string) error {
	return f.update(func(d *Data) error {
		kept := d.Sources[:0]
		for _, s := range d.Sources {
			if s.ID != id {
				kept = append(kept, s)
			}
		}
		d.Sources = kept
		return nil
	})
}

// SetSettings 替换设置。
func (f *File) SetSettings(s Settings) error {
	return f.update(func(d *Data) error {
		d.Settings = s
		return nil
	})
}

// SetRules 替换规则列表，并按切片顺序重编 Order。
//
// Order 在这里赋值而不是信任调用方：路由器是首匹配语义，空缺或重复会
// 悄悄改变哪条规则胜出。
func (f *File) SetRules(rules []Rule) error {
	return f.update(func(d *Data) error {
		out := make([]Rule, len(rules))
		copy(out, rules)
		for i := range out {
			out[i].Order = i
		}
		d.Rules = out
		return nil
	})
}

// Rules 按顺序返回已存储规则。
func (f *File) Rules() ([]Rule, error) {
	d, err := f.Load()
	if err != nil {
		return nil, err
	}
	return append([]Rule(nil), d.Rules...), nil
}
