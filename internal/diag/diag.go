// Package diag 构建用户附加到缺陷报告的归档包。
//
// 难点不是 zip，而是决定什么可以放进去。携带 UUID、密码或订阅 URL 的
// 诊断包就是绕几步的凭据泄漏——这些 URL 是持有者令牌，粘贴到公开
// issue 就等于任何人都能用。因此脱敏不是收尾工作，而是设计本身：
//
//   - 机密从不进入诊断包，因为根本不会传入；
//   - 所有可能包含机密的内容在写入前按模式清洗：UUID、URL 中的密码、
//     订阅令牌、节点地址；
//   - 清洗逻辑通过“用已知机密构建诊断包再搜索它们”来测试。
package diag

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Report 是进入诊断包的内容。每个字段都是应用已知且维护者需要的；
// 这里没有任何内容来自凭据保管器。
type Report struct {
	Memory MemoryStats `json:"memory"`
	// Created 是诊断包的创建时间。
	Created time.Time `json:"created"`
	// App 是版本字符串。
	App string `json:"app"`
	// Platform 是 runtime.GOOS/GOARCH 加 Go 版本。
	Platform string `json:"platform"`
	// Phase、Mode 与 GeoState 是应用当时的状态。
	Phase    string `json:"phase"`
	Mode     string `json:"mode"`
	GeoState string `json:"geoState"`
	// Problem 是当前错误，调用方已脱敏或由这里脱敏。
	Problem *Problem `json:"problem,omitempty"`
	// Nodes 是已存储节点的摘要：协议计数，绝不包含 ID。
	Nodes []NodeSummary `json:"nodes"`
	// Sources 是订阅列表，URL 已替换为哈希。
	Sources []SourceSummary `json:"sources"`
	// Checks 是环境自检结果。
	Checks []Check `json:"checks"`
	// Logs 是收集器环形缓冲的末尾部分。
	Logs []LogLine `json:"logs"`
	// Files 列出数据目录布局：名称与大小，绝不包含内容。
	Files []FileInfo `json:"files"`
}

// Problem 是一个失败。
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// NodeSummary 是诊断包中单个节点的描述：足以看出配置问题，
// 但不足以用来连接。
type NodeSummary struct {
	Protocol string `json:"protocol"`
	// Address 是隐藏中间部分的地址，因此同一服务商的两个节点仍可区分，
	// 又不会暴露服务器名称。
	Address string `json:"address"`
	Port    int    `json:"port"`
	// Security 是 "none"、"tls" 或 "reality"；它解释了大多数连接失败，
	// 且不属于机密。
	Security string `json:"security"`
	// Source 表示节点来自链接还是订阅。
	Source string `json:"source"`
}

// SourceSummary 是隐去 URL 的单个订阅。
type SourceSummary struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// URL 是脱敏形式，绝不是真实地址。
	URL       string `json:"url"`
	LastErr   string `json:"lastError,omitempty"`
	NodeCount int    `json:"nodeCount"`
}

// Check 是一行环境检查。
type Check struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// LogLine 是一行已清洗凭据的日志。
type LogLine struct {
	Level string `json:"level"`
	Time  string `json:"time"`
	Text  string `json:"text"`
}

// FileInfo 描述数据目录中的一个文件。
type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Redaction 替换所有可能构成凭据的内容。
//
// 替换结果刻意保持可读——用 "‹uuid›" 而不是 "***"——让维护者能看出
// 那里曾有 UUID，却得不到任何关于它的信息。
const (
	RedactedUUID     = "‹uuid›"
	RedactedPassword = "‹password›"
	RedactedHost     = "‹host›"
	RedactedURL      = "‹subscription-url›"
	RedactedPath     = "‹path›"
)

// uuidRe 匹配标准的 8-4-4-4-12 UUID，也就是每个分享链接携带的 ID。
var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// subscriptionRe 匹配任意协议的 URL；在本应用中意味着分享链接
// （vless://…）、订阅（https://…）或 WebSocket 端点（ws://…）。
//
// 协议范围刻意放宽：只认识 http 的脱敏器会放过
// "trojan://password@host"，却看起来已经覆盖了 URL。
var subscriptionRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^\s"'<>]+`)

// base64TokenRe 匹配较长的 base64 串，订阅令牌或编码内容就是这种形态。
var base64TokenRe = regexp.MustCompile(`[A-Za-z0-9+/_-]{32,}={0,2}`)

// ipv4Re 匹配点分十进制 IPv4 地址；MaskAddress 对每个节点都要用它判定。
var ipv4Re = regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)

// Scrub 从任意文本中移除凭据。
//
// 它应用于所有离开应用的字符串，包括日志行和错误详情，因为失败消息
// 正是 URL 容易出现的地方："dial tcp: lookup vless://uuid@host" 是真实形态。
func Scrub(s string) string {
	if s == "" {
		return s
	}
	// 顺序很重要。先替换 UUID，URL 处理才能看出凭据所在：
	// "vless://‹uuid›@host" 会被读成带 UUID 的分享链接，而不是裸密码。
	s = uuidRe.ReplaceAllString(s, RedactedUUID)
	s = subscriptionRe.ReplaceAllStringFunc(s, redactURL)
	s = base64TokenRe.ReplaceAllString(s, RedactedURL)
	return s
}

// redactURL 保留 URL 的协议以及“曾有凭据”这一事实，丢弃主机与路径——
// 对分享链接而言主机就是服务器，对订阅 URL 而言路径就是令牌。
//
// "vless://‹uuid›@‹server›/‹path›" 说明出现了什么类型的内容，
// 却不暴露它指向哪里。
func redactURL(u string) string {
	scheme := ""
	rest := u
	if i := strings.Index(u, "://"); i > 0 {
		scheme = u[:i+3]
		rest = u[i+3:]
	}

	userinfo := ""
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		userinfo = rest[:at]
	}

	// 主机与路径都被丢弃。对分享链接而言主机是服务器；对订阅 URL 而言
	// 路径是令牌。两者都不是维护者需要的，却都是攻击者想要的。
	host := RedactedHost
	if userinfo != "" {
		return scheme + RedactUserinfo(userinfo) + "@" + host + "/" + RedactedPath
	}
	return scheme + host + "/" + RedactedPath
}

// RedactUserinfo 标注凭据的“类型”而不是其值。
//
// 分享链接中的裸 userinfo 是 UUID，带冒号的是 user:password；
// 两者无论如何都是机密，因此标记不试图区分。
func RedactUserinfo(info string) string {
	if strings.Contains(info, ":") {
		return RedactedPassword
	}
	return RedactedUUID
}

// lastLabel 返回 s 中最后一个点分隔部分。
func lastLabel(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// MaskAddress 隐藏节点地址的中间部分。
//
// 节点主机不是凭据，但属于隐私细节；显示 "jp1.‹host›.com"
// 足以区分两个节点。
func MaskAddress(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if ipv4Re.MatchString(host) {
		parts := strings.Split(host, ".")
		return parts[0] + ".x.x." + parts[3]
	}
	label, rest, ok := strings.Cut(host, ".")
	if !ok {
		return RedactedHost
	}
	return label + "." + RedactedHost + "." + lastLabel(rest)
}

// ScrubReport 对报告的每个文本字段应用 Scrub。
//
// 在一处实现意味着新增字段不会被遗忘：调用方填充结构体，这里统一遍历。
func ScrubReport(r *Report) {
	if r.Problem != nil {
		r.Problem.Message = Scrub(r.Problem.Message)
		r.Problem.Detail = Scrub(r.Problem.Detail)
	}
	// 每个节点的真实地址无论出现在哪里都会被替换：失败消息会点名无法
	// 访问的主机，日志又会再点一次。保留它们会抵消节点列表的脱敏。
	real := make([]string, len(r.Nodes))
	for i := range r.Nodes {
		real[i] = r.Nodes[i].Address
		if real[i] == "" {
			continue
		}
		masked := MaskAddress(real[i])
		if r.Problem != nil {
			r.Problem.Message = strings.ReplaceAll(r.Problem.Message, real[i], masked)
			r.Problem.Detail = strings.ReplaceAll(r.Problem.Detail, real[i], masked)
		}
		for c := range r.Checks {
			r.Checks[c].Detail = strings.ReplaceAll(r.Checks[c].Detail, real[i], masked)
		}
		for l := range r.Logs {
			r.Logs[l].Text = strings.ReplaceAll(r.Logs[l].Text, real[i], masked)
		}
	}
	for i := range r.Sources {
		r.Sources[i].URL = RedactedURL
		r.Sources[i].Name = Scrub(r.Sources[i].Name)
		r.Sources[i].LastErr = Scrub(r.Sources[i].LastErr)
	}
	for i := range r.Nodes {
		r.Nodes[i].Address = MaskAddress(real[i])
	}
	for i := range r.Checks {
		r.Checks[i].Detail = Scrub(r.Checks[i].Detail)
	}
	for i := range r.Logs {
		r.Logs[i].Text = Scrub(r.Logs[i].Text)
	}
}

// Write 构建 zip。
//
// 报告在本函数内部先脱敏，而不是信任调用方：这是字节离开进程前的
// 最后一站，而诊断包会被上传到公开的 issue 跟踪器。
func Write(w io.Writer, r Report) error {
	ScrubReport(&r)
	if r.Created.IsZero() {
		r.Created = time.Now()
	}
	sort.SliceStable(r.Logs, func(i, j int) bool { return r.Logs[i].Time < r.Logs[j].Time })

	zw := zip.NewWriter(w)
	// report.json 是机器可读部分。
	if err := writeJSON(zw, "report.json", r); err != nil {
		return err
	}
	// summary.txt 是人首先阅读的部分：同样的事实，用散文呈现。
	if err := writeText(zw, "summary.txt", summary(r)); err != nil {
		return err
	}
	return zw.Close()
}

// writeJSON 添加一个 JSON 成员。
func writeJSON(zw *zip.Writer, name string, v any) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeText 添加一个纯文本成员。
func writeText(zw *zip.Writer, name, text string) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, text)
	return err
}

// summary 把报告渲染成简短文本，维护者无需打开 zip 就能在 issue 中阅读。
func summary(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "vRay diagnostic bundle\n")
	fmt.Fprintf(&b, "created   %s\n", r.Created.Format(time.RFC3339))
	fmt.Fprintf(&b, "app       %s\n", r.App)
	fmt.Fprintf(&b, "platform  %s\n", r.Platform)
	fmt.Fprintf(&b, "phase     %s\n", r.Phase)
	fmt.Fprintf(&b, "mode      %s\n", r.Mode)
	fmt.Fprintf(&b, "geo       %s\n", r.GeoState)
	if r.Problem != nil {
		fmt.Fprintf(&b, "\nproblem   [%s] %s\n", r.Problem.Code, r.Problem.Message)
		if r.Problem.Detail != "" {
			fmt.Fprintf(&b, "          %s\n", r.Problem.Detail)
		}
	}
	fmt.Fprintf(&b, "\nnodes     %d\n", len(r.Nodes))
	for _, n := range r.Nodes {
		fmt.Fprintf(&b, "  %-12s %s:%d  security=%s  from=%s\n",
			n.Protocol, n.Address, n.Port, orNone(n.Security), orNone(n.Source))
	}
	fmt.Fprintf(&b, "\nsources   %d\n", len(r.Sources))
	for _, s := range r.Sources {
		state := "enabled"
		if !s.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(&b, "  %s (%s, %d nodes) %s\n", s.Name, state, s.NodeCount, s.URL)
	}
	fmt.Fprintf(&b, "\nchecks    %d\n", len(r.Checks))
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "  %-8s %s — %s\n", c.State, c.Name, c.Detail)
	}
	fmt.Fprintf(&b, "\nfiles     %d\n", len(r.Files))
	for _, f := range r.Files {
		fmt.Fprintf(&b, "  %-24s %d bytes\n", f.Name, f.Size)
	}
	fmt.Fprintf(&b, "\nlog lines %d\n", len(r.Logs))
	return b.String()
}

// orNone 为空字段命名，而不是留下空白。
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// Bytes 把 Write 的输出写入缓冲区，供需要内存中 zip 的调用方使用。
func Bytes(r Report) ([]byte, error) {
	var buf bytes.Buffer
	if err := Write(&buf, r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
