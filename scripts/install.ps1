param(
    [string]$SourceDir = "",
    [switch]$Launch
)

$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
if (-not $SourceDir) { $SourceDir = Join-Path $root "bin" }

$gui = Join-Path $SourceDir "ghaccel-gui.exe"
$cli = Join-Path $SourceDir "ghaccel.exe"
if (-not (Test-Path $gui)) { throw "ghaccel-gui.exe not found in $SourceDir" }

$install = Join-Path $env:LOCALAPPDATA "Programs\ghaccel"
New-Item -ItemType Directory -Force $install | Out-Null

Copy-Item $gui $install -Force
if (Test-Path $cli) { Copy-Item $cli $install -Force }
foreach ($doc in @("README.md", "LICENSE")) {
    $p = Join-Path $root $doc
    if (Test-Path $p) { Copy-Item $p $install -Force }
}

$shell = New-Object -ComObject WScript.Shell
$desktop = [Environment]::GetFolderPath("Desktop")
$startMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
$target = Join-Path $install "ghaccel-gui.exe"

foreach ($dir in @($desktop, $startMenu)) {
    $link = $shell.CreateShortcut((Join-Path $dir "ghaccel.lnk"))
    $link.TargetPath = $target
    $link.WorkingDirectory = $install
    $link.IconLocation = "$target,0"
    $link.Description = "ghaccel GitHub 加速器"
    $link.Save()
}

Write-Host "installed: $install"
Write-Host "shortcuts: desktop + start menu"

if ($Launch) {
    Start-Process -FilePath $target
}
