# Building llama.cpp for AMD ROCm

This document describes the ArtifactFlow-local llama.cpp ROCm setup for Windows. External source checkouts, model files, build products, and smoke logs stay under `ArtifactFlow\` and remain untracked.

## Scope

ArtifactFlow owns local agentic runtime capabilities. Downstream applications such as CodingAgentsGO may use ArtifactFlow-backed packages and configured providers, but they should not manage ArtifactFlow sidecars or local model toolchains directly.

The current validated local setup is:

- GPU: AMD Radeon RX 7800 XT
- ROCm: 7.1
- GPU target: `gfx1101`
- llama.cpp source: `bin\llama.cpp`
- build directory: `bin\llama.cpp\build-gfx1101-vs2022`
- model directory: `models\`
- smoke logs: `logs\`

## Fresh Checkout

Do not reuse stale llama.cpp trees. Move any existing checkout aside before cloning a fresh upstream copy:

```powershell
cd path\to\ArtifactFlow
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
if (Test-Path .\bin\llama.cpp) {
  Rename-Item .\bin\llama.cpp "llama.cpp.old-$stamp"
}
git clone https://github.com/ggml-org/llama.cpp.git .\bin\llama.cpp
```

The `bin\` directory is ignored. Keep the backup, fresh checkout, and build output untracked.

## Verify ROCm Target

Check the installed GPU target before configuring CMake:

```powershell
& "C:\Program Files\AMD\ROCm\7.1\bin\hipInfo.exe"
```

For the RX 7800 XT, `gcnArchName` should report `gfx1101`. Build for the exact target. A neighboring RDNA3 target such as `gfx1100` may compile and list the GPU but fail later with `ROCm error: device kernel image is invalid`.

## Configure

Use ROCm Clang, ROCm `lld-link`, Ninja, and a Visual Studio 2022 developer environment. Avoid the Visual Studio CMake generator and CPU/MSVC builds.

```powershell
cd bin\llama.cpp
$env:CMAKE_PREFIX_PATH = 'C:/Program Files/AMD/ROCm/7.1/lib/cmake;C:/Program Files/AMD/ROCm/7.1'
cmd /s /c "`"C:\Program Files\Microsoft Visual Studio\2022\Community\Common7\Tools\VsDevCmd.bat`" -arch=x64 && cmake -G Ninja -S . -B build-gfx1101-vs2022 -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_COMPILER=`"C:/Program Files/AMD/ROCm/7.1/bin/clang.exe`" -DCMAKE_CXX_COMPILER=`"C:/Program Files/AMD/ROCm/7.1/bin/clang++.exe`" -DCMAKE_LINKER=`"C:/Program Files/AMD/ROCm/7.1/bin/lld-link.exe`" -DGGML_HIP=ON -DGPU_TARGETS=gfx1101"
```

ROCm HIP compilation is noisy and slow. Use `--parallel 1` when diagnosing failures because it keeps compiler output readable.

## Build Targets

Required for OpenAI-compatible local chat:

```powershell
cmd /s /c "`"C:\Program Files\Microsoft Visual Studio\2022\Community\Common7\Tools\VsDevCmd.bat`" -arch=x64 && cmake --build build-gfx1101-vs2022 --target llama-server --parallel 1"
```

Useful smoke and future workflow targets:

```powershell
cmd /s /c "`"C:\Program Files\Microsoft Visual Studio\2022\Community\Common7\Tools\VsDevCmd.bat`" -arch=x64 && cmake --build build-gfx1101-vs2022 --target llama-cli --parallel 1"
cmd /s /c "`"C:\Program Files\Microsoft Visual Studio\2022\Community\Common7\Tools\VsDevCmd.bat`" -arch=x64 && cmake --build build-gfx1101-vs2022 --target llama-completion --parallel 1"
cmd /s /c "`"C:\Program Files\Microsoft Visual Studio\2022\Community\Common7\Tools\VsDevCmd.bat`" -arch=x64 && cmake --build build-gfx1101-vs2022 --target llama-mtmd-cli --parallel 1"
```

## Verify

```powershell
cd path\to\ArtifactFlow
.\bin\llama.cpp\build-gfx1101-vs2022\bin\llama-server.exe --version
.\bin\llama.cpp\build-gfx1101-vs2022\bin\llama-cli.exe --list-devices
```

Expected device output includes:

```text
ROCm0: AMD Radeon RX 7800 XT
```

## Model Layout

Place local GGUF files under `models\`. They are intentionally ignored and should not be committed.

Example:

```text
models\
  Gemma-4-E4B\
    Gemma-4-E4B-Q8_K_P.gguf
    mmproj-Gemma-4-E4B-f16.gguf
```

Do not hardcode model filenames in tracked Go source. Pass them through configuration or environment variables in downstream applications.

## Run llama-server

Start the server from the ArtifactFlow project root, write logs under `logs\`, and pass an absolute model path:

```powershell
cd path\to\ArtifactFlow
$server = Resolve-Path '.\bin\llama.cpp\build-gfx1101-vs2022\bin\llama-server.exe'
$model = Resolve-Path '.\models\Gemma-4-E4B\Gemma-4-E4B-Q8_K_P.gguf'
$outLog = '.\logs\llama-server-smoke.out.log'
$errLog = '.\logs\llama-server-smoke.err.log'
New-Item -ItemType Directory -Force .\logs | Out-Null
Remove-Item -LiteralPath $outLog,$errLog -ErrorAction SilentlyContinue
Start-Process -FilePath $server.Path -ArgumentList @('-m', $model.Path, '--host', '127.0.0.1', '--port', '8081', '--ctx-size', '24576', '--flash-attn', 'auto', '-ngl', '999') -WorkingDirectory (Resolve-Path '.').Path -WindowStyle Hidden -RedirectStandardOutput $outLog -RedirectStandardError $errLog
```

PowerShell requires separate stdout and stderr log files. Do not point both redirects at the same path.

Keep `--ctx-size` explicit. The current E4B-class local reasoning model needs a larger context window because its thinking trace can consume several thousand tokens before it emits tool or handoff output. `24576` is the default local dogfood setting; tune per model if VRAM pressure or model metadata requires it.

## Smoke Test

```powershell
Invoke-RestMethod -Method Get -Uri 'http://127.0.0.1:8081/v1/models'

$body = @{
  model = 'Gemma-4-E4B-Q8_K_P.gguf'
  messages = @(@{ role = 'user'; content = 'Reply with exactly: ready' })
  max_tokens = 8
  temperature = 0
} | ConvertTo-Json -Depth 5

Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:8081/v1/chat/completions' -ContentType 'application/json' -Body $body
```

The validated local smoke returned `ready` from `/v1/chat/completions`.

## CodingAgentsGO Wiring

CodingAgentsGO can target the local llama.cpp server through its OpenAI-compatible provider path:

```powershell
$env:ORCHESTRATOR_MODE = 'cloud'
$env:OPENAI_MODEL = 'Gemma-4-E4B-Q8_K_P.gguf'
$env:OPENAI_URL = 'http://127.0.0.1:8081/v1/chat/completions'
$env:OPENAI_API_KEY = 'local'
```

An explicit `/v1/chat/completions` URL must stay on the OpenAI-compatible provider, even when hosted on `127.0.0.1`.

## Cleanup

Stop local smoke servers when testing is done:

```powershell
Get-Process llama-server -ErrorAction SilentlyContinue | Stop-Process -Force
```

Smoke logs belong under `ArtifactFlow\logs\`; remove them when they are no longer useful.

## Validation

From `ArtifactFlow`:

```powershell
go test ./...
go build ./...
```

The ignored `bin\go.mod` boundary prevents Go from descending into the external llama.cpp checkout during `go test ./...`.
