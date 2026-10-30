// suburl 包负责将分享链接和订阅转换为节点。
//
// 这里的每个函数都是纯函数：输入字符串，输出节点和诊断信息。坏行不会导致整个导入失败；
// 它会变成一条 Diagnostic，便于 UI 列出被跳过的内容（docs/02 §5.1）。
package suburl

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/sbgayhub/vray/internal/core/configgen"
	"github.com/sbgayhub/vray/internal/store"
)

// Diagnostic 说明某一行导入为何被跳过。
type Diagnostic struct {
	Line    int    `json:"line"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// Result 是一次导入的结果。
type Result struct {
	Groups      []store.PolicyGroup
	Profiles    []store.Profile
	Diagnostics []Diagnostic
}

// ParseText 导入分享链接、base64 订阅正文或混合的多行内容。
func ParseText(text string) Result {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "proxies:") || strings.Contains(trimmed, "\nproxies:") ||
		(strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, `"proxies"`)) {
		return parseClash(text)
	}
	var res Result
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		// 整个订阅正文采用 base64 编码，而分享链接从不使用该编码。
		if !strings.Contains(line, "://") {
			if decoded, ok := decodeBase64(line); ok {
				inner := ParseText(decoded)
				res.Profiles = append(res.Profiles, inner.Profiles...)
				res.Groups = append(res.Groups, inner.Groups...)
				for _, d := range inner.Diagnostics {
					d.Line += i // 保持行号指向原始载荷
					res.Diagnostics = append(res.Diagnostics, d)
				}
				continue
			}
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Line: i + 1, Message: "无法识别：既不是分享链接，也不是 base64 订阅内容",
			})
			continue
		}

		p, diag := parseLink(line)
		if diag != nil {
			diag.Line = i + 1
			res.Diagnostics = append(res.Diagnostics, *diag)
			continue
		}
		res.Profiles = append(res.Profiles, p)
	}
	return res
}

func parseLink(raw string) (store.Profile, *Diagnostic) {
	switch {
	case strings.HasPrefix(raw, "vless://"):
		return parseVLESS(raw)
	case strings.HasPrefix(raw, "vmess://"):
		return parseVMess(raw)
	case strings.HasPrefix(raw, "trojan://"):
		return parseTrojan(raw)
	case strings.HasPrefix(raw, "ss://"):
		return parseShadowsocks(raw)
	case strings.HasPrefix(raw, "hysteria2://"), strings.HasPrefix(raw, "hy2://"):
		return parseHysteria2(raw)
	default:
		scheme := raw
		if i := strings.Index(raw, "://"); i > 0 {
			scheme = raw[:i]
		}
		return store.Profile{}, &Diagnostic{
			Field:   "protocol",
			Message: fmt.Sprintf("不支持的协议 %q", scheme),
		}
	}
}

// fragmentName 从链接的 #fragment 读取显示名称。
func fragmentName(u *url.URL) string {
	if u.Fragment == "" {
		return ""
	}
	if n, err := url.QueryUnescape(u.Fragment); err == nil {
		return n
	}
	return u.Fragment
}

// hostPort 读取主机和端口，任一缺失时返回失败。
func hostPort(u *url.URL) (string, int, *Diagnostic) {
	host := u.Hostname()
	if host == "" {
		return "", 0, &Diagnostic{Field: "address", Message: "缺少服务器地址"}
	}
	portStr := u.Port()
	if portStr == "" {
		return "", 0, &Diagnostic{Field: "port", Message: "缺少端口"}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, &Diagnostic{Field: "port", Message: fmt.Sprintf("端口无效：%q", portStr)}
	}
	return host, port, nil
}

func parseVLESS(raw string) (store.Profile, *Diagnostic) {
	u, err := url.Parse(raw)
	if err != nil {
		return store.Profile{}, &Diagnostic{Field: "url", Message: err.Error()}
	}
	host, port, diag := hostPort(u)
	if diag != nil {
		return store.Profile{}, diag
	}
	id := u.User.Username()
	if id == "" {
		return store.Profile{}, &Diagnostic{Field: "uuid", Message: "缺少 UUID"}
	}

	q := u.Query()
	security := q.Get("security")
	if security == "" {
		security = "none"
	}
	if security == "reality" && q.Get("pbk") == "" {
		return store.Profile{}, &Diagnostic{
			Field: "pbk", Message: "REALITY 缺少公钥（pbk）",
		}
	}

	name := fragmentName(u)
	if name == "" {
		name = fmt.Sprintf("%s:%d", host, port)
	}
	p := store.Profile{
		Name:      name,
		Protocol:  store.ProtoVLESS,
		Address:   host,
		Port:      port,
		SecretRef: id, // 调用方会将其移入 Secrets
	}
	p.RawOutbound = rawOutbound(name, string(store.ProtoVLESS), host, port, id, q)
	return p, nil
}

func parseVMess(raw string) (store.Profile, *Diagnostic) {
	body := strings.TrimPrefix(raw, "vmess://")
	decoded, ok := decodeBase64(body)
	if !ok {
		return store.Profile{}, &Diagnostic{Field: "url", Message: "vmess 链接不是有效的 base64"}
	}

	var v struct {
		PS   string `json:"ps"`
		Add  string `json:"add"`
		Port any    `json:"port"`
		ID   string `json:"id"`

		// 传输和安全配置：大多数链接不包含它们，但面板生成的 vmess 链接通常会携带。
		Scy  string `json:"scy"`
		Net  string `json:"net"`
		Type string `json:"type"`
		Host string `json:"host"`
		Path string `json:"path"`
		TLS  string `json:"tls"`
		SNI  string `json:"sni"`
		ALPN any    `json:"alpn"`
		FP   string `json:"fp"`
	}
	if err := json.Unmarshal([]byte(decoded), &v); err != nil {
		return store.Profile{}, &Diagnostic{Field: "url", Message: "vmess 内容不是有效 JSON"}
	}
	if v.Add == "" {
		return store.Profile{}, &Diagnostic{Field: "address", Message: "缺少服务器地址"}
	}
	port, ok := anyToInt(v.Port)
	if !ok {
		return store.Profile{}, &Diagnostic{Field: "port", Message: "端口无效"}
	}
	if v.ID == "" {
		return store.Profile{}, &Diagnostic{Field: "uuid", Message: "缺少 UUID"}
	}

	name := v.PS
	if name == "" {
		name = fmt.Sprintf("%s:%d", v.Add, port)
	}
	p := store.Profile{
		Name:      name,
		Protocol:  store.ProtoVMess,
		Address:   v.Add,
		Port:      port,
		SecretRef: v.ID,
	}

	q := url.Values{}
	setIfNotEmpty := func(k, val string) {
		if val != "" {
			q.Set(k, val)
		}
	}
	setIfNotEmpty("type", v.Net) // vmess 将传输方式称为 "net"
	setIfNotEmpty("host", v.Host)
	setIfNotEmpty("path", v.Path)
	setIfNotEmpty("sni", v.SNI)
	setIfNotEmpty("fp", v.FP)
	setIfNotEmpty("security", v.TLS) // "tls" 或 "reality"；空字符串表示无
	switch a := v.ALPN.(type) {
	case string:
		setIfNotEmpty("alpn", a)
	case []any:
		parts := make([]string, 0, len(a))
		for _, one := range a {
			if s, ok := one.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		setIfNotEmpty("alpn", strings.Join(parts, ","))
	}
	if v.Scy != "" && v.Scy != "auto" {
		// 记录链接中的值，但它不属于出站配置结构；生成器会自行选择加密算法。
		setIfNotEmpty("scy", v.Scy)
	}
	p.RawOutbound = rawOutbound(name, string(store.ProtoVMess), v.Add, port, v.ID, q)
	return p, nil
}

func parseTrojan(raw string) (store.Profile, *Diagnostic) {
	u, err := url.Parse(raw)
	if err != nil {
		return store.Profile{}, &Diagnostic{Field: "url", Message: err.Error()}
	}
	host, port, diag := hostPort(u)
	if diag != nil {
		return store.Profile{}, diag
	}
	pass := u.User.Username()
	if pass == "" {
		return store.Profile{}, &Diagnostic{Field: "password", Message: "缺少密码"}
	}

	name := fragmentName(u)
	if name == "" {
		name = fmt.Sprintf("%s:%d", host, port)
	}
	p := store.Profile{
		Name:      name,
		Protocol:  store.ProtoTrojan,
		Address:   host,
		Port:      port,
		SecretRef: pass,
	}
	p.RawOutbound = rawOutbound(name, string(store.ProtoTrojan), host, port, pass, u.Query())
	return p, nil
}

// parseShadowsocks 同时处理旧版 base64 userinfo 和 SIP002 形式。
func parseShadowsocks(raw string) (store.Profile, *Diagnostic) {
	body := strings.TrimPrefix(raw, "ss://")
	if i := strings.IndexByte(body, '#'); i >= 0 {
		body = body[:i]
	}
	if i := strings.IndexByte(body, '?'); i >= 0 {
		body = body[:i]
	}

	var methodPass, hostport string
	if i := strings.LastIndexByte(body, '@'); i >= 0 {
		// SIP002 形式：base64(method:pass)@host:port
		userinfo, rest := body[:i], body[i+1:]
		if dec, ok := decodeBase64(userinfo); ok {
			methodPass = dec
		} else {
			methodPass = userinfo
		}
		hostport = rest
	} else {
		dec, ok := decodeBase64(body)
		if !ok {
			return store.Profile{}, &Diagnostic{Field: "url", Message: "ss 链接不是有效的 base64"}
		}
		i := strings.LastIndexByte(dec, '@')
		if i < 0 {
			return store.Profile{}, &Diagnostic{Field: "url", Message: "ss 链接缺少 @host:port"}
		}
		methodPass, hostport = dec[:i], dec[i+1:]
	}

	method, _, ok := strings.Cut(methodPass, ":")
	if !ok || method == "" {
		return store.Profile{}, &Diagnostic{Field: "method", Message: "缺少加密方式"}
	}
	host, portStr, ok := strings.Cut(hostport, ":")
	if !ok {
		return store.Profile{}, &Diagnostic{Field: "port", Message: "缺少端口"}
	}
	port, err := strconv.Atoi(strings.Trim(portStr, "/"))
	if err != nil || port <= 0 || port > 65535 {
		return store.Profile{}, &Diagnostic{Field: "port", Message: fmt.Sprintf("端口无效：%q", portStr)}
	}

	u, _ := url.Parse(raw)
	name := ""
	if u != nil {
		name = fragmentName(u)
	}
	if name == "" {
		name = fmt.Sprintf("%s:%d", host, port)
	}
	p := store.Profile{
		Name:      name,
		Protocol:  store.ProtoShadowsocks,
		Address:   host,
		Port:      port,
		SecretRef: methodPass,
	}
	q := url.Values{}
	if u != nil {
		q = u.Query()
	}
	p.RawOutbound = rawOutbound(name, string(store.ProtoShadowsocks), host, port, methodPass, q)
	return p, nil
}

func parseHysteria2(raw string) (store.Profile, *Diagnostic) {
	u, err := url.Parse(raw)
	if err != nil {
		return store.Profile{}, &Diagnostic{Field: "url", Message: err.Error()}
	}
	host, port, diag := hostPort(u)
	if diag != nil {
		return store.Profile{}, diag
	}
	auth := u.User.Username()
	if auth == "" {
		auth = u.Query().Get("auth")
	}

	name := fragmentName(u)
	if name == "" {
		name = fmt.Sprintf("%s:%d", host, port)
	}
	q := u.Query()
	p := store.Profile{
		Name:      name,
		Protocol:  store.ProtoHysteria2,
		Address:   host,
		Port:      port,
		SecretRef: auth,
	}

	p.RawOutbound = rawOutbound(name, string(store.ProtoHysteria2), host, port, auth, q)
	return p, nil
}

// decodeBase64 接受标准字母表和 URL 安全字母表，带填充或不带填充均可。
func decodeBase64(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), true
		}
	}
	return "", false
}

// anyToInt 读取 JSON 可能解析为数字或字符串的端口。
func anyToInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), n > 0 && n <= 65535
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil && i > 0 && i <= 65535
	default:
		return 0, false
	}
}

// SubscriptionInfo 是订阅返回的流量头部。
type SubscriptionInfo struct {
	Upload   int64
	Download int64
	Total    int64
	Expire   int64 // Unix 秒；缺失时为 0
}

// ParseSubscriptionUserInfo 读取 `subscription-userinfo` 头部的值。
func ParseSubscriptionUserInfo(header string) (SubscriptionInfo, bool) {
	if strings.TrimSpace(header) == "" {
		return SubscriptionInfo{}, false
	}
	var info SubscriptionInfo
	seen := false
	for _, part := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "upload":
			info.Upload, seen = n, true
		case "download":
			info.Download, seen = n, true
		case "total":
			info.Total, seen = n, true
		case "expire":
			info.Expire, seen = n, true
		}
	}
	return info, seen
}

// rawOutbound 记录链接中关于如何连接节点的全部信息。
//
// 否则导入只会保留 name/address/port/secret，REALITY 或 WebSocket 节点会静默变成普通节点：
// 配置生成器没有构建传输层所需的信息，连接失败时看起来会像服务器问题。分享链接是这些细节的唯一来源，
// 因此将其存储为生成器已经使用的 Outbound JSON。
func rawOutbound(name, protocol, host string, port int, secret string, q url.Values) json.RawMessage {
	o := configgen.Outbound{
		Name:     name,
		Protocol: protocol,
		Address:  host,
		Port:     port,
		Secret:   secret,
	}

	// 传输层：普通 tcp 无需字段；其他传输方式还需要 path 或服务名。
	switch network := q.Get("type"); network {
	case "", "tcp":
	case "ws", "httpupgrade", "xhttp", "kcp", "grpc":
		o.Network = network
	}
	o.Path = q.Get("path")
	o.Host = q.Get("host")
	o.Service = q.Get("serviceName")
	o.XHTTPMode = q.Get("mode")
	if o.Service == "" && o.Network == "grpc" {
		// 有些链接会将 gRPC 服务名放在 "path" 中。
		o.Service = o.Path
		o.Path = ""
	}

	switch security := q.Get("security"); security {
	case "", "none":
	case "tls", "reality":
		o.Security = security
	}
	o.SNI = q.Get("sni")
	o.Fingerprint = q.Get("fp")
	o.ALPN = q.Get("alpn")
	for _, key := range []string{"allowInsecure", "insecure", "allow-insecure"} {
		value := strings.ToLower(q.Get(key))
		o.AllowInsecure = o.AllowInsecure || value == "1" || value == "true"
	}

	// REALITY 额外参数。
	o.PublicKey = q.Get("pbk")
	o.ShortID = q.Get("sid")
	o.SpiderX = q.Get("spx")

	// VLESS flow，例如 xtls-rprx-vision。
	o.Flow = q.Get("flow")

	raw, err := json.Marshal(o)
	if err != nil {
		return nil
	}
	return raw
}
