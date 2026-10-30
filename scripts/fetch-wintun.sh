#!/bin/sh
# 下载适用于目标 Windows 架构的 wintun.dll，并固定其哈希。
#
# 这是 scripts/fetch-wintun.ps1 的 POSIX 对应脚本。Windows TUN 需要
# resources/windows-<arch>/ 下的 wintun.dll，mygo 会将其复制到可执行文件
# 旁；该 DLL 因架构而异，因此为其他架构构建时需要各自的副本。
#
# 关于哈希：此文件不包含手写的 SHA-256。凭记忆输入的哈希看似
# 在验证，实则只是形式。首次下载会将实际观察到的哈希写入
# resources/windows-<arch>/wintun.sha256；该文件会提交到仓库，
# 后续每次下载和发布构建都会以它为准进行校验。
#
# 用法：
#   scripts/fetch-wintun.sh              # amd64 + arm64
#   scripts/fetch-wintun.sh amd64
#   scripts/fetch-wintun.sh --verify     # 仅检查目录内容，不访问网络
set -eu

WINTUN_VERSION=0.14.1
WINTUN_URL="https://www.wintun.net/builds/wintun-$WINTUN_VERSION.zip"

cd "$(dirname "$0")/.."
root=$(pwd)

VERIFY=0
targets=""
for arg in "$@"; do
	case "$arg" in
	--verify) VERIFY=1 ;;
	-h | --help)
		sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	amd64 | arm64 | 386) targets="$targets $arg" ;;
	*)
		echo "fetch-wintun: unknown argument $arg" >&2
		exit 2
		;;
	esac
done
[ -n "$targets" ] || targets=" amd64 arm64"

# 压缩包中的 32 位目录名为 x86；Go 称该架构为 386。
archive_dir() {
	case "$1" in
	386) echo x86 ;;
	*) echo "$1" ;;
	esac
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		echo "fetch-wintun: no sha256sum or shasum available" >&2
		exit 1
	fi
}

if [ "$VERIFY" -eq 1 ]; then
	bad=0
	for a in $targets; do
		dll="resources/windows-$a/wintun.dll"
		pin="resources/windows-$a/wintun.sha256"
		if [ ! -f "$dll" ]; then
			echo "  windows-$a: wintun.dll missing ($dll)" >&2
			bad=1
			continue
		fi
		if [ ! -f "$pin" ]; then
			echo "  windows-$a: wintun.sha256 missing, so the DLL is unverified" >&2
			bad=1
			continue
		fi
		want=$(tr -d '[:space:]' <"$pin" | tr 'A-Z' 'a-z')
		got=$(sha256_of "$dll")
		if [ "$got" != "$want" ]; then
			echo "  windows-$a: hash mismatch (got $got, pinned $want)" >&2
			bad=1
		else
			echo "windows-$a: ok ($got)"
		fi
	done
	if [ "$bad" -ne 0 ]; then
		echo "wintun.dll is not ready for release; run scripts/fetch-wintun.sh" >&2
		exit 1
	fi
	echo "all wintun.dll files verified"
	exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

need_download=0
for a in $targets; do
	dll="resources/windows-$a/wintun.dll"
	pin="resources/windows-$a/wintun.sha256"
	if [ -f "$dll" ] && [ -f "$pin" ]; then
		want=$(tr -d '[:space:]' <"$pin" | tr 'A-Z' 'a-z')
		if [ "$(sha256_of "$dll")" = "$want" ]; then
			echo "windows-$a: already present and matches the pin"
			continue
		fi
	fi
	need_download=1
done

if [ "$need_download" -eq 1 ]; then
	echo "downloading wintun $WINTUN_VERSION from $WINTUN_URL"
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$tmp/wintun.zip" "$WINTUN_URL"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$tmp/wintun.zip" "$WINTUN_URL"
	else
		echo "fetch-wintun: neither curl nor wget is available" >&2
		exit 1
	fi
	unzip -q "$tmp/wintun.zip" -d "$tmp/x"
fi

for a in $targets; do
	dll="resources/windows-$a/wintun.dll"
	pin="resources/windows-$a/wintun.sha256"
	if [ -f "$dll" ] && [ -f "$pin" ]; then
		want=$(tr -d '[:space:]' <"$pin" | tr 'A-Z' 'a-z')
		if [ "$(sha256_of "$dll")" = "$want" ]; then
			continue
		fi
	fi

	src="$tmp/x/wintun/bin/$(archive_dir "$a")/wintun.dll"
	if [ ! -f "$src" ]; then
		echo "fetch-wintun: the archive has no wintun/bin/$(archive_dir "$a")/wintun.dll" >&2
		exit 1
	fi
	hash=$(sha256_of "$src")

	if [ -f "$pin" ]; then
		want=$(tr -d '[:space:]' <"$pin" | tr 'A-Z' 'a-z')
		if [ "$hash" != "$want" ]; then
			echo "fetch-wintun: windows-$a hash $hash does not match the committed pin $want." >&2
			echo "If the pin is genuinely outdated, delete $pin and re-run, then review the new hash." >&2
			exit 1
		fi
		echo "windows-$a: matches the existing pin"
	else
		mkdir -p "resources/windows-$a"
		printf '%s' "$hash" >"$pin"
		echo "windows-$a: pinned $hash -> $pin"
	fi

	mkdir -p "resources/windows-$a"
	cp "$src" "$dll"
	echo "windows-$a: wrote $dll"
done
