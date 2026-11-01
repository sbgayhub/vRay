package main

import (
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/sbgayhub/vray/internal/gbkx"
)

// mkfix 将其参数的 UTF-8 字节按系统代码页读取，并输出这些字节所表示的文本。
//
// 它用于恢复被有损往返损坏的一行：将 UTF-8 按 GBK 读取就是损坏过程，因此
// 将损坏文本的 UTF-8 字节再次按 GBK 读取即可还原。
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mkfix damaged...")
		os.Exit(2)
	}
	for _, s := range os.Args[1:] {
		fixed, ok := gbkx.Decode([]byte(s))
		if !ok || !utf8.ValidString(fixed) {
			fmt.Printf("cannot decode: %q\n", s)
			continue
		}
		fmt.Println(fixed)
	}
}
