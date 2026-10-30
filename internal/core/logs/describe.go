package logs

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// 在收集器和诊断文件中保留原始条目。只有读取器使用这些描述；
// 地址、协议标签和 ID 保持不变。
var logPhrases = map[string]string{
	"failed to process outbound traffic":      "处理出站流量失败",
	"failed to process inbound traffic":       "处理入站流量失败",
	"failed to establish connection":          "建立连接失败",
	"failed to dial":                          "拨号失败",
	"failed to read response header":          "读取响应头失败",
	"failed to read request header":           "读取请求头失败",
	"failed to get destination":               "获取目标地址失败",
	"failed to find an available destination": "找不到可用目标地址",
	"connection refused":                      "连接被拒绝",
	"connection reset by peer":                "远端重置连接",
	"connection timed out":                    "连接超时",
	"i/o timeout":                             "读写超时",
	"context deadline exceeded":               "操作超时",
	"context canceled":                        "操作已取消",
	"network is unreachable":                  "网络不可达",
	"no such host":                            "域名不存在或无法解析",
	"tls handshake timeout":                   "TLS 握手超时",
	"certificate has expired":                 "证书已过期",
	"certificate signed by unknown authority": "证书签发机构不受信任",
	"use of closed network connection":        "网络连接已关闭",
	"tcp connect request to":                  "TCP 连接请求，目标",
	"udp connect request to":                  "UDP 连接请求，目标",
	"dialing tcp to":                          "正在建立 TCP 连接，目标",
	"dialing udp to":                          "正在建立 UDP 连接，目标",
	"opening connection to":                   "正在建立连接，目标",
	"taking detour":                           "选择出站线路",
	"default route for":                       "使用默认路由，目标",
	"sniffed domain":                          "探测到域名",
	"listening tcp on":                        "正在监听 TCP 地址",
	"listening udp on":                        "正在监听 UDP 地址",
	"tunneling request to":                    "转发隧道请求，目标",
}

var logPhrasePattern = func() *regexp.Regexp {
	phrases := make([]string, 0, len(logPhrases))
	for phrase := range logPhrases {
		phrases = append(phrases, regexp.QuoteMeta(phrase))
	}
	// 优先匹配完整短语，而不是 "failed" 等更短的通用事件。
	sort.Slice(phrases, func(i, j int) bool {
		if len(phrases[i]) == len(phrases[j]) {
			return phrases[i] < phrases[j]
		}
		return len(phrases[i]) > len(phrases[j])
	})
	return regexp.MustCompile(`(?i)\b(` + strings.Join(phrases, "|") + `)\b`)
}()

func Describe(text string) string {
	translated := logPhrasePattern.ReplaceAllStringFunc(text, func(phrase string) string { return logPhrases[strings.ToLower(phrase)] })
	if strings.HasPrefix(translated, "Xray ") && strings.HasSuffix(translated, " started") {
		translated = strings.TrimSuffix(translated, " started") + " 已启动"
	}
	for _, event := range []struct{ en, zh string }{{"accepted", "已接收连接"}, {"rejected", "已拒绝连接"}} {
		translated = strings.ReplaceAll(translated, " "+event.en+" ", " "+event.zh+" ")
		if strings.HasPrefix(translated, event.en+" ") {
			translated = event.zh + strings.TrimPrefix(translated, event.en)
		}
	}
	for _, r := range translated {
		if unicode.Is(unicode.Han, r) {
			return translated
		}
	}
	return "内核记录：" + translated
}
