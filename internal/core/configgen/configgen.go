// Package configgen 把 vRay 的设置与节点转换为 Xray 的 JSON 配置。
//
// 它是 Input 的纯函数：不读时钟、不碰文件、不访问网络。因此黄金测试
// 才有意义，UI 也能在不启动任何东西的情况下预览配置（docs/02 §6.2）。
package configgen

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Mode 是 vRay 把流量交给应用的方式。
type Mode string

// 客户端提供的两种模式。
const (
	// ModeSystemProxy 监听本地混合端口；应用指向它。
	ModeSystemProxy Mode = "system-proxy"
	// ModeTun 通过虚拟网卡接管全部流量。
	ModeTun Mode = "tun"
)

// Outbound 是用户可选择的一个代理节点，由节点信息描述。
type Outbound struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"` // 协议：vless | vmess | trojan | shadowsocks
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Secret   string `json:"secret"` // 凭据：UUID 或密码
	Flow     string `json:"flow,omitempty"`

	// 传输：tcp | ws | grpc | httpupgrade | xhttp | kcp
	Network   string `json:"network,omitempty"`
	Path      string `json:"path,omitempty"`
	Host      string `json:"host,omitempty"`
	Service   string `json:"serviceName,omitempty"` // gRPC 服务名
	XHTTPMode string `json:"xhttpMode,omitempty"`

	// 安全层：none | tls | reality
	Security             string `json:"security,omitempty"`
	SNI                  string `json:"sni,omitempty"`
	Fingerprint          string `json:"fingerprint,omitempty"`
	ALPN                 string `json:"alpn,omitempty"`
	AllowInsecure        bool   `json:"allowInsecure,omitempty"`
	PinnedPeerCertSHA256 string `json:"pinnedPeerCertSha256,omitempty"`

	// REALITY 参数
	PublicKey string `json:"publicKey,omitempty"`
	ShortID   string `json:"shortId,omitempty"`
	SpiderX   string `json:"spiderX,omitempty"`

	// Shadowsocks 加密方式，例如 aes-256-gcm
	Method string `json:"method,omitempty"`

	Mux *Mux `json:"mux,omitempty"`
}

// Mux 是 Xray 的多路复用设置。
type Mux struct {
	Enabled     bool   `json:"enabled"`
	Concurrency int    `json:"concurrency,omitempty"`
	XUDP        int    `json:"xudpConcurrency,omitempty"`
	UDP443      string `json:"xudpProxyUDP443,omitempty"`
}

// Settings 是影响生成配置的选项。
type Settings struct {
	Mode        Mode
	RoutingMode string // 空值或 rule | global | direct
	MixedPort   int
	LogLevel    string

	// Tun 字段，在 Mode 为 ModeTun 时使用。
	TunName              string
	TunMTU               int
	TunGateway           []string
	TunOutboundInterface string

	// DNS 设置
	DNSServers []string
	DNSPort    int
	FakeDNS    bool

	// 进程内 gRPC 客户端使用的 API 端口；0 表示启动时选择。
	APIPort int

	ProbeURL      string
	ProbeInterval string

	// GeoReady 表示 geoip.dat 与 geosite.dat 已在磁盘上。文件缺失时 Xray
	// 会拒绝引用 geo 规则的配置，因此只有下载完成后才输出 geo 规则
	//（docs/02 §9.1）。
	GeoReady bool
}

// Rule 是一条路由规则。
type Rule struct {
	ID       string
	Name     string
	Domains  []string
	IPs      []string
	Ports    string
	Process  []string
	Networks []string
	Outbound string // 出站：direct | proxy | block
}

// Input 是 Build 需要的全部输入。
type Input struct {
	PolicyGroups []PolicyGroup
	PolicyNodes  []PolicyNode
	ActivePolicy string
	Active       Outbound
	Others       []Outbound // 额外节点，切换时无需重启
	Mode         Mode
	Settings     Settings
	Rules        []Rule
}

// Diagnostic 指向一个无法使用的字段。
//
// 严重诊断会中止构建；警告描述被丢弃或降级、但仍能生成可用配置的情况。
type Diagnostic struct {
	Field    string `json:"field"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // 严重程度："error" | "warning"
}

// Diagnostic 可携带的严重程度。
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// blocks 报告该诊断是否阻止构建配置。
func (d Diagnostic) blocks() bool { return d.Severity != SeverityWarning }

// 配置中使用的标签名；见 docs/02 §6.1。
const (
	TagProxy   = "proxy"
	TagDirect  = "direct"
	TagBlock   = "block"
	TagDNS     = "dns"
	TagAPI     = "api"
	TagInSocks = "in-socks"
	TagInTun   = "in-tun"
	TagInAPI   = "in-api"
	TagInDNS   = "in-dns"
)

// 统计计数器的作用域与方向，用于构造 Xray 经 StatsService 暴露的名称。
// 放在这里意味着配置生成器与流量采样器不会各自漂移。
const (
	ScopeOutbound = "outbound"
	ScopeInbound  = "inbound"
	DirUplink     = "uplink"
	DirDownlink   = "downlink"
)

// Name 按 Xray 的拼接方式构造流量计数器名：四部分以 ">>>" 分隔。
func Name(scope, tag, direction string) string {
	return scope + ">>>" + tag + ">>>traffic>>>" + direction
}

// Build 返回 JSON 配置，或阻止构建的诊断信息。
func Build(in Input) ([]byte, []Diagnostic, error) {
	diags := validate(in)
	for _, d := range diags {
		if d.blocks() {
			return nil, diags, ErrInvalid
		}
	}

	services := []string{"HandlerService", "LoggerService", "StatsService", "RoutingService"}
	if in.Settings.RoutingMode != "direct" && in.Settings.ProbeURL != "" {
		services = append(services, "ObservatoryService")
	}
	cfg := config{
		Log: logConfig{LogLevel: orDefault(in.Settings.LogLevel, "warning")},
		API: apiConfig{
			Tag:      TagAPI,
			Services: services,
		},
		Stats:     struct{}{},
		Policy:    defaultPolicy(),
		Outbounds: buildOutbounds(in),
		Routing:   buildRouting(in),
	}
	cfg.Inbounds = buildInbounds(in)
	if err := applyPolicies(&cfg, in); err != nil {
		return nil, diags, err
	}
	if len(in.Settings.DNSServers) > 0 {
		cfg.DNS = buildDNS(in.Settings)
	}
	if in.Settings.ProbeURL != "" && in.Settings.RoutingMode != "direct" {
		cfg.Observatory = &observatoryConfig{
			SubjectSelector:   []string{TagProxy},
			ProbeURL:          in.Settings.ProbeURL,
			ProbeInterval:     orDefault(in.Settings.ProbeInterval, "30s"),
			EnableConcurrency: true,
		}
		if cfg.Policies != nil && len(cfg.Policies.Nodes) > 0 {
			cfg.Observatory.SubjectSelector = []string{PolicyNodePrefix}
		}
	}

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("配置生成：编码失败：%w", err)
	}
	return raw, diags, nil
}

// ErrInvalid 表示 Build 拒绝了输入；诊断信息说明原因。
var ErrInvalid = fmt.Errorf("配置生成：输入无效")

// validate 收集所有问题，UI 因此能一次全部展示。
func validate(in Input) []Diagnostic {
	var d []Diagnostic
	switch in.Settings.RoutingMode {
	case "", "rule", "global", "direct":
	default:
		d = append(d, Diagnostic{Severity: SeverityError, Field: "routingMode", Message: "不支持的出站方式"})
	}

	if in.Active.Address == "" {
		d = append(d, Diagnostic{Severity: SeverityError, Field: "address", Message: "缺少服务器地址"})
	}
	if in.Active.Port <= 0 || in.Active.Port > 65535 {
		d = append(d, Diagnostic{Severity: SeverityError, Field: "port", Message: "端口必须在 1-65535 之间"})
	}
	if in.Active.Secret == "" {
		d = append(d, Diagnostic{Severity: SeverityError, Field: "secret", Message: "缺少 UUID 或密码"})
	}

	switch in.Active.Protocol {
	case "vless", "vmess", "trojan", "shadowsocks", "hysteria2", "hysteria":
	case "":
		d = append(d, Diagnostic{Severity: SeverityError, Field: "protocol", Message: "缺少协议"})
	default:
		d = append(d, Diagnostic{Severity: SeverityError, Field: "protocol", Message: fmt.Sprintf("不支持的协议 %q", in.Active.Protocol)})
	}

	if in.Active.Security == "reality" && in.Active.PublicKey == "" {
		d = append(d, Diagnostic{Severity: SeverityError, Field: "publicKey", Message: "REALITY 缺少公钥"})
	}
	if in.Active.AllowInsecure && in.Active.Security != "reality" && in.Active.PinnedPeerCertSHA256 == "" {
		d = append(d, Diagnostic{Severity: SeverityError, Field: "certificate", Message: "该节点需要先获取服务器证书指纹"})
	}

	if in.Mode != ModeSystemProxy && in.Mode != ModeTun {
		d = append(d, Diagnostic{Severity: SeverityError, Field: "mode", Message: fmt.Sprintf("不支持的模式 %q", in.Mode)})
	}
	if in.Mode == ModeTun {
		if !isTunName(in.Settings.TunName) {
			d = append(d, Diagnostic{Severity: SeverityError, Field: "tunName", Message: "TUN 名称必须形如 utunN 或 tunN"})
		}
	}
	if in.Mode == ModeSystemProxy {
		if in.Settings.MixedPort <= 0 || in.Settings.MixedPort > 65535 {
			d = append(d, Diagnostic{Severity: SeverityError, Field: "mixedPort", Message: "本地端口必须在 1-65535 之间"})
		}
	}

	if in.Settings.RoutingMode == "global" || in.Settings.RoutingMode == "direct" {
		return d
	}
	for _, r := range in.Rules {
		if !in.Settings.GeoReady && RuleNeedsGeo(r) {
			d = append(d, Diagnostic{Severity: SeverityWarning, Field: "rule/" + r.ID, Message: "规则库未就绪，暂不启用此 Geo 规则"})
		}
		switch r.Outbound {
		case TagDirect, TagProxy, TagBlock:
		default:
			d = append(d, Diagnostic{
				Severity: SeverityError,
				Field:    "rule/" + r.ID,
				Message:  fmt.Sprintf("规则的出口 %q 必须是 direct、proxy 或 block", r.Outbound),
			})
		}
		// 空规则会被 Xray 拒绝，因此丢弃；但要明确告知，
		// 而不是让用户疑惑它为何毫无作用。
		if len(r.Domains) == 0 && len(r.IPs) == 0 && r.Ports == "" && len(r.Networks) == 0 && len(r.Process) == 0 {
			d = append(d, Diagnostic{
				Severity: SeverityWarning,
				Field:    "rule/" + r.ID,
				Message:  "规则没有任何匹配条件，已跳过",
			})
		}
		// 畸形值会中止构建：Xray 会在解析时拒绝配置，而“配置无效”
		// 远不如指出错误的 CIDR 有用。
		for _, p := range ValidateRule(r) {
			d = append(d, Diagnostic{
				Severity: SeverityError,
				Field:    "rule/" + r.ID + "/" + p.Field,
				Message:  fmt.Sprintf("%s：%s", p.Value, p.Message),
			})
		}
	}

	return d
}

// isTunName 接受 Xray 自身可接受的网卡名称。
func isTunName(name string) bool {
	if name == "" {
		return false
	}
	rest, ok := strings.CutPrefix(name, "utun")
	if !ok {
		rest, ok = strings.CutPrefix(name, "tun")
	}
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
