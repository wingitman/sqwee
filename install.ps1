#Requires -Version 5.1
<#
.SYNOPSIS
    Installs sqwee on Windows.

.DESCRIPTION
    If Go is available the binary is compiled from source. Otherwise the
    pre-built binary from releases\windows\sqwee.exe is used, so users without
    Go can install without any additional tools.

    The binary is installed to $env:LOCALAPPDATA\Programs\sqwee and that
    directory is added to your user PATH (persisted via the registry) without
    requiring admin rights.

    Use -BuildAll to cross-compile binaries for all platforms into releases\
    without performing an install (requires Go).

.PARAMETER BuildAll
    Cross-compile binaries for all platforms and write them to releases\.
    Requires Go. Does not install the binary or modify PATH.

.EXAMPLE
    .\install.ps1

.EXAMPLE
    .\install.ps1 -BuildAll
#>

param(
    [switch]$Update,
    [switch]$BuildAll
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$BinaryName  = 'sqwee.exe'
$InstallDir  = Join-Path $env:LOCALAPPDATA 'Programs\sqwee'
$BuildDir    = Join-Path $PSScriptRoot 'bin'
$BinaryBuild = Join-Path $BuildDir $BinaryName
$BinaryDest  = Join-Path $InstallDir $BinaryName
$ReleaseBin  = Join-Path $PSScriptRoot 'releases\windows\sqwee.exe'

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
# 1. BuildAll - cross-compile for all platforms and exit
# ---------------------------------------------------------------------------
if ($BuildAll) {
    Write-Step 'Checking for Go (required for -BuildAll)...'
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        Write-Host 'ERROR: Go is not installed or not on PATH.' -ForegroundColor Red
        Write-Host 'Download Go from https://go.dev/dl/ and re-run with -BuildAll.' -ForegroundColor Red
        exit 1
    }
    Write-Ok (go version)

    $Commit = (git rev-parse HEAD 2>$null)
    if (-not $Commit) { $Commit = 'dev' }
    $LdFlags = "-s -w -X main.Commit=$Commit"

    $Targets = @(
        @{ GOOS = 'linux';   GOARCH = 'amd64'; Out = 'releases\linux\sqwee'        },
        @{ GOOS = 'darwin';  GOARCH = 'amd64'; Out = 'releases\darwin\amd64\sqwee' },
        @{ GOOS = 'darwin';  GOARCH = 'arm64'; Out = 'releases\darwin\arm64\sqwee' },
        @{ GOOS = 'windows'; GOARCH = 'amd64'; Out = 'releases\windows\sqwee.exe'  }
    )

    foreach ($t in $Targets) {
        Write-Step "Building $($t.GOOS)/$($t.GOARCH)..."
        $dir = Split-Path (Join-Path $PSScriptRoot $t.Out)
        if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
        $env:GOOS   = $t.GOOS
        $env:GOARCH = $t.GOARCH
        & go build -ldflags $LdFlags -o (Join-Path $PSScriptRoot $t.Out) .
        if ($LASTEXITCODE -ne 0) {
            Write-Host "ERROR: build failed for $($t.GOOS)/$($t.GOARCH)." -ForegroundColor Red
            exit 1
        }
        Write-Ok $t.Out
    }

    $env:GOOS   = $null
    $env:GOARCH = $null

    Write-Host ''
    Write-Host '  Pre-built binaries written to releases\' -ForegroundColor Green
    Write-Host '  Commit these files so users without Go can install without building.' -ForegroundColor Yellow
    Write-Host ''
    exit 0
}

# ---------------------------------------------------------------------------
# 2. Obtain the binary - build from source if Go is present, else use pre-built
# ---------------------------------------------------------------------------
if (Get-Command go -ErrorAction SilentlyContinue) {
    Write-Step 'Go found - building sqwee from source...'
    Write-Ok (go version)
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
    $BinarySource = $BinaryBuild
} else {
    Write-Step 'Go not found - using pre-built binary from releases\windows\...'
    if (-not (Test-Path $ReleaseBin)) {
        Write-Host 'ERROR: Pre-built binary not found at releases\windows\sqwee.exe' -ForegroundColor Red
        Write-Host '       Please install Go (https://go.dev/dl/) and re-run, or ask a' -ForegroundColor Red
        Write-Host '       developer to run ".\install.ps1 -BuildAll" and commit the releases\ folder.' -ForegroundColor Red
        exit 1
    }
    Write-Ok "Using: $ReleaseBin"
    $BinarySource = $ReleaseBin
}

# ---------------------------------------------------------------------------
# 3. Install binary
# ---------------------------------------------------------------------------
Write-Step 'Installing binary...'
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir | Out-Null
}
Copy-Item -Path $BinarySource -Destination $BinaryDest -Force
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
