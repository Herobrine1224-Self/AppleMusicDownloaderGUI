[CmdletBinding()]
param(
    [string]$OutputDirectory
)

$ErrorActionPreference = "Stop"
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $PSScriptRoot "dist\AppleMusicWSL"
}
$payloadSource = Join-Path $PSScriptRoot "..\wrapper-main"
$goCache = Join-Path $PSScriptRoot ".gocache"
$outputFullPath = [System.IO.Path]::GetFullPath($OutputDirectory)
$distRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "dist"))
$outputParent = Split-Path -Parent $outputFullPath
$outputLeaf = Split-Path -Leaf $outputFullPath

if ([string]::IsNullOrWhiteSpace($outputLeaf)) {
    throw "OutputDirectory must name a directory, not a filesystem root"
}
$distPrefix = $distRoot.TrimEnd([System.IO.Path]::DirectorySeparatorChar) + [System.IO.Path]::DirectorySeparatorChar
if (-not $outputFullPath.StartsWith($distPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "OutputDirectory must be a child directory of $distRoot"
}

function Install-StagedBuildInPlace {
    param(
        [Parameter(Mandatory = $true)][string]$Staging,
        [Parameter(Mandatory = $true)][string]$Destination,
        [Parameter(Mandatory = $true)][string]$TransactionID
    )

    $newPayload = Join-Path $Destination (".payload.new-{0}" -f $TransactionID)
    $oldPayload = Join-Path $Destination (".payload.old-{0}" -f $TransactionID)
    $executable = Join-Path $Destination "AppleMusicWSL.exe"
    $readme = Join-Path $Destination "README.md"
    $executableBackup = Join-Path $Destination (".AppleMusicWSL.exe.backup-{0}" -f $TransactionID)
    $readmeBackup = Join-Path $Destination (".README.md.backup-{0}" -f $TransactionID)
    $hadExecutable = Test-Path -LiteralPath $executable
    $hadReadme = Test-Path -LiteralPath $readme
    $hadPayload = Test-Path -LiteralPath (Join-Path $Destination "payload")
    $payloadActivated = $false
    $executableActivated = $false
    $readmeActivated = $false

    try {
        Move-Item -LiteralPath (Join-Path $Staging "payload") -Destination $newPayload
        if ($hadPayload) {
            Move-Item -LiteralPath (Join-Path $Destination "payload") -Destination $oldPayload
        }
        Move-Item -LiteralPath $newPayload -Destination (Join-Path $Destination "payload")
        $payloadActivated = $true

        if ($hadReadme) {
            [System.IO.File]::Replace((Join-Path $Staging "README.md"), $readme, $readmeBackup, $true)
        }
        else {
            [System.IO.File]::Move((Join-Path $Staging "README.md"), $readme)
        }
        $readmeActivated = $true

        if ($hadExecutable) {
            [System.IO.File]::Replace((Join-Path $Staging "AppleMusicWSL.exe"), $executable, $executableBackup, $true)
        }
        else {
            [System.IO.File]::Move((Join-Path $Staging "AppleMusicWSL.exe"), $executable)
        }
        $executableActivated = $true

    }
    catch {
        if ($executableActivated) {
            if (Test-Path -LiteralPath $executableBackup) {
                Copy-Item -LiteralPath $executableBackup -Destination $executable -Force
            }
            elseif (-not $hadExecutable -and (Test-Path -LiteralPath $executable)) {
                Remove-Item -LiteralPath $executable -Force
            }
        }
        if ($readmeActivated) {
            if (Test-Path -LiteralPath $readmeBackup) {
                Copy-Item -LiteralPath $readmeBackup -Destination $readme -Force
            }
            elseif (-not $hadReadme -and (Test-Path -LiteralPath $readme)) {
                Remove-Item -LiteralPath $readme -Force
            }
        }
        if ($payloadActivated -and (Test-Path -LiteralPath (Join-Path $Destination "payload"))) {
            Move-Item -LiteralPath (Join-Path $Destination "payload") -Destination $newPayload
        }
        if ($hadPayload -and (Test-Path -LiteralPath $oldPayload)) {
            Move-Item -LiteralPath $oldPayload -Destination (Join-Path $Destination "payload")
        }
        throw
    }
    finally {
        foreach ($temporary in @($newPayload, $oldPayload, $executableBackup, $readmeBackup)) {
            if (Test-Path -LiteralPath $temporary) {
                Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue
            }
        }
    }
    Get-ChildItem -LiteralPath $Destination -Force | Where-Object {
        $_.Name -notin @("AppleMusicWSL.exe", "README.md", "payload")
    } | Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
}

foreach ($required in @(
    (Join-Path $payloadSource "wrapper"),
    (Join-Path $payloadSource "rootfs\system\bin\main"),
    (Join-Path $payloadSource "rootfs\system\bin\linker64")
)) {
    if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
        throw "Required wrapper payload file is missing: $required"
    }
}

$env:GOCACHE = $goCache
Push-Location $PSScriptRoot
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) {
        throw "Go tests failed"
    }
}
finally {
    Pop-Location
}

New-Item -ItemType Directory -Force -Path $outputParent | Out-Null
$buildID = [Guid]::NewGuid().ToString("N")
$stagingDirectory = Join-Path $outputParent (".{0}.staging-{1}" -f $outputLeaf, $buildID)
$previousDirectory = Join-Path $outputParent (".{0}.previous-{1}" -f $outputLeaf, $buildID)
$payloadDestination = Join-Path $stagingDirectory "payload"
$activated = $false
$previousMoved = $false

try {
    New-Item -ItemType Directory -Path $payloadDestination | Out-Null
    $stagedExecutable = Join-Path $stagingDirectory "AppleMusicWSL.exe"

    Push-Location $PSScriptRoot
    try {
        go build -buildvcs=false -trimpath -ldflags "-s -w" -o $stagedExecutable ./cmd/applemusic-wsl
        if ($LASTEXITCODE -ne 0) {
            throw "Go build failed"
        }
    }
    finally {
        Pop-Location
    }

    Copy-Item -LiteralPath (Join-Path $payloadSource "wrapper") -Destination $payloadDestination
    Copy-Item -LiteralPath (Join-Path $payloadSource "rootfs") -Destination $payloadDestination -Recurse
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "README.md") -Destination $stagingDirectory

    & $stagedExecutable verify --payload $payloadDestination --json
    if ($LASTEXITCODE -ne 0) {
        throw "Packaged payload verification failed"
    }

    if (Test-Path -LiteralPath $outputFullPath) {
        try {
            Move-Item -LiteralPath $outputFullPath -Destination $previousDirectory
            $previousMoved = $true
        }
        catch [System.IO.IOException] {
            Write-Warning "Release directory is in use; activating the verified build in place."
            Install-StagedBuildInPlace -Staging $stagingDirectory -Destination $outputFullPath -TransactionID $buildID
            $activated = $true
        }
    }
    if (-not $activated) {
        try {
            Move-Item -LiteralPath $stagingDirectory -Destination $outputFullPath
            $activated = $true
        }
        catch {
            if ($previousMoved -and -not (Test-Path -LiteralPath $outputFullPath)) {
                Move-Item -LiteralPath $previousDirectory -Destination $outputFullPath
                $previousMoved = $false
            }
            throw
        }
    }

    if ($previousMoved) {
        Remove-Item -LiteralPath $previousDirectory -Recurse -Force
        $previousMoved = $false
    }
}
finally {
    if (Test-Path -LiteralPath $stagingDirectory) {
        Remove-Item -LiteralPath $stagingDirectory -Recurse -Force
    }
    if ($previousMoved -and -not (Test-Path -LiteralPath $outputFullPath)) {
        Move-Item -LiteralPath $previousDirectory -Destination $outputFullPath
    }
}

Write-Host "Built and verified: $outputFullPath"
