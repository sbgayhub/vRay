# 二进制文件超出约定预算时使构建失败。
#
# vRay 在进程中内嵌完整的 Xray 核心，这一决定有代价：
# 可执行文件有数十兆字节，而不是几兆。设置这道门槛才能让
# 这项代价保持可知。否则每次改动都会让体积增加几兆，
# 直到达到承诺值的两倍才有人察觉。
#
# 这是 scripts/check-size.sh 的 PowerShell 对应脚本，供没有
# POSIX shell 的 Windows 构建机使用。两者检查相同的两项预算，
# 因此不会对构建是否通过产生分歧。
#
# 用法：
#   scripts/check-size.ps1                # 检查
#   scripts/check-size.ps1 -Report        # 打印数值，始终不失败
#   $env:VRAY_SIZE_BUDGET_MB = 45; scripts/check-size.ps1

[CmdletBinding()]
param(
    [switch]$Report,
    [int]$UntrimmedBudgetMB = 56,
    [int]$TrimmedBudgetMB = 40
)

$ErrorActionPreference = 'Stop'

if ($env:VRAY_SIZE_BUDGET_MB) { $UntrimmedBudgetMB = [int]$env:VRAY_SIZE_BUDGET_MB }
if ($env:VRAY_SIZE_TRIMMED_MB) { $TrimmedBudgetMB = [int]$env:VRAY_SIZE_TRIMMED_MB }

$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("vray-size-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    # CGO 保持关闭：二进制文件必须可移植，且构建过程必须能在
    # 没有 C 工具链的环境中运行。
    $env:CGO_ENABLED = '0'
    $plain = Join-Path $tmp 'vray.exe'
    $trimmed = Join-Path $tmp 'vray-trimmed.exe'

    & go build -o $plain .
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
    & go build -ldflags='-s -w' -o $trimmed .
    if ($LASTEXITCODE -ne 0) { throw 'trimmed go build failed' }

    $fail = $false
    function Check([string]$label, [string]$path, [int]$budget) {
        $bytes = (Get-Item -LiteralPath $path).Length
        # 整数 MiB，向下取整：与用户在资源管理器中看到的数值一致。
        $mib = [math]::Floor($bytes / 1MB)
        '{0,-14} {1,6} MiB  (budget {2} MiB)' -f $label, $mib, $budget
        if ($mib -gt $budget) {
            if ($Report) {
                '  over budget by {0} MiB (report mode)' -f ($mib - $budget)
            }
            else {
                [Console]::Error.WriteLine('  OVER BUDGET by {0} MiB' -f ($mib - $budget))
                $script:fail = $true
            }
        }
    }

    Check 'untrimmed' $plain $UntrimmedBudgetMB
    Check 'trimmed' $trimmed $TrimmedBudgetMB

    if ($fail) {
        [Console]::Error.WriteLine(@'

The binary grew past its budget. Before raising the number, check which of
these happened:

  - a new dependency was added (go.mod) that pulls a large package in;
  - a whole test-only or offline helper package got linked into main;
  - the Xray core version changed and upstream grew.

Raising the budget is a product decision, recorded in
docs/03-implementation-plan.md, not a build fix.
'@)
        exit 1
    }

    if ($Report) { 'sizes are within budget (report mode)' } else { 'sizes are within budget' }
}
finally {
    Pop-Location
    Remove-Item -Recurse -Force -LiteralPath $tmp -ErrorAction SilentlyContinue
}

