param(
    [int]$TimeoutSeconds = 60,
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\CodingAgentsGO",
    [string]$AppBaseURL = "http://127.0.0.1:8080",
    [int]$ProviderPort = 18081
)

$ErrorActionPreference = "Stop"

$exePath = Join-Path $InstallDir "main.exe"
$workDir = Join-Path $InstallDir "workflow-smoke"
$providerURL = "http://127.0.0.1:$ProviderPort/v1/chat/completions"
$providerPrefix = "http://127.0.0.1:$ProviderPort/"

function Wait-RunStatus {
    param(
        [string]$BaseURL,
        [string]$RunID,
        [string[]]$Statuses,
        [int]$TimeoutSeconds
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $run = $null
    do {
        try {
            $run = (Invoke-WebRequest -UseBasicParsing -Uri "$BaseURL/api/runs/$RunID" -TimeoutSec 5).Content | ConvertFrom-Json
            if ($Statuses -contains $run.status) {
                return $run
            }
        } catch {
            $run = $null
        }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    return $run
}

if (!(Test-Path $exePath)) {
    throw "installed binary not found: $exePath"
}
if ((Test-Path $workDir) -and $workDir.StartsWith($InstallDir)) {
    Remove-Item -LiteralPath $workDir -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $workDir | Out-Null

$providerJob = Start-Job -ScriptBlock {
    param($prefix)
    $listener = [System.Net.HttpListener]::new()
    $listener.Prefixes.Add($prefix)
    $listener.Start()
    try {
        while ($listener.IsListening) {
            $ctx = $listener.GetContext()
            $req = $ctx.Request
            $resp = $ctx.Response
            $stopAfterResponse = $false
            try {
                if ($req.HttpMethod -eq "GET" -and $req.Url.AbsolutePath -eq "/__stop") {
                    $body = '{"ok":true}'
                    $bytes = [Text.Encoding]::UTF8.GetBytes($body)
                    $resp.ContentType = "application/json"
                    $resp.OutputStream.Write($bytes, 0, $bytes.Length)
                    $stopAfterResponse = $true
                } elseif ($req.HttpMethod -eq "GET" -and $req.Url.AbsolutePath -eq "/v1/models") {
                    $body = '{"data":[{"id":"smoke-model"}]}'
                    $bytes = [Text.Encoding]::UTF8.GetBytes($body)
                    $resp.ContentType = "application/json"
                    $resp.OutputStream.Write($bytes, 0, $bytes.Length)
                    continue
                } elseif ($req.HttpMethod -eq "POST" -and $req.Url.AbsolutePath -eq "/v1/embeddings") {
                    $body = '{"data":[{"embedding":[0.01,0.02,0.03,0.04,0.05,0.06,0.07,0.08]}]}'
                    $bytes = [Text.Encoding]::UTF8.GetBytes($body)
                    $resp.ContentType = "application/json"
                    $resp.OutputStream.Write($bytes, 0, $bytes.Length)
                    continue
                } elseif ($req.HttpMethod -eq "POST" -and $req.Url.AbsolutePath -eq "/v1/chat/completions") {
                    $reader = [IO.StreamReader]::new($req.InputStream, $req.ContentEncoding)
                    $requestBody = $reader.ReadToEnd()
                    $content = "[TECHNICAL PLAN]`n1. Smoke workflow completed through the installed API.`n[/TECHNICAL PLAN]`n[NEXT: completed]"
                    if ($requestBody -match "installed approval smoke") {
                        $content = '[TOOL: writer, ARGS: {"action":"write","path":"README.md","content":"smoke"}]'
                    }
                    $payload = @{ choices = @(@{ delta = @{ content = $content } }) } | ConvertTo-Json -Compress -Depth 8
                    $usage = '{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}'
                    $body = "data: $payload`n`ndata: $usage`n`ndata: [DONE]`n`n"
                    $bytes = [Text.Encoding]::UTF8.GetBytes($body)
                    $resp.ContentType = "text/event-stream"
                    $resp.OutputStream.Write($bytes, 0, $bytes.Length)
                    continue
                } else {
                    $resp.StatusCode = 404
                }
            } finally {
                $resp.OutputStream.Close()
            }
            if ($stopAfterResponse) {
                break
            }
        }
    } finally {
        $listener.Stop()
        $listener.Close()
    }
} -ArgumentList $providerPrefix

$previous = @{
    CODINGAGENTS_SERVER_ONLY = $env:CODINGAGENTS_SERVER_ONLY
    CODINGAGENTS_LOCAL_DB = $env:CODINGAGENTS_LOCAL_DB
    ORCHESTRATOR_MODE = $env:ORCHESTRATOR_MODE
    OPENAI_URL = $env:OPENAI_URL
    OPENAI_MODEL = $env:OPENAI_MODEL
    OPENAI_API_KEY = $env:OPENAI_API_KEY
    LOCAL_OLLAMA_URL = $env:LOCAL_OLLAMA_URL
    LMSTUDIO_URL = $env:LMSTUDIO_URL
}

$server = $null
try {
    Start-Sleep -Milliseconds 300
    $probe = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$ProviderPort/v1/models" -TimeoutSec 5
    if ($probe.StatusCode -ne 200) {
        throw "mock provider did not start"
    }

    $env:CODINGAGENTS_SERVER_ONLY = "1"
    $env:CODINGAGENTS_LOCAL_DB = "1"
    $env:ORCHESTRATOR_MODE = "cloud"
    $env:OPENAI_URL = $providerURL
    $env:OPENAI_MODEL = "smoke-model"
    $env:OPENAI_API_KEY = "smoke"
    $env:LOCAL_OLLAMA_URL = "http://127.0.0.1:9"
    $env:LMSTUDIO_URL = "http://127.0.0.1:9"

    $server = Start-Process -FilePath $exePath -WorkingDirectory $workDir -WindowStyle Hidden -PassThru

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $health = $null
    do {
        if ($server.HasExited) {
            throw "installed workflow server exited early with code $($server.ExitCode)"
        }
        try {
            $health = (Invoke-WebRequest -UseBasicParsing -Uri "$AppBaseURL/api/health" -TimeoutSec 2).Content | ConvertFrom-Json
        } catch {
            Start-Sleep -Milliseconds 500
        }
    } while ($null -eq $health -and (Get-Date) -lt $deadline)
    if ($null -eq $health -or -not $health.ok) {
        throw "installed workflow server health did not become ready"
    }

    $probeBody = @{ url = $providerURL } | ConvertTo-Json
    $providerProbe = (Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$AppBaseURL/api/providers/probe" -ContentType "application/json" -Body $probeBody -TimeoutSec 5).Content | ConvertFrom-Json
    if (-not $providerProbe.ok) {
        throw "provider probe failed: $($providerProbe | ConvertTo-Json -Depth 8)"
    }

    $plannerPayload = @{ prompt = "@cloud:smoke-model @planner installed workflow smoke"; role = "planner" } | ConvertTo-Json
    $plannerStart = (Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$AppBaseURL/api/execute" -ContentType "application/json" -Body $plannerPayload -TimeoutSec 5).Content | ConvertFrom-Json
    $plannerRun = Wait-RunStatus -BaseURL $AppBaseURL -RunID $plannerStart.run_id -Statuses @("completed", "failed", "cancelled") -TimeoutSeconds $TimeoutSeconds
    if ($plannerRun.status -ne "completed") {
        throw "planner smoke run did not complete: $($plannerRun | ConvertTo-Json -Depth 8)"
    }

    $coderPayload = @{ prompt = "@cloud:smoke-model @coder installed approval smoke"; role = "coder" } | ConvertTo-Json
    $coderStart = (Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$AppBaseURL/api/execute" -ContentType "application/json" -Body $coderPayload -TimeoutSec 5).Content | ConvertFrom-Json
    $coderRun = Wait-RunStatus -BaseURL $AppBaseURL -RunID $coderStart.run_id -Statuses @("paused", "failed", "completed") -TimeoutSeconds $TimeoutSeconds
    if ($coderRun.status -ne "paused") {
        throw "coder smoke run did not pause for approval: $($coderRun | ConvertTo-Json -Depth 8)"
    }
    $approvals = (Invoke-WebRequest -UseBasicParsing -Uri "$AppBaseURL/api/runs/$($coderStart.run_id)/approvals" -TimeoutSec 5).Content | ConvertFrom-Json
    if ($approvals.Count -lt 1 -or $approvals[0].status -ne "pending") {
        throw "coder smoke run did not expose pending approval"
    }
    $decisionBody = @{ decision = "denied"; reason = "installed smoke denial" } | ConvertTo-Json
    Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$AppBaseURL/api/runs/$($coderStart.run_id)/approvals/$($approvals[0].id)" -ContentType "application/json" -Body $decisionBody -TimeoutSec 5 | Out-Null
    $deniedRun = Wait-RunStatus -BaseURL $AppBaseURL -RunID $coderStart.run_id -Statuses @("denied", "failed", "completed") -TimeoutSeconds $TimeoutSeconds
    if ($deniedRun.status -ne "denied") {
        throw "coder smoke run was not denied after approval decision: $($deniedRun | ConvertTo-Json -Depth 8)"
    }
    $decidedApprovals = (Invoke-WebRequest -UseBasicParsing -Uri "$AppBaseURL/api/runs/$($coderStart.run_id)/approvals" -TimeoutSec 5).Content | ConvertFrom-Json

    [PSCustomObject]@{
        ok = $true
        install_dir = $InstallDir
        work_dir = $workDir
        app_base_url = $AppBaseURL
        provider_url = $providerURL
        provider_probe = $providerProbe
        planner_run = $plannerRun
        approval_run = $deniedRun
        approval = $approvals[0]
        approval_after_decision = $decidedApprovals[0]
    } | ConvertTo-Json -Depth 10
} finally {
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
        $server.WaitForExit(5000) | Out-Null
    }
    if ($providerJob) {
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$ProviderPort/__stop" -TimeoutSec 2 | Out-Null
            Wait-Job $providerJob -Timeout 5 | Out-Null
        } catch {
        }
        if ($providerJob.State -eq "Running") {
            Stop-Job $providerJob -ErrorAction SilentlyContinue
        }
        Remove-Job $providerJob -Force -ErrorAction SilentlyContinue
    }
    foreach ($key in $previous.Keys) {
        if ($null -eq $previous[$key]) {
            Remove-Item "Env:\$key" -ErrorAction SilentlyContinue
        } else {
            Set-Item "Env:\$key" $previous[$key]
        }
    }
}
