$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
$resourceDirectory = Join-Path $repoRoot 'internal\vpngateruntime\rules'
$snapshotCommit = '231248b8e335758254deb15462cba5e2337d1e21'
$resources = @(
    @{ Name = 'cn.srs'; SHA256 = '0e8a3bf62266fedd8b4dd7c11bb0bfb16ae34e86728b747179411e0dfbdcf1cb' },
    @{ Name = 'gfw.srs'; SHA256 = 'ce4eb8aa6b2771f4499b3edba097f5310c5512bcde47ddedef301d0d2d23ddfc' }
)

New-Item -ItemType Directory -Path $resourceDirectory -Force | Out-Null
foreach ($resource in $resources) {
    $targetPath = Join-Path $resourceDirectory $resource.Name
    if (Test-Path -LiteralPath $targetPath -PathType Leaf) {
        $currentHash = (Get-FileHash -LiteralPath $targetPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($currentHash -eq $resource.SHA256) {
            Write-Output "Verified local rule resource: $($resource.Name)"
            continue
        }
    }

    $temporaryPath = Join-Path $resourceDirectory ('.' + $resource.Name + '.' + [guid]::NewGuid().ToString('N') + '.tmp')
    try {
        $resourceURL = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/$snapshotCommit/geo/geosite/$($resource.Name)"
        Invoke-WebRequest -Uri $resourceURL -OutFile $temporaryPath -UseBasicParsing
        $downloadedHash = (Get-FileHash -LiteralPath $temporaryPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($downloadedHash -ne $resource.SHA256) {
            throw "Rule resource checksum mismatch: $($resource.Name). Existing resource was preserved."
        }
        if (Test-Path -LiteralPath $targetPath -PathType Leaf) {
            [System.IO.File]::Replace($temporaryPath, $targetPath, [NullString]::Value)
        } else {
            [System.IO.File]::Move($temporaryPath, $targetPath)
        }
        Write-Output "Prepared pinned rule resource: $($resource.Name)"
    } finally {
        if (Test-Path -LiteralPath $temporaryPath) {
            Remove-Item -LiteralPath $temporaryPath -Force
        }
    }
}
