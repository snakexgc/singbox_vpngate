# Explicit Windows service administration. This script never connects a VPN.
# Distribution layout: vpngate-service.exe and sing-box.exe (or core/sing-box.exe).
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('Install', 'Recover', 'Uninstall', 'Status')]
    [string] $Action,
    [string] $ServiceExecutable
)

$ErrorActionPreference = 'Stop'
if (-not $IsWindows -and $PSVersionTable.PSEdition -eq 'Core') {
    throw 'VPNGate protection requires Windows.'
}

if (-not $ServiceExecutable) {
    $installedExe = Join-Path ([Environment]::GetFolderPath('ProgramFiles')) 'SingBoxVPNGate\vpngate-service.exe'
    if ($Action -ne 'Install' -and (Test-Path -LiteralPath $installedExe)) {
        $ServiceExecutable = $installedExe
    } else {
        $ServiceExecutable = Join-Path $PSScriptRoot '..\build\bin\vpngate-service.exe'
    }
}
$serviceExePath = (Resolve-Path -LiteralPath $ServiceExecutable).Path
if ([IO.Path]::GetExtension($serviceExePath) -ne '.exe') {
    throw 'ServiceExecutable must be a compiled vpngate-service.exe.'
}

if ($Action -eq 'Status') {
    & $serviceExePath --status
    if ($LASTEXITCODE -ne 0) { throw "Service status failed ($LASTEXITCODE)." }
    return
}

$ownerSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
if ($ownerSID -notmatch '^S-1-5-21-\d+-\d+-\d+-\d+$') {
    throw 'Run this explicit installer from the intended interactive user account.'
}
$serviceArgs = @('--' + $Action.ToLowerInvariant(), '--owner-sid', $ownerSID)
$process = Start-Process -FilePath $serviceExePath -ArgumentList $serviceArgs -WorkingDirectory ([IO.Path]::GetDirectoryName($serviceExePath)) -Verb RunAs -WindowStyle Hidden -Wait -PassThru
if ($process.ExitCode -ne 0) {
    throw "VPNGate service $Action failed ($($process.ExitCode)). Existing protection is retained when an operation fails."
}
Write-Output "VPNGate service $Action completed. No VPN connection was started."
