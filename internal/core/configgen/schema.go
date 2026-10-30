package configgen

import "encoding/json"

// 下面的 JSON 结构对应 Xray 自身的配置加载器（infra/conf）。字段名与嵌套
// 关系是与 LoadConfig 的契约；黄金测试会验证每种可生成的配置都能真正加载
//（docs/02 §6.2）。

type config struct {
	Policies    *PolicyMetadata    `json:"vrayPolicies,omitempty"`
	Log         logConfig          `json:"log"`
	API         apiConfig          `json:"api"`
	Stats       struct{}           `json:"stats"`
	Policy      policyConfig       `json:"policy"`
	Inbounds    []inbound          `json:"inbounds"`
	Outbounds   []outbound         `json:"outbounds"`
	Routing     routingConfig      `json:"routing"`
	DNS         *dnsConfig         `json:"dns,omitempty"`
	Observatory *observatoryConfig `json:"observatory,omitempty"`
}

type logConfig struct {
	LogLevel string `json:"loglevel"`
}

type apiConfig struct {
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
}

type policyConfig struct {
	Levels map[string]levelPolicy `json:"levels"`
	System systemPolicy           `json:"system"`
}

type levelPolicy struct {
	StatsUserUplink   bool `json:"statsUserUplink"`
	StatsUserDownlink bool `json:"statsUserDownlink"`
}

type systemPolicy struct {
	StatsInboundUplink    bool `json:"statsInboundUplink"`
	StatsInboundDownlink  bool `json:"statsInboundDownlink"`
	StatsOutboundUplink   bool `json:"statsOutboundUplink"`
	StatsOutboundDownlink bool `json:"statsOutboundDownlink"`
}

// defaultPolicy 开启流量图读取的计数器。
func defaultPolicy() policyConfig {
	return policyConfig{
		Levels: map[string]levelPolicy{"0": {StatsUserUplink: true, StatsUserDownlink: true}},
		System: systemPolicy{
			StatsInboundUplink:    true,
			StatsInboundDownlink:  true,
			StatsOutboundUplink:   true,
			StatsOutboundDownlink: true,
		},
	}
}

// inbound 是 mixed、tun 或 dokodemo-door 之一。
type inbound struct {
	Tag            string          `json:"tag"`
	Port           int             `json:"port,omitempty"`
	Listen         string          `json:"listen,omitempty"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings"`
	Sniffing       *sniffing       `json:"sniffing,omitempty"`
	StreamSettings any             `json:"streamSettings,omitempty"`
}

type sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride,omitempty"`
	RouteOnly    bool     `json:"routeOnly,omitempty"`
}

// outbound 涵盖代理协议以及 freedom、blackhole 和 dns。
type outbound struct {
	Tag            string          `json:"tag"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings"`
	StreamSettings *streamSettings `json:"streamSettings,omitempty"`
	Mux            *muxConfig      `json:"mux,omitempty"`
}

type muxConfig struct {
	Enabled         bool   `json:"enabled"`
	Concurrency     int    `json:"concurrency,omitempty"`
	XUDPConcurrency int    `json:"xudpConcurrency,omitempty"`
	XUDPProxyUDP443 string `json:"xudpProxyUDP443,omitempty"`
}

// streamSettings 是出站的传输与安全层。
type streamSettings struct {
	Network          string            `json:"network,omitempty"`
	Security         string            `json:"security,omitempty"`
	TLSSettings      *tlsSettings      `json:"tlsSettings,omitempty"`
	RealitySettings  *realitySettings  `json:"realitySettings,omitempty"`
	WSSettings       *wsSettings       `json:"wsSettings,omitempty"`
	GRPCSettings     *grpcSettings     `json:"grpcSettings,omitempty"`
	HTTPSettings     *httpSettings     `json:"httpSettings,omitempty"`
	XHTTPSettings    *xhttpSettings    `json:"xhttpSettings,omitempty"`
	KCPSettings      *kcpSettings      `json:"kcpSettings,omitempty"`
	HysteriaSettings *hysteriaSettings `json:"hysteriaSettings,omitempty"`
}

// hysteriaSettings 携带 hysteria2 握手参数。
//
// 传输层从这里读取参数，而不是从出站的 settings 块读取：密码放错位置的
// 配置能解析、能启动，但之后每个连接都会以认证错误失败。
type hysteriaSettings struct {
	Version int32  `json:"version"`
	Auth    string `json:"auth,omitempty"`
	UDPIdle int64  `json:"udpIdleTimeout,omitempty"`
}

type tlsSettings struct {
	ServerName           string   `json:"serverName,omitempty"`
	Fingerprint          string   `json:"fingerprint,omitempty"`
	ALPN                 []string `json:"alpn,omitempty"`
	AllowInsecure        bool     `json:"allowInsecure,omitempty"`
	PinnedPeerCertSHA256 string   `json:"pinnedPeerCertSha256,omitempty"`
}

type realitySettings struct {
	ServerName  string `json:"serverName,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
}

type wsSettings struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

type grpcSettings struct {
	ServiceName string `json:"serviceName,omitempty"`
}

type httpSettings struct {
	Host []string `json:"host,omitempty"`
	Path string   `json:"path,omitempty"`
}

type xhttpSettings struct {
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
	Mode string `json:"mode,omitempty"`
}

type kcpSettings struct {
	MTU  *int   `json:"mtu,omitempty"`
	Seed string `json:"seed,omitempty"`
}

type routingConfig struct {
	Balancers      []policyBalancer `json:"balancers,omitempty"`
	DomainStrategy string           `json:"domainStrategy"`
	Rules          []routingRule    `json:"rules"`
}

type routingRule struct {
	BalancerTag string   `json:"balancerTag,omitempty"`
	Type        string   `json:"type"`
	RuleTag     string   `json:"ruleTag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Port        string   `json:"port,omitempty"`
	Process     []string `json:"process,omitempty"`
	Network     string   `json:"network,omitempty"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	OutboundTag string   `json:"outboundTag,omitempty"`
}

type dnsConfig struct {
	Servers []dnsServer `json:"servers"`
}

type dnsServer struct {
	Address string   `json:"address"`
	Domains []string `json:"domains,omitempty"`
}

type observatoryConfig struct {
	SubjectSelector   []string `json:"subjectSelector"`
	ProbeURL          string   `json:"probeUrl"`
	ProbeInterval     string   `json:"probeInterval"`
	EnableConcurrency bool     `json:"enableConcurrency"`
}
