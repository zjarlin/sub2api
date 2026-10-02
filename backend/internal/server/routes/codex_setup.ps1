param(
    [Parameter(Mandatory = $true)]
    [string]$BaseUrl,
    [Parameter(Mandatory = $true)]
    [string]$ApiKey,
    [ValidateSet("desktop", "cli")]
    [string]$Client = "desktop",
    [ValidateSet("api-key", "legacy")]
    [string]$AuthMode = "api-key",
    [string]$InstallDir = "",
    [string]$CodexHome = "",
    [switch]$PersistHome
)

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

function Test-CodexDesktopInstalled {
    $app = Get-AppxPackage -ErrorAction SilentlyContinue | Where-Object { $_.Name -match "^OpenAI[.](ChatGPT|Codex)" }
    return $null -ne $app
}

function ConvertTo-TomlString {
    param([string]$Value)
    return $Value.Replace("\", "\\").Replace('"', '\"')
}

$gatewayRoot = $BaseUrl.Trim().TrimEnd("/")
try {
    $parsedGateway = [Uri]$gatewayRoot
} catch {
    throw "BaseUrl is not a valid absolute URL: $BaseUrl"
}
if (-not $parsedGateway.IsAbsoluteUri -or ($parsedGateway.Scheme -ne "http" -and $parsedGateway.Scheme -ne "https")) {
    throw "BaseUrl must use http or https: $BaseUrl"
}
$apiBase = if ($gatewayRoot.EndsWith("/v1")) { $gatewayRoot } else { "$gatewayRoot/v1" }

if ($Client -eq "desktop" -and -not (Test-CodexDesktopInstalled)) {
    $tempDir = Join-Path ([IO.Path]::GetTempPath()) ("sub2api-codex-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Path $tempDir | Out-Null
    try {
        $installer = Join-Path $tempDir "ChatGPT-Installer.exe"
        Write-Host "Downloading the official Codex desktop installer through the gateway..."
        Invoke-WebRequest -UseBasicParsing -Uri "$gatewayRoot/downloads/ChatGPT-Installer.exe" -OutFile $installer -TimeoutSec 120
        if (-not (Test-Path -LiteralPath $installer) -or (Get-Item -LiteralPath $installer).Length -eq 0) {
            throw "The gateway returned an empty desktop installer."
        }
        Write-Host "Running the desktop installer. Microsoft Store access has a 90 second hard limit."
        $process = Start-Process -FilePath $installer -PassThru
        if (-not $process.WaitForExit(90000)) {
            try { $process.Kill() } catch { }
            throw "The Microsoft Store installer did not finish within 90 seconds."
        }
        if ($process.ExitCode -ne 0) {
            throw "The Microsoft Store installer exited with code $($process.ExitCode)."
        }
        for ($i = 0; $i -lt 30 -and -not (Test-CodexDesktopInstalled); $i++) {
            Start-Sleep -Seconds 1
        }
        if (-not (Test-CodexDesktopInstalled)) {
            throw "The desktop package was not detected after the installer exited."
        }
    } catch {
        Write-Warning $_.Exception.Message
        Write-Warning "Desktop installation failed; continuing with the official Codex CLI."
    } finally {
        Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$installCli = $Client -eq "cli" -or ($Client -eq "desktop" -and -not (Test-CodexDesktopInstalled))
if ($installCli) {
    $npm = Get-Command npm.cmd -ErrorAction SilentlyContinue
    if (-not $npm) {
        $npm = Get-Command npm -ErrorAction SilentlyContinue
    }
    if (-not $npm) {
        throw "Node.js 22.14 or newer with npm is required for the Codex CLI fallback."
    }
    $npmArgs = @("install", "--global", "@openai/codex", "--registry=https://registry.npmmirror.com")
    if ($InstallDir) {
        $npmArgs += @("--prefix", $InstallDir, "--cache", (Join-Path $InstallDir "npm-cache"))
    }
    Write-Host "Installing the official Codex CLI from the npm mirror..."
    & $npm.Source @npmArgs
    if ($LASTEXITCODE -ne 0) {
        throw "npm install failed with exit code $LASTEXITCODE."
    }
    if ($InstallDir) {
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        if (($userPath -split ";") -notcontains $InstallDir) {
            [Environment]::SetEnvironmentVariable("Path", ($InstallDir + ";" + $userPath), "User")
        }
    }
}

$configDir = if ($CodexHome) { $CodexHome } else { Join-Path $env:USERPROFILE ".codex" }
New-Item -ItemType Directory -Path $configDir -Force | Out-Null
$configPath = Join-Path $configDir "config.toml"
$catalogPath = Join-Path $configDir "codex-models.json"

$catalogJson = $null
try {
    $headers = @{ Authorization = "Bearer $ApiKey" }
    $catalog = Invoke-RestMethod -UseBasicParsing -Uri "$apiBase/models?client_version=0.156.1" -Headers $headers -TimeoutSec 30
    if ($catalog.models -and $catalog.models.Count -gt 0) {
        $catalogJson = $catalog | ConvertTo-Json -Depth 100
        [IO.File]::WriteAllText($catalogPath, $catalogJson, (New-Object Text.UTF8Encoding($false)))
    } else {
        Write-Warning "The gateway returned an empty model catalog; continuing without a local catalog file."
    }
} catch {
    Write-Warning "Model catalog download failed: $($_.Exception.Message)"
}

$catalogLine = if ($catalogJson) { 'model_catalog_json = "' + (ConvertTo-TomlString $catalogPath) + '"' } else { "" }
$authLines = if ($AuthMode -eq "legacy") {
    @('env_key = "SUB2API_API_KEY"', 'requires_openai_auth = false')
} else {
    @('requires_openai_auth = false', 'experimental_bearer_token = "' + (ConvertTo-TomlString $ApiKey) + '"')
}
$config = @"
# Codex CLI -> Sub2API
model_provider = "sub2api"
model = "gpt-5.5"
review_model = "gpt-5.5"
$catalogLine
disable_response_storage = true

[model_providers.sub2api]
name = "Sub2API"
base_url = "$(ConvertTo-TomlString $apiBase)"
$($authLines -join "`n")
wire_api = "responses"
supports_websockets = false
"@
[IO.File]::WriteAllText($configPath, $config, (New-Object Text.UTF8Encoding($false)))

if ($AuthMode -eq "legacy") {
    $authPath = Join-Path $configDir "auth.json"
    $authJson = @{ OPENAI_API_KEY = $ApiKey } | ConvertTo-Json
    [IO.File]::WriteAllText($authPath, $authJson, (New-Object Text.UTF8Encoding($false)))
}

if ($PersistHome -and $CodexHome) {
    [Environment]::SetEnvironmentVariable("CODEX_HOME", $CodexHome, "User")
}

Write-Host "Wrote $configPath"
if ($catalogJson) { Write-Host "Wrote $catalogPath" }
Write-Host "Codex setup complete. Restart Codex if it was already running."
