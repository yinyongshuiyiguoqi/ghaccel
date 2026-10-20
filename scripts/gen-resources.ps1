param(
    [string]$Version = "0.1.0"
)

$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root

$go = Join-Path $env:LOCALAPPDATA "Programs\go\bin\go.exe"
if (-not (Test-Path $go)) {
    $go = (Get-Command go -ErrorAction SilentlyContinue).Source
}
if (-not $go) { throw "go not found" }

$env:GOTOOLCHAIN = "local"
$env:GOPROXY = "https://goproxy.cn,direct"

Push-Location cmd\ghaccel-gui
& $go run github.com/tc-hib/go-winres@latest simply `
    --arch amd64,arm64 --out rsrc `
    --product-version $Version --file-version $Version `
    --manifest gui --admin `
    --file-description "ghaccel GitHub accelerator desktop" `
    --product-name "ghaccel Desktop" `
    --copyright "Copyright (c) 2026 ghaccel contributors" `
    --original-filename ghaccel-gui.exe `
    --icon ..\..\assets\icon.ico
Pop-Location

Write-Host "resources regenerated for version $Version"
