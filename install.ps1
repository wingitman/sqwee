#Requires -Version 5.1
<#
.SYNOPSIS
    Installs sqwee on Windows.

.DESCRIPTION
    Builds the sqwee binary with 'go build', installs it to
    $env:LOCALAPPDATA\Programs\sqwee, and adds that directory to your
    user PATH (persisted via the registry) without requiring admin rights.

.EXAMPLE
    .\install.ps1
#>

param(
    [switch]$Update
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$BinaryName  = 'sqwee.exe'
$InstallDir  = Join-Path $env:LOCALAPPDATA 'Programs\sqwee'
$BuildDir    = Join-Path $PSScriptRoot 'bin'
$BinaryBuild = Join-Path $BuildDir $BinaryName
$BinaryDest  = Join-Path $InstallDir $BinaryName

function Write-Step([string]$msg) {
    Write-Host "==> $msg" -ForegroundColor Cyan
}

function Write-Ok([string]$msg) {
    Write-Host "    $msg" -ForegroundColor Green
}

function Write-Note([string]$msg) {
    Write-Host "    $msg" -ForegroundColor Yellow
}

# ---------------------------------------------------------------------------
# 1. Check Go is available
# ---------------------------------------------------------------------------
Write-Step 'Checking for Go...'
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host ''
    Write-Host 'ERROR: Go is not installed or not on PATH.' -ForegroundColor Red
    Write-Host 'Download Go from https://go.dev/dl/ and re-run this script.' -ForegroundColor Red
    exit 1
}
$goVersion = go version
Write-Ok $goVersion

# ---------------------------------------------------------------------------
# 2. Build
# ---------------------------------------------------------------------------
Write-Step 'Building sqwee...'
if (-not (Test-Path $BuildDir)) {
    New-Item -ItemType Directory -Path $BuildDir | Out-Null
}
$Commit = (git rev-parse HEAD 2>$null)
if (-not $Commit) { $Commit = 'dev' }
& go build -ldflags="-s -w -X main.Commit=$Commit" -o $BinaryBuild .
if ($LASTEXITCODE -ne 0) {
    Write-Host 'ERROR: go build failed.' -ForegroundColor Red
    exit 1
}
Write-Ok "Built: $BinaryBuild"

# ---------------------------------------------------------------------------
# 3. Install binary
# ---------------------------------------------------------------------------
Write-Step 'Installing binary...'
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir | Out-Null
}
Copy-Item -Path $BinaryBuild -Destination $BinaryDest -Force
Write-Ok "Installed: $BinaryDest"

# ---------------------------------------------------------------------------
# 4. Add install dir to user PATH (persistent, no admin required)
# ---------------------------------------------------------------------------
Write-Step 'Updating user PATH...'
$registryPath = 'HKCU:\Environment'
$currentPath  = (Get-ItemProperty -Path $registryPath -Name Path -ErrorAction SilentlyContinue).Path

if ($currentPath -and ($currentPath -split ';') -contains $InstallDir) {
    Write-Note "$InstallDir is already in your PATH."
} else {
    $newPath = if ($currentPath) { "$currentPath;$InstallDir" } else { $InstallDir }
    Set-ItemProperty -Path $registryPath -Name Path -Value $newPath
    Write-Ok "Added $InstallDir to user PATH."

    # Broadcast WM_SETTINGCHANGE so Explorer and new terminals pick up the
    # change without requiring a logoff.
    $signature = @'
[DllImport("user32.dll", SetLastError=true, CharSet=CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(
    IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam,
    uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
    $type   = Add-Type -MemberDefinition $signature -Name WinEnv -Namespace Win32 -PassThru
    $result = [UIntPtr]::Zero
    $type::SendMessageTimeout(
        [IntPtr]0xffff, 0x001A, [UIntPtr]::Zero, 'Environment',
        0x0002, 5000, [ref]$result
    ) | Out-Null
}

# Also update the current session so the user can run sqwee right away.
if (($env:PATH -split ';') -notcontains $InstallDir) {
    $env:PATH = "$env:PATH;$InstallDir"
}

# ---------------------------------------------------------------------------
# 5. Done
# ---------------------------------------------------------------------------
# Config and data live in %AppData%\Roaming\delbysoft\ on Windows
# (os.UserConfigDir() returns %AppData%\Roaming on Windows)
$ConfigFile = Join-Path $env:APPDATA 'delbysoft\sqwee.toml'
$DataFile   = Join-Path $env:APPDATA 'delbysoft\sqwee.json'

Write-Host ''
Write-Host '  sqwee installed successfully!' -ForegroundColor Green
Write-Host ''
Write-Host '  Open a new terminal and run:' -ForegroundColor White
Write-Host '    sqwee' -ForegroundColor Cyan
Write-Host ''
Write-Host '  Config file (created on first launch):' -ForegroundColor White
Write-Host "    $ConfigFile" -ForegroundColor Cyan
Write-Host ''
Write-Host '  Data file (saved connections):' -ForegroundColor White
Write-Host "    $DataFile" -ForegroundColor Cyan
Write-Host ''
Write-Note "  Tip: if you get an 'execution policy' error, run once as your user:"
Write-Note '    Set-ExecutionPolicy -Scope CurrentUser RemoteSigned'
Write-Host ''
