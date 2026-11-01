package locales

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"sync"
)

var sourceIndex = sync.OnceValue(func() map[string]string {
	raw, _ := files.ReadFile("zh-Hans.json")
	var messages map[string]string
	_ = json.Unmarshal(raw, &messages)
	index := make(map[string]string, len(messages))
	for key, text := range messages {
		if key != "@@locale" {
			index[text] = key
		}
	}
	return index
})

type formattedMessage struct {
	pattern     *regexp.Regexp
	source, key string
}

var formatToken = regexp.MustCompile(`%[-+0-9.]*[dsf]`)
var formattedMessages = sync.OnceValue(func() []formattedMessage {
	result := []formattedMessage{}
	for source, key := range sourceIndex() {
		if !formatToken.MatchString(source) {
			continue
		}
		var pattern strings.Builder
		pattern.WriteString("^")
		last := 0
		for _, token := range formatToken.FindAllStringIndex(source, -1) {
			pattern.WriteString(regexp.QuoteMeta(strings.ReplaceAll(source[last:token[0]], "%%", "%")))
			switch source[token[1]-1] {
			case 'd':
				pattern.WriteString(`([+-]?[0-9]+)`)
			case 'f':
				pattern.WriteString(`([+-]?[0-9]+(?:\.[0-9]+)?)`)
			default:
				pattern.WriteString(`(.+?)`)
			}
			last = token[1]
		}
		pattern.WriteString(regexp.QuoteMeta(strings.ReplaceAll(source[last:], "%%", "%")))
		pattern.WriteString("$")
		result = append(result, formattedMessage{regexp.MustCompile(pattern.String()), source, key})
	}
	slices.SortFunc(result, func(a, b formattedMessage) int {
		if len(a.source) != len(b.source) {
			return len(b.source) - len(a.source)
		}
		return strings.Compare(a.source, b.source)
	})
	return result
})

// translateCache 记住 (语言, 源串) 的查询结果，包括“无译文”的未命中。
// 视图每帧会对同一字符串反复翻译；未收录的字符串还要对全部带占位符的
// 消息逐一正则匹配。缓存让每个字符串只付出一次匹配成本。
var translateCache sync.Map // translateKey -> string

type translateKey struct{ language, source string }

// Translate 查询显式的源 UI 消息。用户内容不会经过此函数。缺少条目时保留源语言，仍可阅读。
func Translate(language, source string) string {
	if language == "" || language == "zh-Hans" {
		return source
	}
	key := translateKey{language, source}
	if v, ok := translateCache.Load(key); ok {
		return v.(string)
	}
	translated := translateSlow(language, source)
	translateCache.Store(key, translated)
	return translated
}

// translateSlow 执行目录查询与格式模板匹配；结果由调用方缓存。
func translateSlow(language, source string) string {
	if key := sourceIndex()[source]; key != "" {
		return Catalog.Format(language, key, nil)
	}
	if strings.HasPrefix(source, "✓ ") {
		return "✓ " + Translate(language, strings.TrimPrefix(source, "✓ "))
	}
	for _, message := range formattedMessages() {
		matches := message.pattern.FindStringSubmatch(source)
		if matches == nil {
			continue
		}
		template := Catalog.Format(language, message.key, nil)
		i := 0
		translated := formatToken.ReplaceAllStringFunc(template, func(_ string) string {
			i++
			if i < len(matches) {
				return matches[i]
			}
			return ""
		})
		return strings.ReplaceAll(translated, "%%", "%")
	}
	return source
}
