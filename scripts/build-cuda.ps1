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
if (-not (Get-Command nvcc -ErrorAction SilentlyContinue)) {
    throw '未找到 nvcc，请安装 CUDA Toolkit 并将 bin 目录加入 PATH。'
}
if (-not $CompilerDirectory -and -not (Get-Command cl -ErrorAction SilentlyContinue)) {
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
    if (Test-Path -LiteralPath $vswhere) {
        $installation = & $vswhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
        if ($installation) {
            $compiler = Get-ChildItem -Path (Join-Path $installation 'VC/Tools/MSVC/*/bin/Hostx64/x64/cl.exe') | Sort-Object FullName -Descending | Select-Object -First 1
            if ($compiler) { $CompilerDirectory = $compiler.DirectoryName }
        }
    }
    if (-not $CompilerDirectory) { throw '未找到 MSVC，请安装 Visual Studio C++ 桌面开发工具。' }
}
$nvccArguments = @('-ptx', '-O3', "-arch=$Architecture", '-Xcompiler', '/utf-8')
if ($CompilerDirectory) { $nvccArguments += @('-ccbin', $CompilerDirectory) }
if ($AllowUnsupportedCompiler) { $nvccArguments += '-allow-unsupported-compiler' }
$nvccArguments += @((Join-Path $projectRoot 'cuda/vanity.cu'), '-o', (Join-Path $projectRoot 'cuda/vanity.ptx'))
& nvcc @nvccArguments
if ($LASTEXITCODE -ne 0) { throw 'CUDA 内核编译失败。' }
Write-Host 'CUDA 内核已生成：cuda/vanity.ptx'
