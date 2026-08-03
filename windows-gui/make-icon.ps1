[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
$iconPng = Join-Path $PSScriptRoot "app.png"
$iconIco = Join-Path $PSScriptRoot "app.ico"
$rsrcSyso = Join-Path $PSScriptRoot "cmd\applemusic-gui\rsrc.syso"

if (-not (Test-Path -LiteralPath $iconPng)) {
    Write-Warning "未找到源图标 app.png，跳过图标生成。"
    return
}

$ffmpeg = Get-Command ffmpeg -ErrorAction SilentlyContinue
$pngNewer = -not (Test-Path -LiteralPath $iconIco) -or
    ((Get-Item -LiteralPath $iconPng).LastWriteTime -gt (Get-Item -LiteralPath $iconIco).LastWriteTime)

if ($pngNewer) {
    if (-not $ffmpeg) {
        Write-Warning "未找到 ffmpeg，保留现有 app.ico。"
    }
    else {
        $tmp = Join-Path $env:TEMP ("appicon-" + [guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Path $tmp | Out-Null
        try {
            $sizes = @(16, 24, 32, 48, 64, 128, 256)
            $files = @()
            foreach ($s in $sizes) {
                $png = Join-Path $tmp "$s.png"
                $filter = "scale={0}:{1}:flags=lanczos" -f $s, $s
                & $ffmpeg.Source -y -loglevel error -i $iconPng -vf $filter $png
                if ($LASTEXITCODE -ne 0) {
                    throw "ffmpeg 生成 $s px 图标失败"
                }
                $files += [pscustomobject]@{
                    Size = $s
                    Data = [System.IO.File]::ReadAllBytes($png)
                }
            }
            $ms = New-Object System.IO.MemoryStream
            $bw = New-Object System.IO.BinaryWriter($ms)
            $bw.Write([uint16]0); $bw.Write([uint16]1); $bw.Write([uint16]$files.Count)
            $offset = 6 + 16 * $files.Count
            foreach ($f in $files) {
                $dim = if ($f.Size -ge 256) { 0 } else { $f.Size }
                $bw.Write([byte]$dim); $bw.Write([byte]$dim)
                $bw.Write([byte]0); $bw.Write([byte]0)
                $bw.Write([uint16]1); $bw.Write([uint16]32)
                $bw.Write([uint32]$f.Data.Length); $bw.Write([uint32]$offset)
                $offset += $f.Data.Length
            }
            foreach ($f in $files) { $bw.Write($f.Data) }
            $bw.Flush()
            [System.IO.File]::WriteAllBytes($iconIco, $ms.ToArray())
            $bw.Dispose(); $ms.Dispose()
            Write-Host "已生成 $iconIco"
        }
        finally {
            Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

$rsrc = Get-Command rsrc -ErrorAction SilentlyContinue
if (-not $rsrc) {
    $gopathBin = Join-Path (go env GOPATH) "bin\rsrc.exe"
    if (Test-Path -LiteralPath $gopathBin) {
        $rsrc = Get-Item -LiteralPath $gopathBin
    }
}
if (-not $rsrc) {
    Write-Warning "未找到 rsrc 工具，跳过嵌入资源生成（已有 rsrc.syso 将继续使用）。"
    return
}
if (-not (Test-Path -LiteralPath $iconIco)) {
    Write-Warning "缺少 app.ico，无法生成嵌入资源。"
    return
}
$rsrcArguments = @("-ico", $iconIco)
$manifestPath = Join-Path $PSScriptRoot "app.manifest"
if (Test-Path -LiteralPath $manifestPath) {
    $rsrcArguments += @("-manifest", $manifestPath)
}
& $rsrc.Source @rsrcArguments -arch amd64 -o $rsrcSyso
if ($LASTEXITCODE -ne 0) {
    throw "rsrc 生成嵌入资源失败"
}
Write-Host "已生成 $rsrcSyso（图标 + 清单）"
