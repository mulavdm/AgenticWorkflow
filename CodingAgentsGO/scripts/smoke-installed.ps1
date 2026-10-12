param(
    [int]$TimeoutSeconds = 30,
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\CodingAgentsGO",
    [string]$BaseURL = "http://127.0.0.1:8080"
)

$ErrorActionPreference = "Stop"

$exePath = Join-Path $InstallDir "main.exe"
$caPath = Join-Path $InstallDir "ca.bat"

if (!(Test-Path $InstallDir)) {
    throw "install directory not found: $InstallDir"
}
foreach ($asset in @("main.exe", "VERSION", "ca.bat", "codingagents.bat", "prompts", "web", "internal")) {
    $assetPath = Join-Path $InstallDir $asset
    if (!(Test-Path $assetPath)) {
        throw "installed asset missing: $assetPath"
    }
}

$resolvedCA = Get-Command ca -ErrorAction SilentlyContinue
if ($resolvedCA -and ((Split-Path -Parent $resolvedCA.Source) -ne $InstallDir)) {
    throw "ca resolves to $($resolvedCA.Source), expected install dir $InstallDir"
}
if (!(Test-Path $caPath)) {
    throw "ca alias missing: $caPath"
}

$doctor = & $exePath doctor --json | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) {
    throw "installed doctor failed with exit code $LASTEXITCODE"
}
if (-not $doctor.ok) {
    throw "installed doctor returned ok=false: $($doctor | ConvertTo-Json -Depth 8)"
}

$previousServerOnly = $env:CODINGAGENTS_SERVER_ONLY
$previousLocalDB = $env:CODINGAGENTS_LOCAL_DB
$env:CODINGAGENTS_SERVER_ONLY = "1"
Remove-Item Env:\CODINGAGENTS_LOCAL_DB -ErrorAction SilentlyContinue

$server = Start-Process -FilePath $exePath -WorkingDirectory $InstallDir -WindowStyle Hidden -PassThru

try {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $health = $null
    $lastProbeError = $null
    do {
        if ($server.HasExited) {
            throw "installed server exited early with code $($server.ExitCode)"
        }
        try {
            $healthResponse = Invoke-WebRequest -Method Get -Uri "$BaseURL/api/health" -UseBasicParsing -TimeoutSec 2
            $health = $healthResponse.Content | ConvertFrom-Json
        } catch {
            $lastProbeError = $_.Exception.Message
            Start-Sleep -Milliseconds 500
        }
    } while ($null -eq $health -and (Get-Date) -lt $deadline)

    if ($null -eq $health) {
        throw "timed out waiting for installed /api/health at $BaseURL. Last probe error: $lastProbeError"
    }
    if (-not $health.ok) {
        throw "installed health endpoint returned ok=false: $($health | ConvertTo-Json -Depth 8)"
    }

    $indexer = (Invoke-WebRequest -Method Get -Uri "$BaseURL/api/indexer/status" -UseBasicParsing -TimeoutSec 5).Content | ConvertFrom-Json
    $policy = (Invoke-WebRequest -Method Get -Uri "$BaseURL/api/tool-policy" -UseBasicParsing -TimeoutSec 5).Content | ConvertFrom-Json
    $dashboard = Invoke-WebRequest -Method Get -Uri "$BaseURL/" -UseBasicParsing -TimeoutSec 5
    if ($dashboard.Content -notmatch "Cognitive Orchestration") {
        throw "installed dashboard response did not contain expected title"
    }

    [PSCustomObject]@{
        ok = $true
        install_dir = $InstallDir
        base_url = $BaseURL
        version = (Get-Content -Path (Join-Path $InstallDir "VERSION") -Raw).Trim()
        pid = $server.Id
        doctor = $doctor
        health = $health
        indexer = $indexer
        policy = $policy
    } | ConvertTo-Json -Depth 10
} finally {
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
        $server.WaitForExit()
    }
    if ($null -eq $previousServerOnly) {
        Remove-Item Env:\CODINGAGENTS_SERVER_ONLY -ErrorAction SilentlyContinue
    } else {
        $env:CODINGAGENTS_SERVER_ONLY = $previousServerOnly
    }
    if ($null -eq $previousLocalDB) {
        Remove-Item Env:\CODINGAGENTS_LOCAL_DB -ErrorAction SilentlyContinue
    } else {
        $env:CODINGAGENTS_LOCAL_DB = $previousLocalDB
    }
}
