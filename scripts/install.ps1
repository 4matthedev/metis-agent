# Metis Agent installer for Windows.
#
#   irm https://raw.githubusercontent.com/4matthedev/metis-agent/main/scripts/install.ps1 | iex
#
# The default flow installs Ollama (through winget when available), pulls
# gemma4:e2b, installs the latest Metis release under %LOCALAPPDATA%, and adds
# it to the user PATH. Everything is optional: see -Help.
#
# Run from an ordinary PowerShell window. Administrator rights are not needed.
[CmdletBinding()]
param(
    [string]$Version = "latest",
    [ValidateSet("ollama", "none")]
    [string]$Backend = "ollama",
    [string]$Model = "",
    [string]$InstallDir = "",
    [switch]$NoModel,
    [switch]$NoBackend,
    [switch]$DryRun,
    [switch]$Uninstall,
    [switch]$Purge,
    [switch]$ForceConfig,
    [switch]$Help
)

$ErrorActionPreference = "Stop"
$Repo = "4matthedev/metis-agent"
$DefaultModel = "gemma4:e2b"

function Write-Step([string]$Message) {
    Write-Host ""
    Write-Host "==> $Message" -ForegroundColor Green
}
function Write-Ok([string]$Message) {
    Write-Host "OK  $Message" -ForegroundColor Green
}
function Write-Warn([string]$Message) {
    Write-Host "!   $Message" -ForegroundColor Yellow
}
function Fail([string]$Message) {
    Write-Host "X   $Message" -ForegroundColor Red
    exit 1
}
function Show-Usage {
    @"
Metis Agent installer for Windows

Usage:
  install.ps1 [options]

Options:
  -Version TAG       Install a specific release (e.g. 0.2.0; default: latest)
  -Backend NAME      ollama | none (default: ollama)
  -Model NAME        Ollama model to use (default: gemma4:e2b)
  -InstallDir DIR    Where metis-agent.exe is installed
                     (default: %LOCALAPPDATA%\metis-agent\bin)
  -NoModel           Do not pull an Ollama model
  -NoBackend         Install Metis only
  -ForceConfig       Overwrite an existing config.json (a backup is kept)
  -DryRun            Print actions without changing anything
  -Uninstall         Remove Metis from this user account
  -Purge             With -Uninstall, also remove config, sessions, and data
  -Help              Show this help

Examples:
  irm https://raw.githubusercontent.com/$Repo/main/scripts/install.ps1 | iex
  .\install.ps1 -Version 0.2.0 -Model gemma4:e4b
  .\install.ps1 -Uninstall
  .\install.ps1 -Uninstall -Purge
"@
}

if ($Help) {
    Show-Usage
    exit 0
}

if ($NoBackend) { $Backend = "none" }

$localAppData = $env:LOCALAPPDATA
if (-not $localAppData) { $localAppData = [Environment]::GetFolderPath("LocalApplicationData") }
$appData = $env:APPDATA
if (-not $appData) { $appData = [Environment]::GetFolderPath("ApplicationData") }

$dataDir = Join-Path $localAppData "metis-agent"
if (-not $InstallDir) { $InstallDir = Join-Path $dataDir "bin" }
$configDir = Join-Path $appData "metis-agent"
$configFile = Join-Path $configDir "config.json"
$exe = Join-Path $InstallDir "metis-agent.exe"
$logFile = Join-Path $dataDir "backend.log"

function Get-UserPath {
    [Environment]::GetEnvironmentVariable("Path", "User")
}
function Set-UserPath([string]$Value) {
    [Environment]::SetEnvironmentVariable("Path", $Value, "User")
}
function Add-ToUserPath([string]$Directory) {
    $current = Get-UserPath
    $parts = @()
    if ($current) { $parts = $current.Split(";") | Where-Object { $_ } }
    foreach ($p in $parts) {
        if ($p.TrimEnd("\") -ieq $Directory.TrimEnd("\")) { return }
    }
    $updated = (@($parts) + $Directory) -join ";"
    Set-UserPath $updated
}
function Remove-FromUserPath([string]$Directory) {
    $current = Get-UserPath
    if (-not $current) { return }
    $parts = $current.Split(";") | Where-Object { $_ }
    $kept = @($parts | Where-Object { $_.TrimEnd("\") -ine $Directory.TrimEnd("\") })
    if ($kept.Count -ne @($parts).Count) {
        Set-UserPath ($kept -join ";")
        Write-Ok "removed $Directory from the user PATH"
    }
}

function Uninstall-Metis {
    Write-Step "Uninstalling Metis Agent"
    if (Test-Path $exe) {
        if (-not $DryRun) { Remove-Item -LiteralPath $exe -Force }
        Write-Ok "removed $exe"
    } else {
        Write-Warn "no executable found at $exe"
    }
    if ((Test-Path $InstallDir) -and -not $DryRun) {
        $remaining = @(Get-ChildItem -LiteralPath $InstallDir -Force)
        if ($remaining.Count -eq 0) { Remove-Item -LiteralPath $InstallDir -Force }
    }
    Remove-FromUserPath $InstallDir

    if ($Purge) {
        foreach ($dir in @($dataDir, $configDir)) {
            if (Test-Path $dir) {
                if (-not $DryRun) { Remove-Item -LiteralPath $dir -Recurse -Force }
                Write-Ok "removed $dir"
            }
        }
    } else {
        Write-Host "Kept data and config:"
        Write-Host "  $dataDir (models, sessions, undo history)"
        Write-Host "  $configFile"
        Write-Host "Run with -Uninstall -Purge to remove them too."
    }
    Write-Host "Ollama was left installed."
    Write-Host "Open a new terminal to refresh PATH."
}

if ($Uninstall) {
    Uninstall-Metis
    exit 0
}

# --- platform checks --------------------------------------------------------
$arch = $env:PROCESSOR_ARCHITECTURE
switch ($arch) {
    "AMD64" { $arch = "amd64" }
    "ARM64" {
        Fail "Only a windows/amd64 build is published today. Download metis-agent_*_windows_amd64.tar.gz from https://github.com/$Repo/releases and use the amd64 binary under emulation."
    }
    default { Fail "unsupported architecture: $arch (windows/amd64 is required)" }
}
if (-not (Get-Command tar.exe -ErrorAction SilentlyContinue)) {
    Fail "tar.exe is required (included with Windows 10 1803 and later)"
}
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

Write-Step "Metis Agent installer"
Write-Host "platform: windows/$arch"
Write-Host "install:  $InstallDir"
Write-Host "data:     $dataDir"
Write-Host "config:   $configFile"
if ($DryRun) { Write-Warn "dry run: nothing will be installed" }

# --- resolve the release ----------------------------------------------------
function Resolve-Release([string]$Requested) {
    if ($Requested -and $Requested -ne "latest") {
        if ($Requested.StartsWith("v")) { return $Requested }
        return "v$Requested"
    }
    $api = "https://api.github.com/repos/$Repo/releases/latest"
    $release = Invoke-RestMethod -Uri $api -Headers @{ "User-Agent" = "metis-agent-installer" }
    if (-not $release.tag_name) { Fail "the latest release has no tag" }
    return $release.tag_name
}

if ($DryRun -and ($Version -eq "latest")) {
    $tag = "latest"
} else {
    $tag = Resolve-Release $Version
}
$bareVersion = $tag.TrimStart("v")
$assetName = "metis-agent_${bareVersion}_windows_${arch}.tar.gz"
$versionedUrl = "https://github.com/$Repo/releases/download/$tag/$assetName"
$stableUrl = "https://github.com/$Repo/releases/latest/download/metis-agent_windows_${arch}.tar.gz"
$downloadUrl = if ($tag -eq "latest") { $stableUrl } else { $versionedUrl }
$sumsUrl = "https://github.com/$Repo/releases/download/$tag/SHA256SUMS"
if ($tag -eq "latest") { $sumsUrl = "https://github.com/$Repo/releases/latest/download/SHA256SUMS" }

if ($DryRun) {
    Write-Host "  + download $downloadUrl"
    Write-Host "  + verify $assetName against SHA256SUMS"
    Write-Host "  + install metis-agent.exe -> $exe"
    if ($Backend -eq "ollama") {
        Write-Host "  + install/start Ollama and use $(if ($Model) { $Model } else { $DefaultModel })"
    }
    Write-Host "  + update the user PATH"
    Write-Host "  + write $configFile"
    exit 0
}

# --- install the binary -----------------------------------------------------
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ("metis-install-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempDir -Force | Out-Null
try {
    $archive = Join-Path $tempDir $assetName
    Write-Step "Downloading $assetName"
    Invoke-WebRequest -Uri $downloadUrl -OutFile $archive -UseBasicParsing

    Write-Step "Verifying checksum"
    try {
        $sums = (Invoke-WebRequest -Uri $sumsUrl -UseBasicParsing).Content
        $expected = $null
        foreach ($line in ($sums -split "`n")) {
            $fields = $line.Trim() -split "\s+"
            if ($fields.Count -eq 2 -and $fields[1].TrimStart("*") -eq $assetName) {
                $expected = $fields[0].ToLowerInvariant()
                break
            }
        }
        if (-not $expected) { Fail "SHA256SUMS does not list $assetName" }
        $actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected) { Fail "checksum mismatch for $assetName" }
        Write-Ok "checksum verified"
    } catch {
        Fail "could not verify the download: $($_.Exception.Message)"
    }

    Write-Step "Extracting"
    & tar.exe -xzf $archive -C $tempDir
    if ($LASTEXITCODE -ne 0) { Fail "tar could not extract $assetName" }
    $extracted = Join-Path $tempDir "metis-agent.exe"
    if (-not (Test-Path $extracted)) { Fail "the archive did not contain metis-agent.exe" }

    Write-Step "Installing"
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    if (Test-Path $exe) {
        try {
            Copy-Item -LiteralPath $exe -Destination "$exe.old" -Force
        } catch {
            Write-Warn "could not back up the old executable (is Metis running?)"
        }
    }
    Copy-Item -LiteralPath $extracted -Destination $exe -Force
    Write-Ok "installed $exe"

    Add-ToUserPath $InstallDir
    $env:Path = "$InstallDir;$env:Path"
    Write-Ok "added $InstallDir to the user PATH"

    & $exe --version
    if ($LASTEXITCODE -ne 0) { Fail "the installed binary does not run" }
} finally {
    Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}

# --- Ollama and the model ---------------------------------------------------
function Test-Ollama {
    try {
        $resp = Invoke-WebRequest -Uri "http://127.0.0.1:11434/api/tags" -UseBasicParsing -TimeoutSec 3
        return $resp.StatusCode -eq 200
    } catch {
        return $false
    }
}

function Install-Ollama {
    if (Get-Command ollama -ErrorAction SilentlyContinue) { return }

    $candidate = Join-Path $env:LOCALAPPDATA "Programs\Ollama\ollama.exe"
    if (Test-Path $candidate) {
        $env:Path = "$(Split-Path $candidate);$env:Path"
        return
    }

    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        Fail "Ollama is not installed and winget is unavailable. Install Ollama from https://ollama.com/download and re-run the installer."
    }
    Write-Step "Installing Ollama with winget"
    & winget install --id Ollama.Ollama -e --silent --accept-package-agreements --accept-source-agreements
    if ($LASTEXITCODE -ne 0) {
        Fail "winget could not install Ollama. Install it from https://ollama.com/download and re-run."
    }
    if (-not (Get-Command ollama -ErrorAction SilentlyContinue)) {
        if (Test-Path $candidate) {
            $env:Path = "$(Split-Path $candidate);$env:Path"
        } else {
            Fail "Ollama was installed but ollama.exe is not on PATH. Open a new terminal and re-run."
        }
    }
}

$chosenModel = if ($Model) { $Model } else { $DefaultModel }

if ($Backend -eq "ollama") {
    Write-Step "Configuring Ollama"
    New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
    Install-Ollama
    if (-not (Test-Ollama)) {
        Write-Host "starting the Ollama server in the background"
        Start-Process -FilePath "ollama" -ArgumentList "serve" -WindowStyle Hidden `
            -RedirectStandardOutput $logFile -RedirectStandardError "$logFile.err" | Out-Null
        for ($i = 0; $i -lt 60; $i++) {
            Start-Sleep -Milliseconds 500
            if (Test-Ollama) { break }
        }
    }
    if (-not (Test-Ollama)) {
        Write-Warn "Ollama is not answering yet; start the Ollama app and run: ollama pull $chosenModel"
    } elseif (-not $NoModel) {
        Write-Step "Pulling $chosenModel"
        & ollama pull $chosenModel
        if ($LASTEXITCODE -ne 0) {
            Write-Warn "could not pull $chosenModel; run 'ollama pull $chosenModel' later"
        }
    } elseif ($NoModel) {
        Write-Warn "skipping the model download (-NoModel)"
    }
}

# --- config -----------------------------------------------------------------
Write-Step "Writing config"
if ((Test-Path $configFile) -and -not $ForceConfig) {
    Write-Ok "kept the existing config at $configFile (use -ForceConfig to rewrite)"
} else {
    New-Item -ItemType Directory -Path $configDir -Force | Out-Null
    if (Test-Path $configFile) {
        $backup = "$configFile.bak"
        Copy-Item -LiteralPath $configFile -Destination $backup -Force
        Write-Ok "backed up the old config to $backup"
    }
    $config = [ordered]@{}
    if ($Backend -eq "ollama") {
        $config["provider"] = "ollama"
        $config["base_url"] = "http://127.0.0.1:11434"
        $config["model"] = $chosenModel
    }
    $config["redact_secrets"] = $true
    $config["verify_timeout_sec"] = 120
    $json = $config | ConvertTo-Json
    $utf8 = New-Object System.Text.UTF8Encoding($false)
    [IO.File]::WriteAllText($configFile, $json, $utf8)
    Write-Ok "wrote $configFile"
}

Write-Step "Done"
if ($Backend -eq "ollama") {
    Write-Host "Backend: ollama"
    Write-Host "Model:   $chosenModel"
} else {
    Write-Host "Metis is installed. Configure a backend later with: metis-agent --provider ... --model ..."
}
Write-Host ""
Write-Host "Run it from anywhere, including your home folder:"
Write-Host "  metis-agent"
Write-Host ""
Write-Host "If the command is not found, open a new terminal to refresh PATH."
Write-Host "Logs: $logFile"
