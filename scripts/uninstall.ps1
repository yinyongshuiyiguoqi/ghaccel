$ErrorActionPreference = "Stop"
$install = Join-Path $env:LOCALAPPDATA "Programs\ghaccel"

$cli = Join-Path $install "ghaccel.exe"
if (Test-Path $cli) {
    try { & $cli stop } catch {}
    Write-Host "if you want to remove the hosts block, run as administrator:"
    Write-Host "  & '$cli' restore"
}

$desktop = [Environment]::GetFolderPath("Desktop")
$startMenu = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
foreach ($dir in @($desktop, $startMenu)) {
    $link = Join-Path $dir "ghaccel.lnk"
    if (Test-Path $link) { Remove-Item $link -Force }
}

if (Test-Path $install) { Remove-Item $install -Recurse -Force }
Write-Host "ghaccel removed"
