param([ValidateSet('amd64')][string]$Architecture = 'amd64', [switch]$SkipGui)
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
& (Join-Path $PSScriptRoot 'prepare-rule-resources.ps1')
$outputDirectory = Join-Path $repoRoot "build\bin\singbox_vpngate-gui-windows-$Architecture"
$cacheDirectory = Join-Path $repoRoot 'build\bin\vpngate-cache'
New-Item -ItemType Directory -Force -Path $cacheDirectory | Out-Null

# The pinned official core is a build/test dependency, never a GUI payload.
$assetName = "sing-box-1.14.4-windows-$Architecture.zip"
$release = Invoke-RestMethod -Uri 'https://api.github.com/repos/SagerNet/sing-box/releases/tags/v1.14.4'
$asset = $release.assets | Where-Object name -EQ $assetName | Select-Object -First 1
if (!$asset -or !$asset.digest -or !$asset.digest.StartsWith('sha256:')) { throw 'Official release digest unavailable.' }
$archivePath = Join-Path $cacheDirectory $assetName
if (!(Test-Path -LiteralPath $archivePath)) { Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $archivePath }
$actualDigest = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualDigest -ne $asset.digest.Substring(7)) { throw 'Official sing-box archive checksum mismatch.' }
$expandedDirectory = Join-Path $cacheDirectory "sing-box-1.14.4-windows-$Architecture"
if (!(Test-Path -LiteralPath (Join-Path $expandedDirectory 'sing-box.exe'))) { Expand-Archive -LiteralPath $archivePath -DestinationPath $cacheDirectory -Force }

Push-Location $repoRoot
$previousCheckCore = $env:VPNGATE_CHECK_CORE
$previousCheckRules = $env:VPNGATE_CHECK_RULES
$previousLoopbackCore = $env:VPNGATE_LOOPBACK_CORE
try {
    $env:VPNGATE_CHECK_CORE = Join-Path $expandedDirectory 'sing-box.exe'
    $env:VPNGATE_CHECK_RULES = Join-Path $repoRoot 'internal\vpngateruntime\rules'
    $env:VPNGATE_LOOPBACK_CORE = $env:VPNGATE_CHECK_CORE
    go test ./internal/... ./bridge ./cmd/vpngate-service
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
    if ($SkipGui) {
        Write-Output 'Verified Go and native-core tests. GUI build and distribution packaging skipped.'
        return
    }
    Push-Location (Join-Path $repoRoot 'frontend')
    try { pnpm install --frozen-lockfile; if ($LASTEXITCODE -ne 0) { throw 'Dependency installation failed.' }; pnpm build; if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' } }
    finally { Pop-Location }
    go run github.com/wailsapp/wails/v2/cmd/wails@v2.17.0 build -s -trimpath -skipbindings -o singbox_vpngate.desktop.exe
    if ($LASTEXITCODE -ne 0) { throw 'Wails build failed.' }
    $payloadNames = @('singbox_vpngate.exe','GUI-LICENSE.txt','ACCEPTANCE.md')
    # Do not remove old bundled distributions or data from a previously run GUI.
    # Refuse a contaminated destination instead of silently shipping extra files.
    if (Test-Path -LiteralPath $outputDirectory) {
        $unexpectedFiles = @(Get-ChildItem -LiteralPath $outputDirectory -Force | Where-Object { $_.PSIsContainer -or $_.Name -notin ($payloadNames + 'checksums.json') })
        if ($unexpectedFiles.Count -ne 0) { throw "Distribution directory contains additional files or user data. Preserve them and use a clean destination: $outputDirectory" }
    }
    New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null
    Copy-Item -LiteralPath (Join-Path $repoRoot 'build\bin\singbox_vpngate.desktop.exe') -Destination (Join-Path $outputDirectory 'singbox_vpngate.exe') -Force
    Copy-Item -LiteralPath (Join-Path $repoRoot 'LICENSE') -Destination (Join-Path $outputDirectory 'GUI-LICENSE.txt') -Force
    Copy-Item -LiteralPath (Join-Path $repoRoot 'docs\vpngate-acceptance.md') -Destination (Join-Path $outputDirectory 'ACCEPTANCE.md') -Force
    $payloadPaths = $payloadNames | ForEach-Object { Join-Path $outputDirectory $_ }
    Get-FileHash -LiteralPath $payloadPaths -Algorithm SHA256 | Select-Object Hash,@{Name='File';Expression={Split-Path -Leaf $_.Path}} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $outputDirectory 'checksums.json') -Encoding utf8
    Write-Output "Built unpacked GUI-only distribution: $outputDirectory. Install the core explicitly from Settings using the saved external upstream."
} finally { $env:VPNGATE_CHECK_CORE = $previousCheckCore; $env:VPNGATE_CHECK_RULES = $previousCheckRules; $env:VPNGATE_LOOPBACK_CORE = $previousLoopbackCore; Pop-Location }
