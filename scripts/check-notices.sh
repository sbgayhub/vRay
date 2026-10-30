#!/bin/sh
# 当 THIRD_PARTY_NOTICES.md 与模块图不一致时使检查失败。
#
# 若声明文件列出构建已不再使用的模块，或遗漏仍在使用的模块，
# 就会构成许可证缺陷：有人的许可证正被悄悄违反。此脚本会
# 双向核对生成的表格与 `go list -m all` 的结果。
#
# 用法：
#   scripts/check-notices.sh            # 检查
#   scripts/check-notices.sh --update   # 重写生成的模块表
set -eu

cd "$(dirname "$0")/.."

UPDATE=0
for arg in "$@"; do
	case "$arg" in
	--update) UPDATE=1 ;;
	-h | --help)
		sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "check-notices: unknown argument $arg" >&2
		exit 2
		;;
	esac
done

NOTICES=THIRD_PARTY_NOTICES.md
[ -f "$NOTICES" ] || {
	echo "check-notices: $NOTICES is missing" >&2
	exit 1
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# 只列出其包实际进入二进制文件的模块，而不是整个模块图：
# `go list -m all` 还会列出仅由依赖项的测试或工具使用的模块；
# 若声明文件提及上百个无人链接的包，这些噪声会掩盖真正
# 重要的模块。
#
# deps 提供主模块间接导入的包所属的模块。
go list -deps -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}' . |
	grep -v '^$' |
	grep -v '^github.com/sbgayhub/vray ' |
	sort -u >"$tmp/want"

# 标记之间的表格。
BEGIN='<!-- BEGIN GENERATED -->'
END='<!-- END GENERATED -->'
awk -v b="$BEGIN" -v e="$END" '
	$0 == b { on = 1; next }
	$0 == e { on = 0 }
	on && /^\|/ { print }
' "$NOTICES" |
	sed -n 's/^| `\([^`]*\)` | \([^|]*\) |$/\1 \2/p' |
	sed 's/ *$//' |
	sort >"$tmp/have"

if [ "$UPDATE" -eq 1 ]; then
	{
		echo "$BEGIN"
		echo '| Module | Version |'
		echo '|---|---|'
		sed 's/^\(.*\) \(.*\)$/| `\1` | \2 |/' "$tmp/want"
		echo "$END"
	} >"$tmp/table"
	# 围绕新表格重建文件。
	awk -v b="$BEGIN" -v e="$END" -v t="$tmp/table" '
		$0 == b { while ((getline line < t) > 0) print line; skipping = 1; next }
		$0 == e { skipping = 0; next }
		!skipping { print }
	' "$NOTICES" >"$tmp/notices"
	mv "$tmp/notices" "$NOTICES"
	echo "updated the generated table in $NOTICES"
	exit 0
fi

missing=$(comm -23 "$tmp/want" "$tmp/have" || true)
extra=$(comm -13 "$tmp/want" "$tmp/have" || true)

if [ -n "$missing" ]; then
	echo "modules in the build but not in $NOTICES:" >&2
	echo "$missing" | sed 's/^/  /' >&2
fi
if [ -n "$extra" ]; then
	echo "modules in $NOTICES but not in the build:" >&2
	echo "$extra" | sed 's/^/  /' >&2
fi

if [ -n "$missing$extra" ]; then
	echo "" >&2
	echo "run scripts/check-notices.sh --update and review the diff" >&2
	exit 1
fi

echo "third-party notices match the module graph"
