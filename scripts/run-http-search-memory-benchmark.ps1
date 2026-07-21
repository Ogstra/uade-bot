[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$NodeBin,

    [Parameter(Mandatory = $true)]
    [string]$OutputDir
)

$ErrorActionPreference = 'Stop'

$outputPath = [IO.Path]::GetFullPath($OutputDir)
[IO.Directory]::CreateDirectory($outputPath) | Out-Null
$isolatedFile = Join-Path $outputPath 'isolated.jsonl'
$concurrentFile = Join-Path $outputPath 'concurrent.jsonl'
$summaryFile = Join-Path $outputPath 'summary.json'

# A requested run invalidates any prior pass immediately. The replacement
# summary is promoted last, only after both scenarios and aggregation succeed.
if (Test-Path -LiteralPath $summaryFile) {
    Remove-Item -LiteralPath $summaryFile -Force
}
$runPath = Join-Path $outputPath ('.memory-run-' + [guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($runPath) | Out-Null

try {
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

    $benchmarkScript = Join-Path $PSScriptRoot 'benchmark-http-search-memory.js'
    $repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
    $sourceScope = @(
        'package.json',
        'package-lock.json',
        'scripts/benchmark-http-search-memory.js',
        'scripts/http-search-memory-contract.js',
        'scripts/run-http-search-memory-benchmark.ps1',
        'scripts/validate-http-search-memory-evidence.js',
        ':(glob)src/automation/**/*.js',
        'src/schemas.js',
        'src/logger.js'
    )
    $dirtyBefore = @(& git -c "safe.directory=$($repoRoot.Replace('\','/'))" -C $repoRoot status --porcelain=v1 --untracked-files=all -- @sourceScope)
    if ($LASTEXITCODE -ne 0) { throw 'Unable to inspect benchmark source scope.' }
    if ($dirtyBefore.Count -ne 0) { throw "Benchmark source scope differs from HEAD: $($dirtyBefore[0])" }
    $sourceCommit = (& git -c "safe.directory=$($repoRoot.Replace('\','/'))" -C $repoRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Unable to resolve source commit.' }

    $isolatedLines = @(& $nodePath --expose-gc $benchmarkScript --scenario isolated --runs 5)
    if ($LASTEXITCODE -ne 0) { throw "Isolated benchmark failed with exit code $LASTEXITCODE; no pass summary was published." }
    $concurrentLines = @(& $nodePath --expose-gc $benchmarkScript --scenario concurrent --concurrency 2)
    if ($LASTEXITCODE -ne 0) { throw "Concurrent benchmark failed with exit code $LASTEXITCODE; no pass summary was published." }

    $pendingIsolatedFile = Join-Path $runPath 'isolated.jsonl'
    $pendingConcurrentFile = Join-Path $runPath 'concurrent.jsonl'
    $pendingSummaryFile = Join-Path $runPath 'summary.json'
    $isolatedLines | Set-Content -LiteralPath $pendingIsolatedFile -Encoding utf8
    $concurrentLines | Set-Content -LiteralPath $pendingConcurrentFile -Encoding utf8

    $summaryLines = @(& $nodePath --expose-gc $benchmarkScript --aggregate --isolated-file $pendingIsolatedFile --concurrent-file $pendingConcurrentFile --source-commit $sourceCommit)
    if ($LASTEXITCODE -ne 0) {
        throw "Evidence aggregation failed with exit code $LASTEXITCODE; no pass summary was published."
    }
    $summaryLines | Set-Content -LiteralPath $pendingSummaryFile -Encoding utf8
    $pendingSummary = Get-Content -Raw -LiteralPath $pendingSummaryFile | ConvertFrom-Json
    if ($pendingSummary.pass -ne $true -or $pendingSummary.sourceCommit -ne $sourceCommit) {
        throw 'Aggregated evidence did not produce a pass for the current source commit.'
    }
    $sourceCommitAfter = (& git -c "safe.directory=$($repoRoot.Replace('\','/'))" -C $repoRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $sourceCommitAfter -ne $sourceCommit) { throw 'Source commit changed during benchmark execution.' }
    $dirtyAfter = @(& git -c "safe.directory=$($repoRoot.Replace('\','/'))" -C $repoRoot status --porcelain=v1 --untracked-files=all -- @sourceScope)
    if ($LASTEXITCODE -ne 0) { throw 'Unable to re-inspect benchmark source scope.' }
    if ($dirtyAfter.Count -ne 0) { throw "Benchmark source scope changed during execution: $($dirtyAfter[0])" }

    Move-Item -LiteralPath $pendingIsolatedFile -Destination $isolatedFile -Force
    Move-Item -LiteralPath $pendingConcurrentFile -Destination $concurrentFile -Force
    Move-Item -LiteralPath $pendingSummaryFile -Destination $summaryFile -Force

    Write-Output "memory-benchmark-pass version=$($runtime.nodeVersion) execPath=$($runtime.execPath) output=$outputPath"
} finally {
    if (Test-Path -LiteralPath $runPath) {
        Remove-Item -LiteralPath $runPath -Recurse -Force
    }
}
