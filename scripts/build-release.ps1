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
if (-not $go) {
    throw "go not found; install Go 1.27+ or add it to PATH"
}

$env:GOTOOLCHAIN = "local"
$env:CGO_ENABLED = "0"
$commit = (git rev-parse --short HEAD)
$buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$dist = Join-Path $root "dist"
$stage = Join-Path $dist ".stage"

if (Test-Path $dist) { Remove-Item $dist -Recurse -Force }
New-Item -ItemType Directory -Force $stage | Out-Null

$targets = @(
    @{ os = "windows"; arch = "amd64"; ext = ".exe" },
    @{ os = "windows"; arch = "arm64"; ext = ".exe" },
    @{ os = "linux";   arch = "amd64"; ext = "" },
    @{ os = "linux";   arch = "arm64"; ext = "" },
    @{ os = "darwin";  arch = "amd64"; ext = "" },
    @{ os = "darwin";  arch = "arm64"; ext = "" }
)

$ldflags = "-s -w -X github.com/yinyongshuiyiguoqi/ghaccel/internal/version.Version=$Version " +
           "-X github.com/yinyongshuiyiguoqi/ghaccel/internal/version.Commit=$commit " +
           "-X github.com/yinyongshuiyiguoqi/ghaccel/internal/version.BuildDate=$buildDate"

foreach ($t in $targets) {
    $env:GOOS = $t.os
    $env:GOARCH = $t.arch
    $name = "ghaccel_${Version}_$($t.os)_$($t.arch)"
    $dir = Join-Path $stage $name
    New-Item -ItemType Directory -Force $dir | Out-Null
    $bin = Join-Path $dir ("ghaccel" + $t.ext)

    Write-Host "building $name"
    & $go build -trimpath -ldflags $ldflags -o $bin ./cmd/ghaccel
    if ($LASTEXITCODE -ne 0) { throw "build failed for $name" }

    if ($t.os -eq "windows") {
        $gui = Join-Path $dir "ghaccel-gui.exe"
        & $go build -trimpath -ldflags "$ldflags -H=windowsgui" -o $gui ./cmd/ghaccel-gui
        if ($LASTEXITCODE -ne 0) { throw "gui build failed for $name" }
    }

    Copy-Item -Path README.md, README.en.md, LICENSE -Destination $dir

    if ($t.os -eq "windows") {
        Compress-Archive -Path (Join-Path $dir "*") -DestinationPath (Join-Path $dist "$name.zip") -Force
    } else {
        tar.exe -czf (Join-Path $dist "$name.tar.gz") -C $dir .
    }
    Remove-Item $dir -Recurse -Force
}

Remove-Item $stage -Recurse -Force

$lines = Get-ChildItem $dist -File | Sort-Object Name | ForEach-Object {
    $hash = (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLower()
    "$hash  $($_.Name)"
}
$lines | Set-Content (Join-Path $dist "sha256sums.txt") -Encoding ascii

Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
Write-Host ""
$table = Get-ChildItem $dist -File | Select-Object Name, Length | Format-Table -AutoSize | Out-String
Write-Host $table
