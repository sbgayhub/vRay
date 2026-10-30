package configgen

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// RuleNeedsGeo 报告求值该规则是否需要 geo 数据文件。
func RuleNeedsGeo(r Rule) bool {
	for _, domain := range r.Domains {
		if strings.HasPrefix(domain, "geosite:") {
			return true
		}
	}
	for _, ip := range r.IPs {
		if strings.HasPrefix(ip, "geoip:") {
			return true
		}
	}
	return false
}

// Field 是规则校验器报告问题所对应的字段，表单可把消息放在持有坏值的控件下方。
const (
	RuleFieldDomains = "domains"
	RuleFieldIPs     = "ips"
	RuleFieldPorts   = "ports"
	RuleFieldProcess = "process"
)

// RuleProblem 是规则无法使用的一个值。
type RuleProblem struct {
	Field   string // 字段：domains | ips | ports | process
	Value   string
	Message string
}

// ValidateRule 在不构建配置的情况下校验规则的匹配值。
//
// 它让编辑器在用户输入时就指出问题，而不是让错误的 CIDR 进入 Build，
// 直到连接时才收到一条关于配置文件的消息。所有问题都会返回：
// 只报首个错误的校验器会让修规则变成猜谜。
func ValidateRule(r Rule) []RuleProblem {
	var out []RuleProblem
	for _, d := range r.Domains {
		if msg := domainProblem(d); msg != "" {
			out = append(out, RuleProblem{RuleFieldDomains, d, msg})
		}
	}
	for _, ip := range r.IPs {
		if msg := ipProblem(ip); msg != "" {
			out = append(out, RuleProblem{RuleFieldIPs, ip, msg})
		}
	}
	if msg := portsProblem(r.Ports); msg != "" {
		out = append(out, RuleProblem{RuleFieldPorts, r.Ports, msg})
	}
	for _, p := range r.Process {
		if strings.TrimSpace(p) == "" {
			out = append(out, RuleProblem{RuleFieldProcess, p, "进程名不能为空"})
		}
	}
	return out
}

// domainProblem 接受 Xray 路由器理解的域名形式。
//
// 裸域名、前导点后缀，以及路由器解析的前缀形式（domain:、full:、regexp:、
// keyword:、geosite:）都有效；其他内容（如误入的 URL 或中间空格）无效。
func domainProblem(d string) string {
	v := strings.TrimSpace(d)
	if v == "" {
		return "域名不能为空"
	}
	if strings.ContainsAny(v, " \t") {
		return "域名不能包含空格"
	}
	if strings.Contains(v, "://") {
		return "这里只填域名，不要带协议前缀"
	}
	// 路由器解析的前缀。剩余部分按域名校验，因此 "domain:exa mple.com"
	// 仍会被抓住；geosite 与 regexp 名称对我们不透明，只需非空。
	for _, prefix := range []string{"geosite:", "regexp:"} {
		if rest, ok := strings.CutPrefix(v, prefix); ok {
			if strings.TrimSpace(rest) == "" {
				return fmt.Sprintf("%s 后面不能为空", prefix)
			}
			return ""
		}
	}
	for _, prefix := range []string{"domain:", "full:", "keyword:"} {
		if rest, ok := strings.CutPrefix(v, prefix); ok {
			if strings.TrimSpace(rest) == "" {
				return fmt.Sprintf("%s 后面不能为空", prefix)
			}
			// keyword: 是子串匹配，不需要点号。
			if prefix == "keyword:" {
				if strings.ContainsAny(rest, " \t") {
					return "keyword 不能包含空格"
				}
				return ""
			}
			return bareDomainProblem(rest)
		}
	}
	return bareDomainProblem(v)
}

// bareDomainProblem 校验没有路由器前缀的域名。
func bareDomainProblem(v string) string {
	// ".example.com" 是后缀匹配。
	v = strings.TrimPrefix(v, ".")
	if !strings.Contains(v, ".") {
		return "域名至少要有一个点，或用 keyword:/domain: 前缀"
	}
	if strings.HasSuffix(v, ".") {
		return "域名结尾不能是点"
	}
	if strings.Contains(v, "..") {
		return "域名里不能有连续的点"
	}
	if strings.Contains(v, ":") {
		return "域名里不能带端口"
	}
	return ""
}

// ipProblem 接受地址、CIDR 段或 geoip: 名称。
func ipProblem(ip string) string {
	v := strings.TrimSpace(ip)
	if v == "" {
		return "IP 不能为空"
	}
	if rest, ok := strings.CutPrefix(v, "geoip:"); ok {
		if strings.TrimSpace(rest) == "" {
			return "geoip: 后面不能为空"
		}
		return ""
	}
	if strings.Contains(v, "/") {
		if _, _, err := net.ParseCIDR(v); err != nil {
			return "不是合法的 CIDR 网段"
		}
		return ""
	}
	if net.ParseIP(v) == nil {
		return "不是合法的 IP 地址"
	}
	return ""
}

// portsProblem 校验 Xray 的端口列表语法：逗号分隔的端口与范围，
// 以及环境变量形式（"env:NAME"）。
//
// Xray 在解析配置时会拒绝畸形端口串；在这里抓住它，就是字段错误与
// 连接失败之间的差别。
func portsProblem(p string) string {
	v := strings.TrimSpace(p)
	if v == "" {
		return ""
	}
	if name, ok := strings.CutPrefix(v, "env:"); ok {
		if strings.TrimSpace(name) == "" {
			return "env: 后面不能为空"
		}
		return ""
	}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return "端口列表里有空项"
		}
		lo, hi, isRange := strings.Cut(part, "-")
		low, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return fmt.Sprintf("%q 不是端口号", part)
		}
		high := low
		if isRange {
			if high, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return fmt.Sprintf("%q 不是端口范围", part)
			}
			if high < low {
				return fmt.Sprintf("%q 的范围是倒的", part)
			}
		}
		if low < 1 || low > 65535 || high < 1 || high > 65535 {
			return fmt.Sprintf("%q 超出 1-65535", part)
		}
	}
	return ""
}
