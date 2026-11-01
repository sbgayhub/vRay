// Command fixutf8 修复因有损编码往返而变成乱码的中文文本。
//
// 这里发生过两次：PowerShell 的 `-replace` 将 UTF-8 Go 文件按系统 GBK
// 代码页读取、编辑后再写回，导致每个中文字符串字面量都变成乱码（有些还丢失了
// 结尾引号）。对于未被有损步骤截断的行，这种损坏完全可以逆推：取乱码文本，
// 编码为 GBK 字节，再按 UTF-8 读回。
//
// 无法干净解码的行会保持原样并报告，因为错误的“修复”比不修复更糟。已经是有效
// 中文的文本绝不会被改动：它不是乱码，因此无法解码。
//
// 用法：
//
//	fixutf8 [-w] files...
//
// 不加 -w 时只报告将会改动的内容。
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	write := flag.Bool("w", false, "write the repaired text back")
	check := flag.Bool("check", false, "exit non-zero if any file needs repair")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: fixutf8 [-w] files...")
		os.Exit(2)
	}

	bad := 0
	for _, path := range flag.Args() {
		switch {
		case *check:
			needs, err := NeedsRepair(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				os.Exit(2)
			}
			if needs != "" {
				fmt.Printf("%s line %s: needs repair\n", path, needs)
				bad++
			}
		case *write:
			changed, err := Repair(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				os.Exit(2)
			}
			if changed {
				fmt.Println("repaired", path)
			}
		default:
			report, err := DryRun(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				os.Exit(2)
			}
			if report != "" {
				fmt.Print(report)
			}
		}
	}
	if bad > 0 {
		os.Exit(1)
	}
}
