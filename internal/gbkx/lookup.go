package gbkx

import "sort"

// encodeRune 返回 r 对应的两个代码页字节；若代码页没有该字符，则返回 false。
//
// 表按代码页顺序而非 Unicode 顺序排列，因此这里采用扫描。它每次只处理一个
// 文件中的每个字符，并不在应用运行时执行。
func encodeRune(r rune) ([2]byte, bool) {
	for i := 0; i < tableSize; i++ {
		if tableRune[i] == r {
			return [2]byte{tableLead[i], tableTrail[i]}, true
		}
	}
	return [2]byte{}, false
}

// decodePair 返回一对字节所表示的字符；若代码页没有该字节对，则返回 false。
func decodePair(b [2]byte) (rune, bool) {
	i := sort.Search(tableSize, func(i int) bool {
		if tableLead[i] != b[0] {
			return tableLead[i] > b[0]
		}
		return tableTrail[i] >= b[1]
	})
	if i < tableSize && tableLead[i] == b[0] && tableTrail[i] == b[1] {
		return tableRune[i], true
	}
	return 0, false
}
