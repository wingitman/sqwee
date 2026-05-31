#Requires -Version 5.1
<#
.SYNOPSIS
    Uninstalls sqwee on Windows.

.DESCRIPTION
    Removes the sqwee binary from $env:LOCALAPPDATA\Programs\sqwee and removes
    that directory from your user PATH. Config and data files are left in place
    unless -Purge is supplied.

.EXAMPLE
    .\uninstall.ps1
    .\uninstall.ps1 -Purge
#>

param(
    [switch]$Purge
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\sqwee'
$ConfigDir  = Join-Path $env:APPDATA 'delbysoft'

function Write-Step([string]$msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Ok([string]$msg)   { Write-Host "    $msg" -ForegroundColor Green }
function Write-Note([string]$msg) { Write-Host "    $msg" -ForegroundColor Yellow }

# 1. Remove the install directory
Write-Step 'Removing binary...'
if (Test-Path $InstallDir) {
    Remove-Item -Path $InstallDir -Recurse -Force
    Write-Ok "Removed $InstallDir"
} else {
    Write-Note "$InstallDir not found (already removed?)"
}

# 2. Strip install dir from user PATH
Write-Step 'Updating user PATH...'
$registryPath = 'HKCU:\Environment'
$currentPath  = (Get-ItemProperty -Path $registryPath -Name Path -ErrorAction SilentlyContinue).Path
if ($currentPath -and ($currentPath -split ';') -contains $InstallDir) {
    $newPath = ($currentPath -split ';' | Where-Object { $_ -ne $InstallDir }) -join ';'
    Set-ItemProperty -Path $registryPath -Name Path -Value $newPath
    Write-Ok "Removed $InstallDir from user PATH."
} else {
    Write-Note 'Install dir was not on your user PATH.'
}

# 3. Optionally purge config + data
if ($Purge) {
    Write-Step 'Purging config and data...'
    if (Test-Path (Join-Path $ConfigDir 'sqwee.toml')) {
        Remove-Item -Path (Join-Path $ConfigDir 'sqwee.toml') -Force
        Write-Ok 'Removed sqwee.toml'
    }
    if (Test-Path (Join-Path $ConfigDir 'sqwee.json')) {
        Remove-Item -Path (Join-Path $ConfigDir 'sqwee.json') -Force
        Write-Ok 'Removed sqwee.json'
    }
} else {
    Write-Host ''
    Write-Note 'Config and data files were left in place. To remove them:'
    Write-Note "  $ConfigDir\sqwee.toml"
    Write-Note "  $ConfigDir\sqwee.json"
    Write-Note 'Or re-run with -Purge.'
}

Write-Host ''
Write-Host '  sqwee uninstalled.' -ForegroundColor Green
