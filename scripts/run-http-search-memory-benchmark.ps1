[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Node24Bin
)

$ErrorActionPreference = 'Stop'

if (-not [IO.Path]::IsPathFullyQualified($Node24Bin)) {
    throw '-Node24Bin must be an absolute path.'
}

$fullPath = [IO.Path]::GetFullPath($Node24Bin)
if (-not (Test-Path -LiteralPath $fullPath -PathType Leaf)) {
    throw '-Node24Bin must reference an existing file.'
}

$node24Bin = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $fullPath).ProviderPath)

function Assert-Node24Runtime {
    $version = (& $node24Bin -p "process.versions.node").Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "Node runtime preflight failed with exit code $LASTEXITCODE."
    }

    $major = $version.Split('.')[0]
    if ($major -ne '24') {
        throw "Node runtime major must be exactly 24; received $version."
    }

    Write-Output "node24-preflight version=$version execPath=$node24Bin"
}

$benchmarkScript = Join-Path $PSScriptRoot 'benchmark-http-search-memory.js'

Assert-Node24Runtime
& $node24Bin --expose-gc $benchmarkScript --scenario isolated --runs 5
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}

Assert-Node24Runtime
& $node24Bin --expose-gc $benchmarkScript --scenario concurrent --concurrency 2
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
