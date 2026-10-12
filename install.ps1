# install.ps1 - CodingAgentsGO Native Installer & Code-Signer
# Enforces a clean, native installation to bypass WDAC/AppLocker binary execution blocks.

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
$installDir = "$env:LOCALAPPDATA\Programs\CodingAgentsGO"
$dbDir = "$env:APPDATA\CodingAgentsGO"

# Resolve directories relative to script location
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
if (!$scriptRoot) { $scriptRoot = Get-Location }
$srcDir = Join-Path $scriptRoot "CodingAgentsGO"

Write-Host "==================================================" -ForegroundColor Cyan
Write-Host "  CodingAgentsGO Native Installer for Windows" -ForegroundColor Cyan
Write-Host "==================================================" -ForegroundColor Cyan
Write-Host "Installation Path: $installDir" -ForegroundColor Gray
Write-Host "Database Path:     $dbDir" -ForegroundColor Gray
Write-Host "Source Path:       $srcDir" -ForegroundColor Gray
Write-Host "==================================================" -ForegroundColor Cyan

if (!(Test-Path $srcDir)) {
    Write-Error "Source directory not found at $srcDir. Please run the installer from the workspace root."
    exit 1
}

$buildVersion = (Get-Date).ToUniversalTime().ToString("yyyyMMddHHmmss")

# 1. Compile the Go Binary
Write-Host "[1/6] Compiling CodingAgentsGO production binary..." -ForegroundColor Cyan
Push-Location $srcDir
# Compile with optimizations and no console window popups if run as background task
& go build -ldflags="-s -w -X main.buildVersion=$buildVersion" -o main.exe
$compileExitCode = $LASTEXITCODE
Pop-Location

if ($compileExitCode -ne 0) {
    Write-Error "Go compilation failed. Aborting installation."
    exit 1
}
Write-Host "Compilation successful." -ForegroundColor Green

# 2. Code-Signing (Skipped for SmartAppControl Compatibility)
Write-Host "[2/6] Setting up binary security..." -ForegroundColor Cyan
Write-Host "Code-signing skipped. SmartAppControl blocks self-signed certificates. Unsigned binaries will run via reputation." -ForegroundColor Gray

# 3. Deploy Assets
Write-Host "[3/6] Deploying application assets to AppData..." -ForegroundColor Cyan

# Stop existing running processes to unlock binary files
Write-Host "Stopping any running instances to unlock files..." -ForegroundColor Gray
Stop-Process -Name "main" -ErrorAction SilentlyContinue
Stop-Process -Name "cognitive-orchestrator" -ErrorAction SilentlyContinue
Stop-Process -Name "codingagents-dogfood" -ErrorAction SilentlyContinue
Stop-Process -Name "llama-server" -ErrorAction SilentlyContinue
Stop-Process -Name "rust-sidecar" -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

if (!(Test-Path $installDir)) {
    New-Item -ItemType Directory -Path $installDir -Force | Out-Null
}

# Copy binary
$binaryPath = Join-Path $srcDir "main.exe"
Copy-Item -Path $binaryPath -Destination $installDir -Force
[System.IO.File]::WriteAllText((Join-Path $installDir "VERSION"), $buildVersion)

# Copy rust-sidecar binary if it exists
$rustSidecarPath = Join-Path $scriptRoot "bin\rust-sidecar.exe"
if (Test-Path $rustSidecarPath) {
    Write-Host "Copying Rust sidecar binary..." -ForegroundColor Gray
    Copy-Item -Path $rustSidecarPath -Destination $installDir -Force
} else {
    Write-Warning "rust-sidecar.exe not found in bin/ directory! The application will fail to run globally."
}

# Copy llama-server.exe binary and all supporting DLLs to make global installation self-contained
$llamaServerPath = Join-Path $scriptRoot "ArtifactFlow\bin\llama.cpp\build-gfx1101-vs2022\bin\llama-server.exe"
if (!(Test-Path $llamaServerPath)) {
    $llamaServerPath = Join-Path $scriptRoot "bin\llama.cpp\build-gfx1101-vs2022\bin\llama-server.exe"
}
if (Test-Path $llamaServerPath) {
    Write-Host "Copying llama-server and supporting DLLs..." -ForegroundColor Gray
    $llamaServerBinDir = Split-Path -Parent $llamaServerPath
    Copy-Item -Path $llamaServerPath -Destination $installDir -Force
    Copy-Item -Path (Join-Path $llamaServerBinDir "*.dll") -Destination $installDir -Force
} else {
    Write-Warning "llama-server.exe not found in build directory. Global GGUF execution will fall back to workspace."
}

# Copy app icon if it exists
$srcIconPath = Join-Path $srcDir "app_icon.ico"
if (Test-Path $srcIconPath) {
    Write-Host "Copying application icon as app_icon_v5.ico..." -ForegroundColor Gray
    # Remove old icon names to prevent confusion
    Remove-Item (Join-Path $installDir "app_icon.ico") -ErrorAction SilentlyContinue
    Remove-Item (Join-Path $installDir "app_icon_v2.ico") -ErrorAction SilentlyContinue
    Remove-Item (Join-Path $installDir "app_icon_v3.ico") -ErrorAction SilentlyContinue
    Remove-Item (Join-Path $installDir "app_icon_v4.ico") -ErrorAction SilentlyContinue
    Copy-Item -Path $srcIconPath -Destination (Join-Path $installDir "app_icon_v5.ico") -Force
}

# Copy prompts/, web/, and internal/ folders (internal/ is needed for constitutions)
$dirsToCopy = @("prompts", "web", "internal")
foreach ($dir in $dirsToCopy) {
    $srcPath = Join-Path $srcDir $dir
    if (Test-Path $srcPath) {
        Write-Host "Copying $dir/ directory..." -ForegroundColor Gray
        # Clean destination first to prevent nested folders on reinstall
        $destPath = Join-Path $installDir $dir
        if (Test-Path $destPath) {
            Remove-Item -Path $destPath -Recurse -Force | Out-Null
        }
        Copy-Item -Path $srcPath -Destination $installDir -Recurse -Force
    }
}

# Copy or create .env file
$devEnvPath = Join-Path $srcDir ".env"
$destEnvPath = Join-Path $installDir ".env"
if (Test-Path $devEnvPath) {
    Write-Host "Migrating existing .env configuration..." -ForegroundColor Gray
    Copy-Item -Path $devEnvPath -Destination $destEnvPath -Force
} else {
    Write-Host "Creating default .env template..." -ForegroundColor Gray
    $defaultEnv = @"
# --- LOCAL DEFAULTS ---
LOCAL_OLLAMA_URL=http://localhost:11434
LOCAL_OLLAMA_MODEL=llama3.2:latest

# --- LM STUDIO DEFAULTS ---
LMSTUDIO_URL=http://127.0.0.1:1234/api/v1/chat
LMSTUDIO_MODEL=local-model

# --- ACTIVE MODE ---
ORCHESTRATOR_MODE=local
"@
    [System.IO.File]::WriteAllText($destEnvPath, $defaultEnv)
}
Write-Host "Asset deployment complete." -ForegroundColor Green

# Verify critical deployed assets before registering aliases/shortcuts.
$requiredAssets = @(
    "main.exe",
    "VERSION",
    "prompts",
    "web",
    "internal"
)
$missingAssets = @()
foreach ($asset in $requiredAssets) {
    $assetPath = Join-Path $installDir $asset
    if (!(Test-Path $assetPath)) {
        $missingAssets += $asset
    }
}
if ($missingAssets.Count -gt 0) {
    Write-Error "Installation verification failed. Missing assets: $($missingAssets -join ', '). Re-run the installer from the workspace root after building dependencies."
    exit 1
}
Write-Host "Deployment verification passed. Version: $buildVersion" -ForegroundColor Green


# 4. Create Command Batch Aliases
Write-Host "[4/6] Registering global command aliases..." -ForegroundColor Cyan
$codingagentsBat = Join-Path $installDir "codingagents.bat"
$caBat = Join-Path $installDir "ca.bat"

# Create batch scripts that launch the compiled and signed production binary directly
$batContent = "@echo off`r`n`"$installDir\main.exe`" %*"
[System.IO.File]::WriteAllText($codingagentsBat, $batContent)
[System.IO.File]::WriteAllText($caBat, $batContent)
Write-Host "Registered commands 'codingagents' and 'ca' via compiled production binary." -ForegroundColor Green

# 5. Shell Environment Integration (User PATH)
Write-Host "[5/6] Adding installation folder to User PATH environment variable..." -ForegroundColor Cyan
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$installDir*") {
    $newPath = $userPath + ";" + $installDir
    $newPath = $newPath -replace ";+", ";"
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    Write-Host "PATH updated successfully. Please open a new terminal window to apply changes." -ForegroundColor Green
} else {
    Write-Host "Path already registered." -ForegroundColor Gray
}

# 6. Create Windows Shortcuts
Write-Host "[6/6] Registering Windows shortcuts..." -ForegroundColor Cyan
try {
    $wshShell = New-Object -ComObject WScript.Shell
    
    # Start Menu Shortcut
    $startMenuDir = [System.IO.Path]::Combine([Environment]::GetFolderPath("StartMenu"), "Programs")
    if (Test-Path $startMenuDir) {
        $shortcut = $wshShell.CreateShortcut((Join-Path $startMenuDir "CodingAgents.lnk"))
        $shortcut.TargetPath = Join-Path $installDir "main.exe"
        $shortcut.WorkingDirectory = $installDir
        $shortcut.Description = "CodingAgents Cognitive Orchestrator"
        $iconPath = Join-Path $installDir "app_icon_v5.ico"
        if (Test-Path $iconPath) {
            $shortcut.IconLocation = "$iconPath,0"
        }
        $shortcut.Save()
    }

    # Desktop Shortcut
    $desktopDir = [Environment]::GetFolderPath("Desktop")
    if (Test-Path $desktopDir) {
        $shortcut = $wshShell.CreateShortcut((Join-Path $desktopDir "CodingAgents.lnk"))
        $shortcut.TargetPath = Join-Path $installDir "main.exe"
        $shortcut.WorkingDirectory = $installDir
        $shortcut.Description = "CodingAgents Cognitive Orchestrator"
        $iconPath = Join-Path $installDir "app_icon_v5.ico"
        if (Test-Path $iconPath) {
            $shortcut.IconLocation = "$iconPath,0"
        }
        $shortcut.Save()
    }
    Write-Host "Shortcuts created successfully with custom icon." -ForegroundColor Green
} catch {
    Write-Host "Warning: Failed to create shortcuts: $_" -ForegroundColor Yellow
}

Write-Host "==================================================" -ForegroundColor Cyan
Write-Host "  CodingAgentsGO Installation Completed!" -ForegroundColor Green
Write-Host "==================================================" -ForegroundColor Cyan
Write-Host "To run the application, open a FRESH terminal and type:" -ForegroundColor Gray
Write-Host "  ca" -ForegroundColor Green
Write-Host "or" -ForegroundColor Gray
Write-Host "  codingagents" -ForegroundColor Green
Write-Host "==================================================" -ForegroundColor Cyan
