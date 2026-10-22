# Builds the Linux backend and packs everything the VM needs into one file, so a deploy
# needs no gcloud on this machine: run this, press "Upload file" in the Console's SSH
# window, then run install.sh on the VM (backend/README.md, Deploying).
#
#   .\tools\deploy\bundle.ps1            # binary + emoji + install.sh
#   .\tools\deploy\bundle.ps1 -Demo      # also data\demo.db (build it first with fairshare-demo)
#
# Output: dist\fairshare-bundle.tar.gz
param([switch]$Demo)

$ErrorActionPreference = "Stop"
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
Set-Location $root
New-Item -ItemType Directory -Force dist | Out-Null
$stage = Join-Path $env:TEMP "fairshare-bundle"
if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
New-Item -ItemType Directory -Force (Join-Path $stage "emoji") | Out-Null

Write-Host "building linux/amd64 binary"
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
try {
    go build -trimpath -o (Join-Path $stage "fairshare") ./backend/cmd/fairshare
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}

Copy-Item backend\assets\emoji\*.png (Join-Path $stage "emoji")
Copy-Item tools\deploy\install.sh, tools\deploy\update.sh $stage
if ($Demo) {
    if (-not (Test-Path data\demo.db)) { throw "data\demo.db not found: go run ./backend/cmd/fairshare-demo -db data/demo.db" }
    Copy-Item data\demo.db $stage
}

$out = Join-Path $root "dist\fairshare-bundle.tar.gz"
if (Test-Path $out) { Remove-Item $out }
# Use the bsdtar that ships with Windows 10+. Git for Windows puts a GNU tar first on
# PATH that reads "C:" in a path as a remote host and fails. The VM unpacks with GNU tar.
$tar = Join-Path $env:SystemRoot "System32\tar.exe"
if (-not (Test-Path $tar)) { $tar = "tar" }
# Name the entries instead of ".", so the archive has no entry for the directory itself:
# extracting "." into a shared directory makes tar try to chmod it and fail as a normal user.
$items = Get-ChildItem $stage | ForEach-Object { $_.Name }
& $tar -czf $out -C $stage @items
if ($LASTEXITCODE -ne 0) { throw "tar failed" }
Remove-Item -Recurse -Force $stage
$size = [math]::Round((Get-Item $out).Length / 1MB, 1)
Write-Host "wrote $out ($size MB)"
Write-Host "next: Console -> VM -> SSH -> Upload file -> fairshare-bundle.tar.gz, then on the VM:"
Write-Host "  mkdir -p ~/bundle && tar -xzf ~/fairshare-bundle.tar.gz -C ~/bundle && sudo bash ~/bundle/install.sh && rm -r ~/bundle ~/fairshare-bundle.tar.gz"
