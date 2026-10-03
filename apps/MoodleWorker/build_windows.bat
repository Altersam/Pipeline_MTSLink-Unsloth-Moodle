@echo off
setlocal
cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
  echo Go is not installed. Install Go 1.23+ from https://go.dev/dl/
  pause
  exit /b 1
)

if not exist dist mkdir dist

set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0

go build -trimpath -ldflags="-s -w -H=windowsgui" -o dist\MoodleWorker.exe .
if errorlevel 1 (
  echo Build failed.
  pause
  exit /b 1
)

echo.
echo READY: %CD%\dist\MoodleWorker.exe
pause
