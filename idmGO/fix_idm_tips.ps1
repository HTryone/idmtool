$ErrorActionPreference = 'Stop'
$dir  = 'C:\Program Files (x86)\Internet Download Manager\Languages'
$file = Join-Path $dir 'tips_chn.txt'
$bak  = Join-Path $dir 'tips_chn.txt.gbk.bak'

if (-not (Test-Path $file)) { throw "找不到 $file" }

# 1) 备份原 GBK 文件（仅一次）
if (-not (Test-Path $bak)) {
    Copy-Item -LiteralPath $file -Destination $bak -Force
    Write-Host "[1/3] 已备份原文件 -> $bak"
} else {
    Write-Host "[1/3] 备份已存在，跳过：$bak"
}

# 2) GBK(936) -> UTF-8（保留原换行；无 BOM，IDM 按 UTF-8 读）
$gbk     = [Text.Encoding]::GetEncoding(936)
$content = [IO.File]::ReadAllText($file, $gbk)
[IO.File]::WriteAllText($file, $content, (New-Object Text.UTF8Encoding($false)))
Write-Host "[2/3] 已转码为 UTF-8：$file"

# 3) 验证：按 UTF-8 读回应为正常中文
$check = [IO.File]::ReadAllText($file, [Text.Encoding]::UTF8)
if ($check.StartsWith('小提示')) {
    Write-Host "[3/3] 验证通过：$($check.Substring(0, 12))..."
} else {
    Write-Warning "[3/3] 验证异常，开头不是预期的中文：$($check.Substring(0, [Math]::Min(30, $check.Length)))"
}

# 顺带检查其他中文提示文件（tips_cht.txt 是繁体，可能同为 GBK/Big5）
foreach ($name in @('tips_cht.txt')) {
    $p = Join-Path $dir $name
    if (Test-Path $p) {
        $b = [IO.File]::ReadAllBytes($p)
        $asU8 = [Text.Encoding]::UTF8.GetString($b)
        if ($asU8.Contains([char]0xFFFD)) {
            Write-Host "[附加] $name 疑似非 UTF-8，可另行转码"
        }
    }
}
