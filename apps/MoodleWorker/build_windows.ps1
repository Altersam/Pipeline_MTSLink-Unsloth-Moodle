$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go 1.23+ is not installed. Install from https://go.dev/dl/"
}

New-Item -ItemType Directory -Force -Path ".\dist" | Out-Null

$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

go build -trimpath -ldflags="-s -w -H=windowsgui" -o ".\dist\MoodleWorker.exe" .

Write-Host "READY: $PWD\dist\MoodleWorker.exe" -ForegroundColor Green
