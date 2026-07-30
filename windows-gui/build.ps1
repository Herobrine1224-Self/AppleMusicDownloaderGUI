[CmdletBinding()]
param(
    [string]$OutputDirectory,
    [string]$GPACDirectory = "C:\Program Files\GPAC"
)

$ErrorActionPreference = "Stop"
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot ".."))
$distRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "dist"))
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $distRoot "AppleMusicDownloader"
}
$target = [System.IO.Path]::GetFullPath($OutputDirectory)
$distPrefix = $distRoot.TrimEnd([System.IO.Path]::DirectorySeparatorChar) + [System.IO.Path]::DirectorySeparatorChar
if (-not $target.StartsWith($distPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "OutputDirectory must be inside $distRoot"
}
if (-not (Test-Path -LiteralPath (Join-Path $GPACDirectory "mp4box.exe") -PathType Leaf)) {
    throw "GPAC runtime not found at $GPACDirectory"
}

$bootstrapRoot = Join-Path $projectRoot "windows-bootstrap"
$downloaderRoot = Join-Path $projectRoot "apple-music-downloader-main"
$mp4TagRoot = Join-Path $downloaderRoot "third_party\go-mp4tag"
$bootstrapPackage = Join-Path $bootstrapRoot "dist\AppleMusicWSL"
$buildID = [Guid]::NewGuid().ToString("N")
$staging = Join-Path $distRoot (".AppleMusicDownloader.staging-{0}" -f $buildID)
$previous = Join-Path $distRoot (".AppleMusicDownloader.previous-{0}" -f $buildID)
$previousMoved = $false

function Invoke-GoChecked {
    param(
        [Parameter(Mandatory = $true)][string]$WorkingDirectory,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )
    $previousModuleCache = $env:GOMODCACHE
    if ($WorkingDirectory -eq $PSScriptRoot) {
        $env:GOMODCACHE = Join-Path $PSScriptRoot ".gomodcache"
    }
    else {
        Remove-Item Env:GOMODCACHE -ErrorAction SilentlyContinue
    }
    Push-Location $WorkingDirectory
    try {
        & go @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "go $($Arguments -join ' ') failed in $WorkingDirectory"
        }
    }
    finally {
        Pop-Location
        if ([string]::IsNullOrEmpty($previousModuleCache)) {
            Remove-Item Env:GOMODCACHE -ErrorAction SilentlyContinue
        }
        else {
            $env:GOMODCACHE = $previousModuleCache
        }
    }
}

New-Item -ItemType Directory -Force -Path $distRoot | Out-Null
$env:CGO_ENABLED = "0"
$env:GOCACHE = Join-Path $PSScriptRoot ".gocache"
Remove-Item Env:GOMODCACHE -ErrorAction SilentlyContinue

try {
    & (Join-Path $bootstrapRoot "build.ps1")
    if ($LASTEXITCODE -ne 0) {
        throw "WSL bootstrap build failed"
    }

    Invoke-GoChecked -WorkingDirectory $downloaderRoot -Arguments @("test", "-buildvcs=false", "./...")
    Invoke-GoChecked -WorkingDirectory $downloaderRoot -Arguments @("vet", "-buildvcs=false", "./...")
    Invoke-GoChecked -WorkingDirectory $mp4TagRoot -Arguments @("test", "-buildvcs=false", "./...")
    Invoke-GoChecked -WorkingDirectory $mp4TagRoot -Arguments @("vet", "-buildvcs=false", "./...")
    Invoke-GoChecked -WorkingDirectory $PSScriptRoot -Arguments @("test", "-buildvcs=false", "./...")
    Invoke-GoChecked -WorkingDirectory $PSScriptRoot -Arguments @("vet", "-buildvcs=false", "./...")

    New-Item -ItemType Directory -Path $staging | Out-Null
    foreach ($directory in @("runtime", "downloader", "tools\gpac", "licenses")) {
        New-Item -ItemType Directory -Path (Join-Path $staging $directory) | Out-Null
    }

    Invoke-GoChecked -WorkingDirectory $PSScriptRoot -Arguments @(
        "build", "-buildvcs=false", "-trimpath", "-ldflags=-H windowsgui -s -w",
        "-o", (Join-Path $staging "AppleMusicDownloader.exe"), "./cmd/applemusic-gui"
    )
    Invoke-GoChecked -WorkingDirectory $downloaderRoot -Arguments @(
        "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w",
        "-o", (Join-Path $staging "downloader\AppleMusicDownloaderCLI.exe"), "."
    )

    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "app.manifest") -Destination (Join-Path $staging "AppleMusicDownloader.exe.manifest")
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "README.md") -Destination $staging
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "THIRD-PARTY-NOTICES.md") -Destination $staging
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "licenses\WALK.txt") -Destination (Join-Path $staging "licenses")
    Copy-Item -LiteralPath (Join-Path $downloaderRoot "utils\runv2\LICENSE") -Destination (Join-Path $staging "licenses\RUNV2.txt")
    Copy-Item -LiteralPath (Join-Path $mp4TagRoot "LICENSE") -Destination (Join-Path $staging "licenses\MP4TAG.txt")
    Copy-Item -LiteralPath (Join-Path $downloaderRoot "config.yaml") -Destination (Join-Path $staging "downloader")
    Get-ChildItem -LiteralPath $bootstrapPackage -Force | Copy-Item -Destination (Join-Path $staging "runtime") -Recurse

    Get-ChildItem -LiteralPath $GPACDirectory -Force | Where-Object {
        $_.Name -notin @("cache", "sdk", "uninstall.exe")
    } | Copy-Item -Destination (Join-Path $staging "tools\gpac") -Recurse

    $bootstrapExecutable = Join-Path $staging "runtime\AppleMusicWSL.exe"
    & $bootstrapExecutable verify --json
    if ($LASTEXITCODE -ne 0) {
        throw "Packaged WSL payload verification failed"
    }

    $checksums = Get-ChildItem -LiteralPath $staging -Recurse -File | Where-Object {
        $_.Name -ne "SHA256SUMS.txt"
    } | Sort-Object FullName | ForEach-Object {
        $relative = [System.IO.Path]::GetRelativePath($staging, $_.FullName).Replace('\', '/')
        $hash = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        "{0}  {1}" -f $hash, $relative
    }
    [System.IO.File]::WriteAllLines((Join-Path $staging "SHA256SUMS.txt"), $checksums, [System.Text.UTF8Encoding]::new($false))

    if (Test-Path -LiteralPath $target) {
        Move-Item -LiteralPath $target -Destination $previous
        $previousMoved = $true
    }
    Move-Item -LiteralPath $staging -Destination $target
    if ($previousMoved) {
        Remove-Item -LiteralPath $previous -Recurse -Force
        $previousMoved = $false
    }
}
finally {
    if (Test-Path -LiteralPath $staging) {
        Remove-Item -LiteralPath $staging -Recurse -Force
    }
    if ($previousMoved -and -not (Test-Path -LiteralPath $target)) {
        Move-Item -LiteralPath $previous -Destination $target
    }
}

Write-Host "Built and verified: $target"
