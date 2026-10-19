#Requires -Version 7.0
param(
    [string]$Architecture = 'compute_86',
    [string]$CompilerDirectory,
    [switch]$AllowUnsupportedCompiler
)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$PSDefaultParameterValues['*:Encoding'] = 'utf8'
$projectRoot = Split-Path -Parent $PSScriptRoot
& (Join-Path $PSScriptRoot 'build-cuda.ps1') @PSBoundParameters
$previousCGO = $env:CGO_ENABLED
Push-Location $projectRoot
try {
    # CUDA 直接调用系统驱动，关闭 CGO，避免引入外部 C/C++ 链接器与运行库。
    $env:CGO_ENABLED = '0'
    & go build -o go-vanity-eth.exe .
    if ($LASTEXITCODE -ne 0) { throw 'Go 程序构建失败。' }
} finally {
    $env:CGO_ENABLED = $previousCGO
    Pop-Location
}
Write-Host '构建完成。双击 run-cuda.cmd 使用显卡搜索，或在终端中运行 EXE。'
