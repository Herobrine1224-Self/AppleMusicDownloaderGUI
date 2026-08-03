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
$activated = $false

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

function Install-StagedBuildInPlace {
    param(
        [Parameter(Mandatory = $true)][string]$Staging,
        [Parameter(Mandatory = $true)][string]$Destination,
        [Parameter(Mandatory = $true)][string]$TransactionID
    )
    # The runtime log directory (logs/) is intentionally not part of the
    # release layout: a running background runtime keeps wrapper.log open, so
    # replacing the whole directory would fail. These items are swapped one by
    # one and rolled back on error instead.
    $items = @(
        "downloader", "licenses", "runtime", "tools",
        "AppleMusicDownloader.exe",
        "README.md", "SHA256SUMS.txt", "THIRD-PARTY-NOTICES.md", "app.ico"
    )
    $backupRoot = Join-Path $Destination (".inplace-backup-{0}" -f $TransactionID)
    New-Item -ItemType Directory -Force -Path $backupRoot | Out-Null
    $installed = @()
    try {
        foreach ($name in $items) {
            $source = Join-Path $Staging $name
            if (-not (Test-Path -LiteralPath $source)) {
                continue
            }
            $dest = Join-Path $Destination $name
            $backup = Join-Path $backupRoot $name
            $hadItem = Test-Path -LiteralPath $dest
            if ($hadItem) {
                Move-Item -LiteralPath $dest -Destination $backup
            }
            Move-Item -LiteralPath $source -Destination $dest
            $installed += @{ Name = $name; HadItem = $hadItem; Dest = $dest; Backup = $backup }
        }
        # 旧版发布布局可能残留与 exe 同名的外部清单。Windows 优先读取外部
        # manifest，会遮蔽现在已嵌入 exe 的清单，因此必须删除（删除前先备份
        # 以便失败时回滚）。
        $legacyManifest = Join-Path $Destination "AppleMusicDownloader.exe.manifest"
        if (Test-Path -LiteralPath $legacyManifest) {
            $backup = Join-Path $backupRoot "AppleMusicDownloader.exe.manifest"
            Move-Item -LiteralPath $legacyManifest -Destination $backup
            $installed += @{
                Name = "AppleMusicDownloader.exe.manifest"
                HadItem = $true
                Dest = $legacyManifest
                Backup = $backup
            }
        }
    }
    catch {
        for ($i = $installed.Count - 1; $i -ge 0; $i--) {
            $item = $installed[$i]
            if (Test-Path -LiteralPath $item.Dest) {
                Remove-Item -LiteralPath $item.Dest -Recurse -Force -ErrorAction SilentlyContinue
            }
            if ($item.HadItem -and (Test-Path -LiteralPath $item.Backup)) {
                Move-Item -LiteralPath $item.Backup -Destination $item.Dest -ErrorAction SilentlyContinue
            }
        }
        # Restore the item that failed mid-install (backup moved, dest not installed).
        foreach ($name in $items) {
            $dest = Join-Path $Destination $name
            $backup = Join-Path $backupRoot $name
            if (-not (Test-Path -LiteralPath $dest) -and (Test-Path -LiteralPath $backup)) {
                Move-Item -LiteralPath $backup -Destination $dest -ErrorAction SilentlyContinue
            }
        }
        throw
    }
    finally {
        if (Test-Path -LiteralPath $backupRoot) {
            Remove-Item -LiteralPath $backupRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

New-Item -ItemType Directory -Force -Path $distRoot | Out-Null
$env:CGO_ENABLED = "0"
$env:GOCACHE = Join-Path $PSScriptRoot ".gocache"
Remove-Item Env:GOMODCACHE -ErrorAction SilentlyContinue

try {
    & (Join-Path $PSScriptRoot "make-icon.ps1")

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

    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "README.md") -Destination $staging
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "THIRD-PARTY-NOTICES.md") -Destination $staging
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "licenses\WALK.txt") -Destination (Join-Path $staging "licenses")
    Copy-Item -LiteralPath (Join-Path $downloaderRoot "utils\runv2\LICENSE") -Destination (Join-Path $staging "licenses\RUNV2.txt")
    Copy-Item -LiteralPath (Join-Path $mp4TagRoot "LICENSE") -Destination (Join-Path $staging "licenses\MP4TAG.txt")
    Copy-Item -LiteralPath (Join-Path $downloaderRoot "config.yaml") -Destination (Join-Path $staging "downloader")
    if (Test-Path -LiteralPath (Join-Path $PSScriptRoot "app.ico")) {
        Copy-Item -LiteralPath (Join-Path $PSScriptRoot "app.ico") -Destination (Join-Path $staging "app.ico")
    }
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

    if (-not (Test-Path -LiteralPath $target)) {
        Move-Item -LiteralPath $staging -Destination $target
    }
    else {
        try {
            Move-Item -LiteralPath $target -Destination $previous
            $previousMoved = $true
        }
        catch {
            Write-Warning "发布目录正在被占用（通常是后台运行时仍在使用日志文件），改为就地更新发布文件。"
            # 把可能已部分移走的内容尽量合并回目标目录，随后逐项就地替换。
            if (Test-Path -LiteralPath $previous) {
                Get-ChildItem -LiteralPath $previous -Force | ForEach-Object {
                    $partialDest = Join-Path $target $_.Name
                    if (Test-Path -LiteralPath $partialDest) {
                        Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction SilentlyContinue
                    }
                    else {
                        Move-Item -LiteralPath $_.FullName -Destination $partialDest -ErrorAction SilentlyContinue
                    }
                }
                Remove-Item -LiteralPath $previous -Force -ErrorAction SilentlyContinue
            }
        }
        if ($previousMoved) {
            Move-Item -LiteralPath $staging -Destination $target
            Remove-Item -LiteralPath $previous -Recurse -Force
            $previousMoved = $false
        }
        else {
            Install-StagedBuildInPlace -Staging $staging -Destination $target -TransactionID $buildID
            $activated = $true
        }
    }
}
finally {
    if (Test-Path -LiteralPath $staging) {
        Remove-Item -LiteralPath $staging -Recurse -Force
    }
    if ($previousMoved -and -not (Test-Path -LiteralPath $target)) {
        Move-Item -LiteralPath $previous -Destination $target
    }
    if ($activated -and (Test-Path -LiteralPath $previous)) {
        Remove-Item -LiteralPath $previous -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Write-Host "Built and verified: $target"
