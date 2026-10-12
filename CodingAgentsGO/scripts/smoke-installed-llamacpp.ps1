param(
    [int]$TimeoutSeconds = 240,
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\CodingAgentsGO",
    [string]$AppBaseURL = "http://127.0.0.1:8080",
    [string]$ProviderURL = "",
    [string]$Model = "",
    [switch]$RequireConfiguredEndpointReady
)

$ErrorActionPreference = "Stop"

$exePath = Join-Path $InstallDir "main.exe"
$workDir = Join-Path $InstallDir "llamacpp-smoke"
$envPath = Join-Path $InstallDir ".env"

function Read-DotEnv {
    param([string]$Path)
    $values = @{}
    if (!(Test-Path $Path)) {
        return $values
    }
    foreach ($line in Get-Content -LiteralPath $Path) {
        $trimmed = $line.Trim()
        if ($trimmed -eq "" -or $trimmed.StartsWith("#") -or $trimmed -notmatch "=") {
            continue
        }
        $parts = $trimmed.Split("=", 2)
        $values[$parts[0].Trim()] = $parts[1].Trim().Trim('"').Trim("'")
    }
    return $values
}

function Normalize-OpenAICompatibleURL {
    param([string]$URL)
    $u = $URL.Trim().TrimEnd("/")
    if ($u.EndsWith("/v1")) {
        return "$u/chat/completions"
    }
    if ($u.EndsWith("/v1/chat/completions")) {
        return $u
    }
    if ($u -match "/v1/chat/completions$") {
        return $u
    }
    throw "llama.cpp smoke requires an OpenAI-compatible endpoint ending in /v1/chat/completions or /v1: $URL"
}

function Assert-LocalURL {
    param([string]$URL)
    $uri = [Uri]$URL
    $hostName = $uri.Host.ToLowerInvariant()
    if ($hostName -ne "127.0.0.1" -and $hostName -ne "localhost" -and $hostName -ne "::1") {
        throw "llama.cpp smoke only accepts local endpoints, got: $URL"
    }
}

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
        Start-Sleep -Seconds 1
    } while ((Get-Date) -lt $deadline)
    return $run
}

if (!(Test-Path $exePath)) {
    throw "installed binary not found: $exePath"
}

$envValues = Read-DotEnv -Path $envPath
if ($ProviderURL -eq "") {
    $ProviderURL = $env:OPENAI_URL
    if ($ProviderURL -eq "" -and $envValues.ContainsKey("OPENAI_URL")) {
        $ProviderURL = $envValues["OPENAI_URL"]
    }
}
if ($Model -eq "") {
    $Model = $env:OPENAI_MODEL
    if ($Model -eq "" -and $envValues.ContainsKey("OPENAI_MODEL")) {
        $Model = $envValues["OPENAI_MODEL"]
    }
}
if ($ProviderURL -eq "") {
    throw "ProviderURL was not supplied and OPENAI_URL was not found in process env or installed .env"
}
if ($Model -eq "") {
    throw "Model was not supplied and OPENAI_MODEL was not found in process env or installed .env"
}
if ($Model.ToLowerInvariant() -notlike "*.gguf") {
    throw "llama.cpp smoke requires a GGUF model id, got: $Model"
}

$providerURL = Normalize-OpenAICompatibleURL -URL $ProviderURL
Assert-LocalURL -URL $providerURL

if ((Test-Path $workDir) -and ([IO.Path]::GetFullPath($workDir).StartsWith([IO.Path]::GetFullPath($InstallDir), [StringComparison]::OrdinalIgnoreCase))) {
    Remove-Item -LiteralPath $workDir -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $workDir | Out-Null

$previous = @{
    CODINGAGENTS_SERVER_ONLY = $env:CODINGAGENTS_SERVER_ONLY
    CODINGAGENTS_LOCAL_DB = $env:CODINGAGENTS_LOCAL_DB
    CODINGAGENTS_DISABLE_PROVIDER_FALLBACKS = $env:CODINGAGENTS_DISABLE_PROVIDER_FALLBACKS
    ORCHESTRATOR_MODE = $env:ORCHESTRATOR_MODE
    OPENAI_URL = $env:OPENAI_URL
    OPENAI_MODEL = $env:OPENAI_MODEL
    OPENAI_API_KEY = $env:OPENAI_API_KEY
    LOCAL_OLLAMA_URL = $env:LOCAL_OLLAMA_URL
    LMSTUDIO_URL = $env:LMSTUDIO_URL
}

$server = $null
try {
    $env:CODINGAGENTS_SERVER_ONLY = "1"
    $env:CODINGAGENTS_LOCAL_DB = "1"
    $env:CODINGAGENTS_DISABLE_PROVIDER_FALLBACKS = "1"
    $env:ORCHESTRATOR_MODE = "cloud"
    $env:OPENAI_URL = $providerURL
    $env:OPENAI_MODEL = $Model
    if ($env:OPENAI_API_KEY -eq "") {
        $env:OPENAI_API_KEY = "local"
    }
    $env:LOCAL_OLLAMA_URL = "http://127.0.0.1:9"
    $env:LMSTUDIO_URL = "http://127.0.0.1:9"

    $server = Start-Process -FilePath $exePath -WorkingDirectory $workDir -WindowStyle Hidden -PassThru

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $health = $null
    do {
        if ($server.HasExited) {
            throw "installed llama.cpp smoke server exited early with code $($server.ExitCode)"
        }
        try {
            $health = (Invoke-WebRequest -UseBasicParsing -Uri "$AppBaseURL/api/health" -TimeoutSec 2).Content | ConvertFrom-Json
        } catch {
            Start-Sleep -Milliseconds 500
        }
    } while ($null -eq $health -and (Get-Date) -lt $deadline)
    if ($null -eq $health -or -not $health.ok) {
        throw "installed llama.cpp smoke server health did not become ready"
    }

    $providerProbe = $null
    try {
        $probeBody = @{ url = $providerURL } | ConvertTo-Json
        $providerProbe = (Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$AppBaseURL/api/providers/probe" -ContentType "application/json" -Body $probeBody -TimeoutSec 10).Content | ConvertFrom-Json
    } catch {
        if ($RequireConfiguredEndpointReady) {
            throw "configured llama.cpp endpoint probe failed: $($_.Exception.Message)"
        }
        $providerProbe = [PSCustomObject]@{
            ok = $false
            provider = "openai-compatible"
            url = $providerURL
            error = $_.Exception.Message
            note = "Probe is advisory because ArtifactFlow may allocate the llama.cpp port during the first GGUF run."
        }
    }
    if ($RequireConfiguredEndpointReady -and -not $providerProbe.ok) {
        throw "configured llama.cpp endpoint probe failed: $($providerProbe | ConvertTo-Json -Depth 8)"
    }
    if (-not $RequireConfiguredEndpointReady -and -not $providerProbe.ok) {
        $providerProbe | Add-Member -NotePropertyName note -NotePropertyValue "Probe is advisory because ArtifactFlow may allocate the llama.cpp port during the first GGUF run." -Force
    }

    $prompt = "@llamacpp:$Model @planner local llama.cpp live smoke. Reply with a concise technical plan and end with [NEXT: completed]. Do not call tools."
    $payload = @{ prompt = $prompt; role = "planner" } | ConvertTo-Json
    $start = (Invoke-WebRequest -UseBasicParsing -Method Post -Uri "$AppBaseURL/api/execute" -ContentType "application/json" -Body $payload -TimeoutSec 10).Content | ConvertFrom-Json
    $run = Wait-RunStatus -BaseURL $AppBaseURL -RunID $start.run_id -Statuses @("completed", "failed", "cancelled", "paused") -TimeoutSeconds $TimeoutSeconds
    if ($null -eq $run) {
        throw "llama.cpp smoke run timed out before reaching a terminal state"
    }
    if ($run.status -ne "completed") {
        $messages = $null
        $events = $null
        try {
            $messages = (Invoke-WebRequest -UseBasicParsing -Uri "$AppBaseURL/api/runs/$($start.run_id)/messages" -TimeoutSec 5).Content | ConvertFrom-Json
            $events = (Invoke-WebRequest -UseBasicParsing -Uri "$AppBaseURL/api/runs/$($start.run_id)/events" -TimeoutSec 5).Content | ConvertFrom-Json
        } catch {
        }
        throw "llama.cpp smoke run did not complete: $($run | ConvertTo-Json -Depth 8); messages=$($messages | ConvertTo-Json -Depth 6); events=$($events | ConvertTo-Json -Depth 6)"
    }

    [PSCustomObject]@{
        ok = $true
        install_dir = $InstallDir
        work_dir = $workDir
        app_base_url = $AppBaseURL
        provider_url = $providerURL
        model = $Model
        fallbacks_disabled = $true
        provider_probe = $providerProbe
        run = $run
    } | ConvertTo-Json -Depth 10
} finally {
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
        $server.WaitForExit(5000) | Out-Null
    }
    foreach ($key in $previous.Keys) {
        if ($null -eq $previous[$key]) {
            Remove-Item "Env:\$key" -ErrorAction SilentlyContinue
        } else {
            Set-Item "Env:\$key" $previous[$key]
        }
    }
}
