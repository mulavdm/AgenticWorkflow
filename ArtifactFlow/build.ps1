# build.ps1
# Automates the build process for both Go and Rust, formats code, and runs tests.

param (
    [switch]$Test = $false,
    [switch]$Fmt = $false,
    [switch]$Clean = $false
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

# Schema compilation (FlatBuffers)
function Compile-Schema {
    Write-Host "Compiling FlatBuffers schema..." -ForegroundColor Cyan
    .\bin\flatc.exe -g -o go-orchestrator/internal/ipc schema/workflow.fbs
    .\bin\flatc.exe -r -o rust-sidecar/src/ipc schema/workflow.fbs
}

# Go Tasks
function Build-Go {
    Write-Host "Building Go Orchestrator..." -ForegroundColor Cyan
    Set-Location "$ScriptDir\go-orchestrator"
    
    if ($Fmt) {
        Write-Host "Running go fmt..."
        go fmt ./...
    }
    
    if ($Clean) {
        Write-Host "Running go clean..."
        go clean
    }

    go build -o ../bin/orchestrator.exe ./cmd/orchestrator
    
    if ($Test) {
        Write-Host "Running Go tests..."
        go test ./...
    }
}

# Rust Tasks
function Build-Rust {
    Write-Host "Building Rust Sidecar..." -ForegroundColor Cyan
    Set-Location "$ScriptDir\rust-sidecar"
    
    if ($Fmt) {
        Write-Host "Running cargo fmt..."
        cargo fmt
    }
    
    if ($Clean) {
        Write-Host "Running cargo clean..."
        cargo clean
    }

    cargo build --release
    Copy-Item "target/release/rust-sidecar.exe" -Destination "../bin/rust-sidecar.exe" -ErrorAction SilentlyContinue
    
    if ($Test) {
        Write-Host "Running Rust tests..."
        cargo test
    }
}

# Main
New-Item -ItemType Directory -Force -Path "$ScriptDir\bin" | Out-Null

Compile-Schema
Build-Rust
Build-Go

Write-Host "Build Complete! Binaries are in the /bin directory." -ForegroundColor Green
Set-Location $ScriptDir
