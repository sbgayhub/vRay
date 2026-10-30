# 下载目标架构的 wintun.dll，并固定其哈希。
#
# Windows 上 Xray 的 TUN 入站会从可执行文件旁加载 wintun.dll。该
# DLL 因架构而异，必须与内嵌核心的目标架构一致，因此不能一次
# 提交后就不再处理：为其他架构构建时需要各自的副本，而过时的
# 文件会导致启动失败，让人误以为缺少某项功能。
# 因此，每个目标架构的副本都必须单独维护。
#
# 关于哈希：此脚本不包含手写的 SHA-256 值。凭记忆输入的哈希
# 比没有更糟，因为它看似在验证，实则纯属形式。首次下载会将
# 实际观察到的哈希写入 resources/windows-<arch>/wintun.sha256；
# 该文件会提交到仓库，后续每次下载和发布构建都会以它为准进行
# 校验。因此，更改 DLL 时必须同步更改固定值并接受审核，这正是
# 该流程所需要的审核。
#
# 用法：
#   scripts/fetch-wintun.ps1                 # amd64 + arm64
#   scripts/fetch-wintun.ps1 -Arch amd64
#   scripts/fetch-wintun.ps1 -Verify         # 仅检查目录内容，不访问网络
#   scripts/fetch-wintun.ps1 -Force          # 即使固定哈希匹配也重新下载
#
# 输出：resources/windows-<arch>/wintun.dll + wintun.sha256。

[CmdletBinding()]
param(
    [ValidateSet('amd64', 'arm64', '386', 'all')]
    [string]$Arch = 'all',
    [switch]$Verify,
    [switch]$Force
)

$ErrorActionPreference = 'Stop'

# 固定发布版本，不使用“latest”：会变动的 URL 意味着下一次构建
# 可能发布与已审核版本不同的 DLL。
$WintunVersion = '0.14.1'
$WintunUrl = "https://www.wintun.net/builds/wintun-$WintunVersion.zip"

$root = Split-Path -Parent $PSScriptRoot
$targets = if ($Arch -eq 'all') { @('amd64', 'arm64') } else { @($Arch) }

# 压缩包按 wintun/bin/<name>/wintun.dll 的路径存放 DLL，其中 32 位
# 目录名为 x86，而 Go 称该架构为 386。
function Get-ArchiveDir([string]$a) {
    if ($a -eq '386') { return 'x86' }
    return $a
}

function Get-DllPath([string]$a) { Join-Path $root "resources\windows-$a\wintun.dll" }
function Get-PinPath([string]$a) { Join-Path $root "resources\windows-$a\wintun.sha256" }
function Get-Sha256([string]$path) {
    (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
}

if ($Verify) {
    # Verify 只读取目录中现有的文件，因此发布构建无需访问网络即可
    # 证明所使用的 DLL 是已审核版本。
    $bad = @()
    foreach ($a in $targets) {
        $dll = Get-DllPath $a
        $pin = Get-PinPath $a
        if (-not (Test-Path -LiteralPath $dll)) {
            $bad += "  windows-$a`: wintun.dll missing ($dll)"
            continue
        }
        if (-not (Test-Path -LiteralPath $pin)) {
            $bad += "  windows-$a`: wintun.sha256 missing, so the DLL is unverified ($pin)"
            continue
        }
        $want = (Get-Content -LiteralPath $pin -Raw).Trim().ToLowerInvariant()
        $got = Get-Sha256 $dll
        if ($got -ne $want) {
            $bad += "  windows-$a`: hash mismatch (got $got, pinned $want)"
        }
        else {
            'windows-{0}: ok ({1})' -f $a, $got
        }
    }
    if ($bad.Count -gt 0) {
        [Console]::Error.WriteLine('wintun.dll is not ready for release:')
        $bad | ForEach-Object { [Console]::Error.WriteLine($_) }
        [Console]::Error.WriteLine('run scripts/fetch-wintun.ps1 to fetch it')
        exit 1
    }
    'all wintun.dll files verified'
    exit 0
}

$archive = Join-Path ([System.IO.Path]::GetTempPath()) "wintun-$WintunVersion-$(New-Guid).zip"
$extract = Join-Path ([System.IO.Path]::GetTempPath()) ('wintun-extract-' + [guid]::NewGuid().ToString('N'))

try {
    $downloaded = $false
    foreach ($a in $targets) {
        $dll = Get-DllPath $a
        $pin = Get-PinPath $a

        if (-not $Force -and (Test-Path -LiteralPath $dll) -and (Test-Path -LiteralPath $pin)) {
            $want = (Get-Content -LiteralPath $pin -Raw).Trim().ToLowerInvariant()
            if ((Get-Sha256 $dll) -eq $want) {
                'windows-{0}: already present and matches the pin' -f $a
                continue
            }
            'windows-{0}: the DLL does not match the pin; refetching' -f $a
        }

        if (-not $downloaded) {
            "downloading wintun $WintunVersion from $WintunUrl"
            Invoke-WebRequest -Uri $WintunUrl -OutFile $archive -UseBasicParsing
            Expand-Archive -LiteralPath $archive -DestinationPath $extract -Force
            $downloaded = $true
        }

        $from = Join-Path $extract "wintun\bin\$(Get-ArchiveDir $a)\wintun.dll"
        if (-not (Test-Path -LiteralPath $from)) {
            throw "the archive has no wintun/bin/$(Get-ArchiveDir $a)/wintun.dll"
        }

        $hash = Get-Sha256 $from
        if (Test-Path -LiteralPath $pin) {
            # 已存在的固定哈希与下载内容不一致是真正的问题：要么固定值
            # 已过时，要么下载内容并非 Wintun。
            $want = (Get-Content -LiteralPath $pin -Raw).Trim().ToLowerInvariant()
            if ($hash -ne $want) {
                throw ('windows-{0}: downloaded DLL hash {1} does not match the committed pin {2}. If the pin is genuinely outdated, delete {3} and re-run, then review the new hash.' -f $a, $hash, $want, $pin)
            }
            'windows-{0}: matches the existing pin' -f $a
        }
        else {
            New-Item -ItemType Directory -Force -Path (Split-Path -Parent $pin) | Out-Null
            Set-Content -LiteralPath $pin -Value $hash -NoNewline -Encoding ascii
            'windows-{0}: pinned {1} -> {2}' -f $a, $hash, $pin
        }

        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $dll) | Out-Null
        Copy-Item -LiteralPath $from -Destination $dll -Force
        'windows-{0}: wrote {1}' -f $a, $dll
    }
}
finally {
    Remove-Item -Force -LiteralPath $archive -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force -LiteralPath $extract -ErrorAction SilentlyContinue
}
