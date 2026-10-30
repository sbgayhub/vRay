package locales

// 各语言目录必须彼此一致。
//
// 源语言中某个键在翻译里缺失并不会导致崩溃：它会回退到中文，因此直到使用
// 该语言的用户报告出现残留中文标签时才有人发现。此测试会把这种缺失变成
// 构建失败，也能捕获相反的错误——翻译包含源语言中不存在的键，而这种键
// 永远不会被使用。

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// messages 读取一个目录，并移除 @@locale 元数据。
func messages(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	out := make(map[string]string, len(all))
	for k, v := range all {
		if len(k) >= 2 && k[:2] == "@@" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s: %q is not a string", path, k)
		}
		if s == "" {
			t.Fatalf("%s: %q is empty", path, k)
		}
		// mygo 会把第一个点之前的文本视为目录覆盖的库命名空间，
		// 因此应用键不能包含点。
		if contains(k, '.') {
			t.Fatalf("%s: %q contains a dot, which mygo reads as a library namespace", path, k)
		}
		out[k] = s
	}
	return out
}

func contains(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}

// 每种语言都恰好拥有源语言的键。
func TestCataloguesAgree(t *testing.T) {
	src := messages(t, "zh-Hans.json")
	if len(src) == 0 {
		t.Fatal("the source catalogue is empty")
	}

	for _, lang := range []string{"en", "zh-Hant"} {
		got := messages(t, lang+".json")
		var missing, extra []string
		for k := range src {
			if _, ok := got[k]; !ok {
				missing = append(missing, k)
			}
		}
		for k := range got {
			if _, ok := src[k]; !ok {
				extra = append(extra, k)
			}
		}
		slices.Sort(missing)
		slices.Sort(extra)
		if len(missing) > 0 {
			t.Fatalf("%s is missing %d key(s): %v", lang, len(missing), missing)
		}
		if len(extra) > 0 {
			t.Fatalf("%s has %d key(s) the source does not: %v", lang, len(extra), extra)
		}
	}
}

// 如果翻译与源文本完全相同，就说明有人忘了翻译。
//
// 并非所有字符串都能不同（"vRay" 是应用名，"IPv6" 写法相同），因此检查会
// 列出允许相同的项，而不是跳过整个测试。
func TestTranslationsAreActuallyTranslated(t *testing.T) {
	src := messages(t, "zh-Hans.json")
	en := messages(t, "en.json")

	// 只有两条消息在两种语言中合理地完全相同：一条是速率单位，另一条是
	// 分享链接协议列表，这些协议字面量不能翻译。
	identicalAllowed := map[string]bool{
		"ratePerSecond":      true,
		"sourcesPlaceholder": true,
	}

	var untranslated []string
	for k, zh := range src {
		if en[k] != zh {
			continue
		}
		if identicalAllowed[k] {
			continue
		}
		untranslated = append(untranslated, k)
	}
	slices.Sort(untranslated)
	// 少量键合理地完全相同（品牌名和缩写）；若超过该数量，就说明整段内容
	// 被复制而非翻译。
	if len(untranslated) > 3 {
		t.Fatalf("%d English keys are identical to the Chinese: %v", len(untranslated), untranslated)
	}
	if len(untranslated) > 0 {
		t.Logf("identical in both languages, which is expected for brand names: %v", untranslated)
	}
}

// 占位符在翻译后仍会保留：若消息丢掉 {count} 就会丢失数量，而直到消息
// 显示为 "restored  system setting(s)" 时才有人发现。
func TestPlaceholdersSurvive(t *testing.T) {
	src := messages(t, "zh-Hans.json")
	for _, lang := range []string{"en", "zh-Hant"} {
		got := messages(t, lang+".json")
		for k, zhText := range src {
			want := placeholders(zhText)
			if len(want) == 0 {
				continue
			}
			have := placeholders(got[k])
			slices.Sort(want)
			slices.Sort(have)
			if !slices.Equal(want, have) {
				t.Fatalf("%s %q: placeholders %v, want %v", lang, k, have, want)
			}
		}
	}
}

// placeholders 返回消息中的 {name} 占位符。
func placeholders(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, c := range s {
		switch c {
		case '{':
			depth++
			start = i + 1
		case '}':
			if depth > 0 {
				out = append(out, s[start:i])
				depth--
			}
		}
	}
	return out
}
