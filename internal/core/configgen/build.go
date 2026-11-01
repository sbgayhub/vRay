package configgen

import (
	"encoding/json"
	"fmt"
	"strings"
)

// buildOutbounds 依次输出当前节点（作为代理）、其余节点，以及固定的
// direct、block 与 dns 出站。
func buildOutbounds(in Input) []outbound {
	outs := []outbound{buildProxy(TagProxy, in.Active)}
	for i, o := range in.Others {
		outs = append(outs, buildProxy(fmt.Sprintf("node:%d", i), o))
	}
	outs = append(outs,
		outbound{
			Tag:      TagDirect,
			Protocol: "freedom",
			Settings: mustJSON(map[string]any{"domainStrategy": "UseIPv4v6"}),
		},
		outbound{
			Tag:      TagBlock,
			Protocol: "blackhole",
			Settings: mustJSON(map[string]any{}),
		},
	)
	if len(in.Settings.DNSServers) > 0 {
		outs = append(outs, outbound{
			Tag:      TagDNS,
			Protocol: "dns",
			Settings: mustJSON(map[string]any{}),
		})
	}
	return outs
}

// xrayProtocol 把 vRay 的协议名映射为 Xray 注册的名称。
//
// hysteria2 上两者不同：分享链接的协议与通用名称是 "hysteria2"，
// 而 Xray 的出站注册名是 "hysteria" 且版本为 2。输出 "hysteria2" 会让
// 内核收到未知协议，并以 "failed to load outbound detour config for
// protocol hysteria2" 失败——看起来像 vRay 的缺陷，而它确实是。
func xrayProtocol(p string) string {
	switch p {
	case "hysteria2", "hysteria":
		return "hysteria"
	default:
		return p
	}
}

// buildProxy 把一个节点转换为出站。
func buildProxy(tag string, o Outbound) outbound {
	out := outbound{Tag: tag, Protocol: xrayProtocol(o.Protocol), Settings: proxySettings(o)}

	if ss := streamSettingsFor(o); ss != nil {
		out.StreamSettings = ss
	}
	if o.Mux != nil && o.Mux.Enabled {
		out.Mux = &muxConfig{
			Enabled:         true,
			Concurrency:     o.Mux.Concurrency,
			XUDPConcurrency: o.Mux.XUDP,
			XUDPProxyUDP443: o.Mux.UDP443,
		}
	}
	return out
}

// proxySettings 构建协议专属的 `settings` 对象。
func proxySettings(o Outbound) json.RawMessage {
	switch o.Protocol {
	case "vless":
		user := map[string]any{"id": o.Secret, "encryption": "none"}
		if o.Flow != "" {
			user["flow"] = o.Flow
		}
		return mustJSON(map[string]any{
			"vnext": []any{map[string]any{
				"address": o.Address,
				"port":    o.Port,
				"users":   []any{user},
			}},
		})
	case "vmess":
		return mustJSON(map[string]any{
			"vnext": []any{map[string]any{
				"address": o.Address,
				"port":    o.Port,
				"users": []any{map[string]any{
					"id":       o.Secret,
					"security": "auto",
				}},
			}},
		})
	case "trojan":
		return mustJSON(map[string]any{
			"servers": []any{map[string]any{
				"address":  o.Address,
				"port":     o.Port,
				"password": o.Secret,
			}},
		})
	case "shadowsocks":
		return mustJSON(map[string]any{
			"servers": []any{map[string]any{
				"address":  o.Address,
				"port":     o.Port,
				"method":   orDefault(o.Method, "aes-256-gcm"),
				"password": o.Secret,
			}},
		})
	case "hysteria2", "hysteria":
		// Xray 把 hysteria2 命名为 "hysteria" 并要求版本 2。密码不在这里：
		// 它位于 streamSettings.hysteriaSettings，传输层从那里读取。
		return mustJSON(map[string]any{
			"version": 2,
			"address": o.Address,
			"port":    o.Port,
		})
	default:
		return mustJSON(map[string]any{})
	}
}

// streamSettingsFor 构建传输与安全层；普通无 TLS 的 TCP 返回 nil。
func streamSettingsFor(o Outbound) *streamSettings {
	network := o.Network
	if o.Protocol == "hysteria2" || o.Protocol == "hysteria" {
		network = "hysteria"
	}
	if network == "" {
		network = "tcp"
	}
	security := o.Security
	if security == "" && (o.Protocol == "trojan" || network == "hysteria") {
		security = "tls"
	}
	if security == "" {
		security = "none"
	}
	if network == "tcp" && security == "none" {
		return nil
	}

	s := &streamSettings{Network: network, Security: security}

	switch security {
	case "tls":
		s.TLSSettings = &tlsSettings{
			ServerName:           firstNonEmpty(o.SNI, o.Host, o.Address),
			Fingerprint:          o.Fingerprint,
			ALPN:                 splitALPN(o.ALPN),
			PinnedPeerCertSHA256: o.PinnedPeerCertSHA256,
		}
	case "reality":
		s.RealitySettings = &realitySettings{
			ServerName:  firstNonEmpty(o.SNI, o.Host),
			Fingerprint: orDefault(o.Fingerprint, "chrome"),
			PublicKey:   o.PublicKey,
			ShortID:     o.ShortID,
			SpiderX:     o.SpiderX,
		}
	}

	switch network {
	case "ws":
		s.WSSettings = &wsSettings{Path: orDefault(o.Path, "/"), Host: o.Host}
	case "grpc":
		s.GRPCSettings = &grpcSettings{ServiceName: o.Service}
	case "httpupgrade":
		s.HTTPSettings = &httpSettings{Path: orDefault(o.Path, "/")}
		if o.Host != "" {
			s.HTTPSettings.Host = []string{o.Host}
		}
	case "xhttp", "splithttp":
		s.XHTTPSettings = &xhttpSettings{Host: o.Host, Path: orDefault(o.Path, "/"), Mode: o.XHTTPMode}
	case "kcp":
		s.KCPSettings = &kcpSettings{}
	case "hysteria":
		// hysteria2 总是运行在带 TLS 的 QUIC 上，因此安全层是隐含的而非
		// 可选：安全层为 "none" 的 hysteria 节点仍必须获得 TLS 设置，
		// 否则传输永远无法启动。
		s.HysteriaSettings = &hysteriaSettings{Version: 2, Auth: o.Secret}
		if s.Security == "none" {
			s.Security = "tls"
		}
		if s.TLSSettings == nil && s.RealitySettings == nil {
			s.TLSSettings = &tlsSettings{
				ServerName:           firstNonEmpty(o.SNI, o.Host, o.Address),
				Fingerprint:          o.Fingerprint,
				ALPN:                 splitALPN(o.ALPN),
				PinnedPeerCertSHA256: o.PinnedPeerCertSHA256,
			}
		}
	}
	return s
}

// buildInbounds 输出所选模式的监听器以及 API 入站。
func buildInbounds(in Input) []inbound {
	var ins []inbound

	switch in.Mode {
	case ModeTun:
		tunSettings := map[string]any{
			"name": in.Settings.TunName,
			"mtu":  orDefaultInt(in.Settings.TunMTU, 1500),
		}
		if len(in.Settings.TunGateway) > 0 {
			tunSettings["gateway"] = in.Settings.TunGateway
		}
		// 让 Xray 自身的上行流量绕开隧道；否则经 tun 的默认路由会形成
		// 回环。
		tunSettings["autoSystemRoutingTable"] = []string{"0.0.0.0/0", "::/0"}
		if in.Settings.TunOutboundInterface != "" {
			tunSettings["autoOutboundsInterface"] = in.Settings.TunOutboundInterface
		}
		ins = append(ins, inbound{
			Tag:      TagInTun,
			Port:     0,
			Protocol: "tun",
			Settings: mustJSON(tunSettings),
			Sniffing: &sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}},
		})
	default:
		ins = append(ins, inbound{
			Tag:      TagInSocks,
			Listen:   "127.0.0.1",
			Port:     in.Settings.MixedPort,
			Protocol: "mixed",
			Settings: mustJSON(map[string]any{
				"auth": "noauth",
				"udp":  true,
				"ip":   "127.0.0.1",
			}),
			Sniffing: &sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}},
		})
	}

	// 只有选定端口后才存在 API 入站；Xray 会拒绝没有端口的 dokodemo 入站。
	if in.Settings.APIPort > 0 {
		ins = append(ins, inbound{
			Tag:      TagInAPI,
			Listen:   "127.0.0.1",
			Port:     in.Settings.APIPort,
			Protocol: "dokodemo-door",
			Settings: mustJSON(map[string]any{"address": "127.0.0.1"}),
		})
	}

	// DNS 入站同时需要上游与端口；没有端口 Xray 会拒绝该入站，
	// 因此调用方必须提供。
	if len(in.Settings.DNSServers) > 0 && in.Settings.DNSPort > 0 {
		ins = append(ins, inbound{
			Tag:      TagInDNS,
			Listen:   "127.0.0.1",
			Port:     in.Settings.DNSPort,
			Protocol: "dokodemo-door",
			Settings: mustJSON(map[string]any{"address": dnsHost(in.Settings.DNSServers[0]), "port": 53, "network": "tcp,udp"}),
		})
	}
	return ins
}

// buildRouting 编译规则：始终先把 DNS 流量挡在代理之外，最后放兜底规则。
func buildRouting(in Input) routingConfig {
	var rules []routingRule

	// API 必须能在本地访问且绝不走代理。只有 API 入站存在时才有意义，
	// 而入站需要端口。
	if in.Settings.APIPort > 0 {
		rules = append(rules, routingRule{
			Type: "field", RuleTag: "api", InboundTag: []string{TagInAPI}, OutboundTag: TagAPI,
		})
	}
	if len(in.Settings.DNSServers) > 0 && in.Settings.DNSPort > 0 {
		rules = append(rules, routingRule{
			Type: "field", RuleTag: "dns-in", InboundTag: []string{TagInDNS}, OutboundTag: TagDirect,
		})
	}
	// geo 规则需要数据文件；引用的 geo 规则无数据可读时 Xray 会拒绝启动。
	// 私有地址始终绕过代理：把 192.168.0.0/16 送进隧道会破坏局域网访问。
	if in.Settings.GeoReady {
		rules = append(rules,
			routingRule{
				Type: "field", RuleTag: "geo-private",
				IP: []string{"geoip:private"}, OutboundTag: TagDirect,
			},
		)
	} else {
		// 没有数据文件时，至少让本地网络绕开隧道；字面范围完全不依赖 geo 数据。
		rules = append(rules, routingRule{
			Type: "field", RuleTag: "private",
			IP: []string{
				"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
				"127.0.0.0/8", "169.254.0.0/16", "::1/128", "fc00::/7",
			},
			OutboundTag: TagDirect,
		})
	}

	// 每种模式下都保留 API、DNS 与局域网路由。全局与直连模式用一条显式的
	// 应用流量兜底规则绕过用户/Geo 规则。
	if mode := in.Settings.RoutingMode; mode == "global" || mode == "direct" {
		target := TagProxy
		if mode == "direct" {
			target = TagDirect
		}
		rules = append(rules, routingRule{
			Type: "field", RuleTag: "mode-" + mode, Network: "tcp,udp", OutboundTag: target,
		})
		return routingConfig{DomainStrategy: "AsIs", Rules: rules}
	}

	for _, r := range in.Rules {
		// 跳过整条规则：只丢弃它的 Geo 条件，可能把 AND 匹配放宽成
		// 针对全部 TCP 或 443 端口的意外规则。
		if !in.Settings.GeoReady && RuleNeedsGeo(r) {
			continue
		}
		// 没有匹配器的规则会被 Xray 拒绝（"no effective fields"），因此跳过，
		// 而不是输出无法启动的配置。validate() 已把空规则转为诊断信息。
		// Process 也是匹配器：只配进程名的规则同样会在这里被丢掉。
		if len(r.Domains) == 0 && len(r.IPs) == 0 && r.Ports == "" && len(r.Networks) == 0 && len(r.Process) == 0 {
			continue
		}
		rule := routingRule{
			Type:        "field",
			RuleTag:     "rule-" + r.ID,
			Domain:      r.Domains,
			IP:          r.IPs,
			Port:        r.Ports,
			Process:     r.Process,
			Network:     strings.Join(r.Networks, ","),
			OutboundTag: r.Outbound,
		}
		rules = append(rules, rule)
	}

	// 用户规则可以覆盖国内默认分流（例如广告拦截）。上面的 API/DNS 与
	// 局域网安全规则保持更高优先级。
	if in.Settings.GeoReady {
		rules = append(rules, routingRule{
			Type: "field", RuleTag: "geo-cn",
			Domain: []string{"geosite:cn"}, OutboundTag: TagDirect,
		})
	}

	// 不加兜底规则：Xray 会把所有未匹配流量送往第一个出站，也就是当前节点。
	// 没有匹配器的规则会被拒绝。

	return routingConfig{DomainStrategy: "AsIs", Rules: rules}
}

// buildDNS 保留给定的上游地址；调用方必须传入 IP 形式端点，避免解析
// 经由隧道形成回环。
func buildDNS(s Settings) *dnsConfig {
	cfg := &dnsConfig{}
	if s.FakeDNS {
		cfg.Servers = append(cfg.Servers, dnsServer{Address: "fakedns"})
	}
	for _, addr := range s.DNSServers {
		cfg.Servers = append(cfg.Servers, dnsServer{Address: addr})
	}
	return cfg
}

// dnsHost 去掉 DNS 端点的协议与路径，让 dokodemo 可以拨号。
// IPv6 字面量保留方括号与冒号。
func dnsHost(endpoint string) string {
	s := endpoint
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if strings.HasPrefix(s, "[") {
		if end := strings.IndexByte(s, ']'); end >= 0 {
			return s[:end+1]
		}
		return s
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return s
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		// 这里的值全是字面量映射，因此不会失败；万一失败，空对象仍能让
		// 配置可加载，黄金测试会显示差异。
		return json.RawMessage("{}")
	}
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func splitALPN(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func orDefaultInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
