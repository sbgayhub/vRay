#!/bin/sh
# 当 go.mod 中的 mygo 偏离固定版本时使检查失败。
#
# CrychicUI 声明依赖 mygo v0.5.5，因此 `go mod tidy` 会悄悄降级，
# 导致 HideWindowButtons 无法编译（docs/03 P1）。每次 tidy 后都要运行。
set -eu

want="v0.7.1"
got="$(go list -m -f '{{.Version}}' github.com/egoist/mygo)"

if [ "$got" != "$want" ]; then
	echo "mygo is $got, want $want" >&2
	echo "fix: go get github.com/egoist/mygo@$want" >&2
	exit 1
fi

echo "mygo $got"
