#!/bin/sh
# 二进制文件超出约定预算时使构建失败。
#
# vRay 在进程中内嵌完整的 Xray 核心，这一决定有代价：可执行文件
# 有数十兆字节，而不是几兆。设置门槛才能让这项代价保持
# 可知。否则每次改动都会让体积增加几兆，直到达到承诺值的
# 两倍才有人察觉。
#
# 特意检查两项预算：
#   - 未裁剪构建，即开发期间 `go build` 生成的产物；
#   - 裁剪构建（-s -w），即用于发布的产物。
# 两项预算都在此处校验；提高预算属于产品决策，不是构建修复。
#
# 用法：
#   scripts/check-size.sh              # 检查当前平台
#   scripts/check-size.sh --report     # 打印数值，始终不失败
#   VRAY_SIZE_BUDGET_MB=40 scripts/check-size.sh
#
# 预算单位为 MiB（1024*1024），即计划中使用的单位。
set -eu

# 预算。未裁剪数值预留了使用不同 Go 版本构建的余量，而裁剪
# 数值是发布时承诺的上限。
BUDGET_UNTRIMMED_MB=${VRAY_SIZE_BUDGET_MB:-56}
BUDGET_TRIMMED_MB=${VRAY_SIZE_TRIMMED_MB:-40}

REPORT=0
for arg in "$@"; do
	case "$arg" in
	--report) REPORT=1 ;;
	-h | --help)
		sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "check-size: unknown argument $arg" >&2
		exit 2
		;;
	esac
done

cd "$(dirname "$0")/.."

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# 本项目始终关闭 CGO：二进制文件必须可移植，且构建过程必须能在
# 没有 C 工具链的环境中运行。
CGO_ENABLED=0 go build -o "$tmp/vray" .
CGO_ENABLED=0 go build -ldflags='-s -w' -o "$tmp/vray-trimmed" .

size_of() {
	# macOS 和 Linux 的 stat 命令不同；遇到异常情况时回退到 wc。
	if size=$(stat -c %s "$1" 2>/dev/null); then
		echo "$size"
	elif size=$(stat -f %z "$1" 2>/dev/null); then
		echo "$size"
	else
		wc -c <"$1" | tr -d ' '
	fi
}

to_mb() {
	# 整数 MiB，向下取整：与用户在文件管理器中
	# 读取的数值一致。
	echo $(($1 / 1024 / 1024))
}

fail=0

check() {
	label=$1
	file=$2
	budget=$3

	bytes=$(size_of "$file")
	mb=$(to_mb "$bytes")
	printf '%-14s %6s MiB  (budget %s MiB)\n' "$label" "$mb" "$budget"

	if [ "$mb" -gt "$budget" ]; then
		if [ "$REPORT" -eq 1 ]; then
			printf '  over budget by %s MiB (report mode)\n' "$((mb - budget))"
		else
			printf '  OVER BUDGET by %s MiB\n' "$((mb - budget))" >&2
			fail=1
		fi
	fi
}

check "untrimmed" "$tmp/vray" "$BUDGET_UNTRIMMED_MB"
check "trimmed" "$tmp/vray-trimmed" "$BUDGET_TRIMMED_MB"

if [ "$fail" -ne 0 ]; then
	cat >&2 <<'EOF'

The binary grew past its budget. Before raising the number, check which of
these happened:

  - a new dependency was added (go.mod) that pulls a large package in;
  - a whole test-only or offline helper package got linked into main;
  - the Xray core version changed and upstream grew.

Raising the budget is a product decision, not a build fix.

EOF
	exit 1
fi

if [ "$REPORT" -eq 1 ]; then
	echo "sizes are within budget (report mode)"
else
	echo "sizes are within budget"
fi

