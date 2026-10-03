# MoodleWorker v0.5

Standalone Windows orchestration layer for Unsloth → Moodle.

## Build

Requires Go 1.23+.

```powershell
$env:GOOS="windows"
$env:GOARCH="amd64"
$env:CGO_ENABLED="0"

go build -trimpath -ldflags="-s -w -H=windowsgui" -o dist/MoodleWorker.exe .
```

No third-party Go modules are required.

## Embedded resources

```text
resources/ui.html
resources/moodle-lesson-builder.md
resources/moodle-batch-run-prompt.md
resources/moodleworker.ico
```

## Runtime config

Saved locally in:

```text
%APPDATA%\MoodleWorker\settings.json
```

Do not commit this file: it may contain Studio credentials.

## WebUI

```text
http://127.0.0.1:8765
```

## Current published binary checksum

```text
SHA256 66cb2e2c9b0cf551d9ef94878f66adc51813fc10c14f87321d11d7d405a13ebf
```
