// Package app 持有 vRay 的状态以及视图调用的操作。
//
// UI 通过 Snapshot（返回深拷贝）读取 State，并调用从不阻塞的 Service 方法。
// 所有接触内核或磁盘的工作都在独立 goroutine 中运行；见 docs/02 §5.9
// 与 docs/03 §4 的并发规则。
package app

import (
	"encoding/json"
	"sync"
	"time"

	xcore "github.com/sbgayhub/vray/internal/core"
	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/store"
)

// Phase 是 UI 展示的连接状态。
type Phase string

// 连接阶段。
const (
	PhaseIdle          Phase = "idle"
	PhaseConnecting    Phase = "connecting"
	PhaseConnected     Phase = "connected"
	PhaseDisconnecting Phase = "disconnecting"
	PhaseFailed        Phase = "failed"
)

// Traffic 保存 UI 绘制的速率与累计流量。
type Traffic struct {
	UpBps     int64 `json:"upBps"`
	DownBps   int64 `json:"downBps"`
	TotalUp   int64 `json:"totalUp"`
	TotalDown int64 `json:"totalDown"`
}

// Problem 是 UI 可展示的失败，使用稳定代码索引。
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// ProfileView 是列表中一个节点的展示形态。
type ProfileView struct {
	revision   time.Time
	IP         string `json:"ip,omitempty"`
	SourceID   string `json:"sourceId,omitempty"`
	SourceName string `json:"sourceName"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	Favorite   bool   `json:"favorite"`
}

// SourceView 是列表中一个订阅的展示形态。
type SourceView struct {
	Host            string    `json:"host"`
	Upload          int64     `json:"upload"`
	Download        int64     `json:"download"`
	Total           int64     `json:"total"`
	ExpireAt        time.Time `json:"expireAt"`
	LastFetchedAt   time.Time `json:"lastFetchedAt"`
	IntervalMinutes int       `json:"intervalMinutes"`
	Updating        bool      `json:"updating"`
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Enabled         bool      `json:"enabled"`
	NodeCount       int       `json:"nodeCount"`
	LastError       string    `json:"lastError,omitempty"`
}

// RuleView 是规则页中一条路由规则的展示形态。
type RuleView struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Enabled  bool     `json:"enabled"`
	Domains  []string `json:"domains,omitempty"`
	IPs      []string `json:"ips,omitempty"`
	Ports    string   `json:"ports,omitempty"`
	Process  []string `json:"process,omitempty"`
	Networks []string `json:"networks,omitempty"`
	Outbound string   `json:"outbound"`
	// Problem 是这条规则的首个问题，没有问题时为空。
	Problem string `json:"problem,omitempty"`
}

// Snapshot 是一帧内视图可以读取的全部状态。
//
// 它是值类型：切片都是副本，视图可以跨帧持有而不会与写入方竞争。
type Snapshot struct {
	Groups       []store.PolicyGroup       `json:"groups,omitempty"`
	GroupBusy    bool                      `json:"groupBusy"`
	GroupResult  string                    `json:"groupResult,omitempty"`
	Dashboard    Dashboard                 `json:"dashboard"`
	History      []TrafficPoint            `json:"history,omitempty"`
	Usage        map[string]store.Transfer `json:"usage,omitempty"`
	NodeDetails  map[string]IPDetails      `json:"nodeDetails,omitempty"`
	NodeSpeeds   map[string]DownloadTest   `json:"nodeSpeeds,omitempty"`
	SourceResult string                    `json:"sourceResult,omitempty"`
	SourceDraft  *SourceEdit               `json:"-"`
	Version      uint64                    `json:"version"`

	Phase   Phase     `json:"phase"`
	Mode    string    `json:"mode"`
	Active  string    `json:"active"` // 节点 ID
	Since   time.Time `json:"since"`
	Traffic Traffic   `json:"traffic"`

	Profiles         []ProfileView `json:"profiles"`
	Sources          []SourceView  `json:"sources"`
	Rules            []RuleView    `json:"rules"`
	Settings         SettingsView  `json:"settings"`
	Update           UpdateView    `json:"update"`
	ConsentRequired  bool          `json:"consentRequired"`
	SettingsSaving   bool          `json:"settingsSaving"`
	RoutingSaving    bool          `json:"routingSaving"`
	SettingsError    string        `json:"settingsError,omitempty"`
	DiagnosticSaving bool          `json:"diagnosticSaving"`
	DiagnosticResult string        `json:"diagnosticResult,omitempty"`
	Checking         bool          `json:"checking"`
	Checks           []Check       `json:"checks,omitempty"`

	// DataRoot 与 DataRootDefault 供设置页展示当前和平台默认位置；
	// 其余字段描述目录变更与重启迁移的状态。
	DataRoot         string `json:"dataRoot"`
	DataRootDefault  string `json:"dataRootDefault,omitempty"`
	DataRootEditable bool   `json:"dataRootEditable"`
	DataRootNotice   string `json:"dataRootNotice,omitempty"`
	DataRootSaving   bool   `json:"dataRootSaving"`
	DataRootPending  bool   `json:"dataRootPending"`
	DataRootError    string `json:"dataRootError,omitempty"`
	GeoReady         bool   `json:"geoReady"`
	// GeoState 对应 geo.Status.State：idle、downloading、ready 或 failed。
	GeoState string `json:"geoState"`
	// GeoDone 与 GeoTotal 是正在下载文件的字节进度。
	GeoDone  int64 `json:"geoDone"`
	GeoTotal int64 `json:"geoTotal"`
	// GeoError 用用户可以据以行动的文字解释下载失败。
	GeoError string `json:"geoError,omitempty"`
	CoreVer  string `json:"coreVersion"`

	// ImportAdded 与 ImportProblem 是最近一次导入的结果；订阅页据此
	// 告诉用户发生了什么。
	ImportAdded   int    `json:"importAdded"`
	ImportBusy    bool   `json:"importBusy"`
	ImportProblem string `json:"importProblem,omitempty"`

	// Recovered 是上次启动因上一次未正常退出而撤销的系统变更数量。
	// 非零意味着“应当告知用户”。
	Recovered int `json:"recovered,omitempty"`

	Problem *Problem `json:"problem,omitempty"`
}

// State 是所有视图共享、由互斥锁保护的真实状态。
type State struct {
	mu      sync.RWMutex
	version uint64
	snap    Snapshot

	onChange func() // 由外壳适配器在主线程调用
}

// NewState 返回带给定初始值的状态。
func NewState(initial Snapshot) *State {
	return &State{snap: initial}
}

// SetOnChange 注册请求 UI 重绘一帧的回调。
func (s *State) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// notifier 返回重绘回调，供不经 Update 也需要请求重绘的协作者使用
// （延迟探测器在快照之外发布自己的结果）。
func (s *State) notifier() func() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.onChange
}

// Snapshot 返回当前状态的深拷贝。
func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := s.snap
	out.Dashboard.PolicyExits.Nodes = append([]xcore.PolicyExitNode(nil), s.snap.Dashboard.PolicyExits.Nodes...)
	out.Groups = append([]store.PolicyGroup(nil), s.snap.Groups...)
	for i := range out.Groups {
		out.Groups[i].Members = append([]string(nil), out.Groups[i].Members...)
	}
	if s.snap.SourceDraft != nil {
		draft := *s.snap.SourceDraft
		out.SourceDraft = &draft
	}
	out.History = append([]TrafficPoint(nil), s.snap.History...)
	out.Usage = make(map[string]store.Transfer, len(s.snap.Usage))
	for id, usage := range s.snap.Usage {
		out.Usage[id] = usage
	}
	out.NodeDetails = make(map[string]IPDetails, len(s.snap.NodeDetails))
	for id, info := range s.snap.NodeDetails {
		out.NodeDetails[id] = info
	}
	out.NodeSpeeds = make(map[string]DownloadTest, len(s.snap.NodeSpeeds))
	for id, speed := range s.snap.NodeSpeeds {
		out.NodeSpeeds[id] = speed
	}
	out.Profiles = append([]ProfileView(nil), s.snap.Profiles...)
	out.Sources = append([]SourceView(nil), s.snap.Sources...)
	out.Checks = append([]Check(nil), s.snap.Checks...)
	out.Rules = append([]RuleView(nil), s.snap.Rules...)
	for i := range out.Rules {
		out.Rules[i] = cloneRule(out.Rules[i])
	}
	if s.snap.Problem != nil {
		p := *s.snap.Problem
		out.Problem = &p
	}
	return out
}

// Version 返回当前版本号，用于低成本变更检测。
func (s *State) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Update 在写锁内修改状态、递增版本并请求重绘；变更回调在锁外执行。
func (s *State) Update(fn func(*Snapshot)) {
	s.mu.Lock()
	fn(&s.snap)
	s.version++
	s.snap.Version = s.version
	notify := s.onChange
	s.mu.Unlock()

	if notify != nil {
		notify()
	}
}

// SetPhase 是 Update 的常见用法。
func (s *State) SetPhase(p Phase, problem *Problem) {
	s.Update(func(sn *Snapshot) {
		if sn.Phase != p {
			sn.Since = time.Now()
		}
		sn.Phase = p
		sn.Problem = problem
	})
}

// SetTraffic 更新时间戳之外的流量计数。
func (s *State) SetTraffic(t Traffic) {
	s.Update(func(sn *Snapshot) { sn.Traffic = t })
}

// SetProfiles 替换节点列表。
func (s *State) SetProfiles(list []ProfileView) {
	s.Update(func(sn *Snapshot) {
		sn.Profiles = append([]ProfileView(nil), list...)
	})
}

// SetSources 替换订阅列表。
func (s *State) SetSources(list []SourceView) {
	s.Update(func(sn *Snapshot) {
		sn.Sources = append([]SourceView(nil), list...)
	})
}

// SetRules 替换规则列表。
func (s *State) SetRules(list []RuleView) {
	s.Update(func(sn *Snapshot) {
		sn.Rules = append([]RuleView(nil), list...)
		for i := range sn.Rules {
			sn.Rules[i] = cloneRule(sn.Rules[i])
		}
	})
}

func cloneRule(r RuleView) RuleView {
	r.Domains = append([]string(nil), r.Domains...)
	r.IPs = append([]string(nil), r.IPs...)
	r.Process = append([]string(nil), r.Process...)
	r.Networks = append([]string(nil), r.Networks...)
	return r
}

// viewOf 把存储的节点转换为列表展示形态。
func viewOf(p store.Profile) ProfileView {
	return ProfileView{
		revision: p.UpdatedAt,
		SourceID: p.SourceID,
		ID:       p.ID,
		Name:     p.Name,
		Protocol: string(p.Protocol),
		Address:  p.Address,
		Port:     p.Port,
		Favorite: p.Favorite,
	}
}

// outboundOf 把存储的节点转换为 configgen 需要的出站配置。凭据单独传入，
// 因为它保存在凭据保管器中而不是普通存储里。
//
// 先合并 RawOutbound，再覆盖身份字段，这样在 UI 中编辑过的节点（新名称、
// 地址或端口）仍保留分享链接带来的传输与安全配置。只根据持久化列构建
// 出站会把 REALITY/WS 节点悄悄降级成普通 TCP；那正是导入修复要避免的
// 故障模式，在这里重做等于晚一层把修复又撤销。
func outboundOf(p store.Profile, secret string) configgen.Outbound {
	o := configgen.Outbound{}
	if len(p.RawOutbound) > 0 {
		// 畸形负载不能阻止连接，因此吞掉错误，下面的列仍然描述一个可用节点。
		_ = json.Unmarshal(p.RawOutbound, &o)
	}
	o.Name = p.Name
	o.Protocol = string(p.Protocol)
	o.Address = p.Address
	o.Port = p.Port
	o.Secret = secret
	// hysteria2 节点没有自己的网络层：其传输就是 hysteria，生成器据此
	// 启用 QUIC/TLS。留空会让本已正确导入的节点报“不支持的协议”。
	if o.Protocol == "hysteria2" || o.Protocol == "hysteria" {
		o.Network = "hysteria"
	}
	return o
}
