param(
    [int]$TimeoutSeconds = 30,
    [string]$BaseURL = "http://127.0.0.1:8080"
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$smokeDir = Join-Path $repoRoot "scratch\server-smoke"
$goCacheDir = Join-Path $repoRoot ".gocache\server-smoke"
$exePath = Join-Path $smokeDir "codingagents-smoke.exe"
$buildLogPath = Join-Path $smokeDir "build.log"
$stdoutPath = Join-Path $smokeDir "server.stdout.log"
$stderrPath = Join-Path $smokeDir "server.stderr.log"

New-Item -ItemType Directory -Force -Path $smokeDir | Out-Null
New-Item -ItemType Directory -Force -Path $goCacheDir | Out-Null

Push-Location $repoRoot
try {
    $env:GOCACHE = $goCacheDir
    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        go build -o $exePath . > $buildLogPath 2>&1
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed with exit code $LASTEXITCODE. See $buildLogPath"
    }

    $env:CODINGAGENTS_LOCAL_DB = "1"
    $doctor = & $exePath doctor --json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) {
        throw "doctor failed with exit code $LASTEXITCODE"
    }
    if (-not $doctor.ok) {
        throw "doctor failed: $($doctor | ConvertTo-Json -Depth 6)"
    }

    $env:CODINGAGENTS_SERVER_ONLY = "1"
    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $exePath
    $startInfo.WorkingDirectory = $repoRoot
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $server = [System.Diagnostics.Process]::new()
    $server.StartInfo = $startInfo
    $null = $server.Start()

    try {
        $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
        $health = $null
        do {
            if ($server.HasExited) {
                throw "server exited early with code $($server.ExitCode). See $stdoutPath and $stderrPath"
            }
            try {
                $health = Invoke-RestMethod -Method Get -Uri "$BaseURL/api/health" -TimeoutSec 2
            } catch {
                Start-Sleep -Milliseconds 500
            }
        } while ($null -eq $health -and (Get-Date) -lt $deadline)

        if ($null -eq $health) {
            throw "timed out waiting for /api/health"
        }
        if (-not $health.ok) {
            throw "health endpoint returned ok=false: $($health | ConvertTo-Json -Depth 6)"
        }

        $indexer = Invoke-RestMethod -Method Get -Uri "$BaseURL/api/indexer/status" -TimeoutSec 5
        $policy = Invoke-RestMethod -Method Get -Uri "$BaseURL/api/tool-policy" -TimeoutSec 5
        $dashboard = Invoke-WebRequest -Method Get -Uri "$BaseURL/" -UseBasicParsing -TimeoutSec 5
        if ($dashboard.Content -notmatch "Cognitive Orchestration") {
            throw "dashboard asset response did not contain expected title"
        }

        [PSCustomObject]@{
            ok = $true
            base_url = $BaseURL
            pid = $server.Id
            health = $health
            indexer = $indexer
            policy = $policy
            stdout = $stdoutPath
            stderr = $stderrPath
        } | ConvertTo-Json -Depth 8
    } finally {
        if ($server -and -not $server.HasExited) {
            Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
            $server.WaitForExit()
        }
        $server.StandardOutput.ReadToEnd() | Set-Content -Path $stdoutPath
        $server.StandardError.ReadToEnd() | Set-Content -Path $stderrPath
    }
} finally {
    Pop-Location
}
