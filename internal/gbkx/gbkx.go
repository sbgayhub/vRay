// Package gbkx 以表的形式保存 GBK 代码页，使程序能够读取以中文 Windows
// 系统代码页写入的文本。
//
// 它只负责一项工作：撤销乱码往返。按 GBK 解码过的 UTF-8 字节串，可以通过
// 将每个字符重新编码为 GBK 字节，再按 UTF-8 读取这些字节来恢复。
//
// 该表由 Unicode 自身的映射数据生成（生成器位于 tools/fixutf8）；它不是手工
// 编写的，因为手写代码页必然会有缺漏。
package gbkx

// Encode 返回 s 的 GBK 字节；若 s 含有 GBK 无法表示的字符，则返回 false。
// ASCII 映射到自身。
func Encode(s string) ([]byte, bool) {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 0x80 {
			out = append(out, byte(r))
			continue
		}
		b, ok := encodeRune(r)
		if !ok {
			return nil, false
		}
		out = append(out, b[0], b[1])
	}
	return out, true
}

// Decode 返回 GBK 字节对应的文本；若字节不是有效 GBK，则返回 false。
func Decode(b []byte) (string, bool) {
	out := make([]rune, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] < 0x80 {
			out = append(out, rune(b[i]))
			i++
			continue
		}
		if i+1 >= len(b) {
			return "", false
		}
		r, ok := decodePair([2]byte{b[i], b[i+1]})
		if !ok {
			return "", false
		}
		out = append(out, r)
		i += 2
	}
	return string(out), true
}
