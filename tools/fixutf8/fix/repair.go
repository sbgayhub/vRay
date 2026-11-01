package main

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/sbgayhub/vray/internal/gbkx"
)

// Repair 就地解码一个文件，并报告是否有改动。
func Repair(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	fixed, changed := repairText(string(raw))
	if !changed {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// DryRun 说明 Repair 会做什么，但不改动文件。
func DryRun(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, line := range strings.Split(string(raw), "\n") {
		fixed, ok := decodeLine(line)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "%s:%d:\n  - %s\n  + %s\n", path, i+1, strings.TrimSpace(line), strings.TrimSpace(fixed))
	}
	return b.String(), nil
}

// NeedsRepair 返回第一个可修复行的行号；若没有，则返回 ""。
func NeedsRepair(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for i, line := range strings.Split(string(raw), "\n") {
		if _, ok := decodeLine(line); ok {
			return fmt.Sprint(i + 1), nil
		}
	}
	return "", nil
}

// repairText 修复每一行可解码的内容，其余行保持不变。
func repairText(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	changed := false
	for i, line := range lines {
		fixed, ok := decodeLine(line)
		if !ok {
			continue
		}
		lines[i] = fixed
		changed = true
	}
	return strings.Join(lines, "\n"), changed
}

// decodeLine 还原一行的损坏内容，并报告是否成功。
//
// 损坏过程通过系统代码页读取 UTF-8 字节，因此逆向操作也使用该代码页：将损坏
// 文本编码为 GBK，再把这些字节按 UTF-8 读取。
//
// 仅在结果可信时才返回成功。代码页是有损的，若损坏行中的非 ASCII 字符全部
// 位于符号区，解码后会得到在此处没有实际含义的符号——明显特征就是没有恢复
// 任何 CJK 字符。围绕中文产生的真实损坏总会留下一些 CJK 范围内的字符，而
// 本身已是中文的文本根本无法解码。
func decodeLine(line string) (string, bool) {
	if line == "" {
		return "", false
	}
	raw, ok := gbkx.Encode(line)
	if !ok {
		return "", false
	}
	fixed := string(raw)
	if fixed == line || !utf8.ValidString(fixed) {
		return "", false
	}
	for _, r := range fixed {
		if isCJK(r) {
			return fixed, true
		}
	}
	return "", false
}

// isCJK 报告 r 是否为汉字，或 CJK 标点、全角形式：这些是中文文本的组成部分。
func isCJK(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF: // 汉字
		return true
	case r >= 0x3400 && r <= 0x4DBF: // 汉字扩展 A
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK 标点
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // 全角形式
		return true
	}
	return false
}
