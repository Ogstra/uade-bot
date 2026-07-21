[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$NodeBin,

    [Parameter(Mandatory = $true)]
    [string]$OutputDir
)

$ErrorActionPreference = 'Stop'

if (-not [IO.Path]::IsPathFullyQualified($NodeBin)) {
    throw '-NodeBin must be an absolute path.'
}

$candidate = [IO.Path]::GetFullPath($NodeBin)
if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
    throw '-NodeBin must reference an existing file.'
}
$nodePath = [IO.Path]::GetFullPath((Resolve-Path -LiteralPath $candidate).ProviderPath)

$runtimeJson = (& $nodePath --expose-gc -p 'JSON.stringify({nodeVersion:process.versions.node,execPath:process.execPath,gcAvailable:typeof global.gc==="function"})').Trim()
if ($LASTEXITCODE -ne 0) { throw "Node runtime preflight failed with exit code $LASTEXITCODE." }
$runtime = $runtimeJson | ConvertFrom-Json
if ([int]($runtime.nodeVersion.Split('.')[0]) -lt 24) { throw "Node runtime major must be 24 or newer; received $($runtime.nodeVersion)." }
if (-not $runtime.gcAvailable) { throw 'Node runtime must expose GC.' }
if ([IO.Path]::GetFullPath($runtime.execPath) -ne $nodePath) { throw "Node execPath mismatch: $($runtime.execPath)" }

$outputPath = [IO.Path]::GetFullPath($OutputDir)
[IO.Directory]::CreateDirectory($outputPath) | Out-Null
$benchmarkScript = Join-Path $PSScriptRoot 'benchmark-http-search-memory.js'
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$sourceCommit = (& git -c "safe.directory=$($repoRoot.Replace('\','/'))" -C $repoRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0) { throw 'Unable to resolve source commit.' }

$isolatedLines = @(& $nodePath --expose-gc $benchmarkScript --scenario isolated --runs 5)
if ($LASTEXITCODE -ne 0) { throw "Isolated benchmark failed with exit code $LASTEXITCODE; no pass summary was published." }
$concurrentLines = @(& $nodePath --expose-gc $benchmarkScript --scenario concurrent --concurrency 2)
if ($LASTEXITCODE -ne 0) { throw "Concurrent benchmark failed with exit code $LASTEXITCODE; no pass summary was published." }

$isolatedFile = Join-Path $outputPath 'isolated.jsonl'
$concurrentFile = Join-Path $outputPath 'concurrent.jsonl'
$summaryFile = Join-Path $outputPath 'summary.json'
$isolatedLines | Set-Content -LiteralPath $isolatedFile -Encoding utf8
$concurrentLines | Set-Content -LiteralPath $concurrentFile -Encoding utf8

$summaryLines = @(& $nodePath --expose-gc $benchmarkScript --aggregate --isolated-file $isolatedFile --concurrent-file $concurrentFile --source-commit $sourceCommit)
if ($LASTEXITCODE -ne 0) {
    throw "Evidence aggregation failed with exit code $LASTEXITCODE; no pass summary was published."
}
$summaryLines | Set-Content -LiteralPath $summaryFile -Encoding utf8

Write-Output "memory-benchmark-pass version=$($runtime.nodeVersion) execPath=$($runtime.execPath) output=$outputPath"
