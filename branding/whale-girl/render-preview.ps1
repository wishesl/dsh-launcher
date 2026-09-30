# 把 <Name>.svg 渲染成不同尺寸的 PNG 预览（headless Chrome）
# 用法: pwsh -NoProfile -File render-preview.ps1 -Sizes 512,256,128,64,32
param(
  [string]$Sizes = '512,256,128,64,32',
  [string]$Name = 'whale-girl-logo',
  [switch]$Opaque
)
$ErrorActionPreference = 'Stop'
$dir    = $PSScriptRoot
$svg    = Get-Content (Join-Path $dir "$Name.svg") -Raw
$chrome = @(
  "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
  "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe",
  "${env:ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe",
  "$env:ProgramFiles\Microsoft\Edge\Application\msedge.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $chrome) { throw 'Chrome/Edge not found' }

$tmp = Join-Path $dir '_preview'
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
$bg  = if ($Opaque) { '#ffffff' } else { 'transparent' }

foreach ($s in ($Sizes -split '[,\s]+' | Where-Object { $_ } | ForEach-Object { [int]$_.Trim() })) {
  $html = @"
<!doctype html><meta charset="utf-8">
<style>html,body{margin:0;padding:0;background:$bg;overflow:hidden}
svg{display:block;width:${s}px;height:${s}px}</style>
$svg
"@
  $h = Join-Path $tmp ("p{0}.html" -f $s)
  Set-Content -Path $h -Value $html -Encoding UTF8
  $out = Join-Path $dir ("png\{0}-{1}.png" -f $Name, $s)
  New-Item -ItemType Directory -Force -Path (Split-Path $out) | Out-Null
  if (Test-Path $out) { Remove-Item $out }
  & $chrome --headless=new --disable-gpu --disable-extensions --no-first-run --no-default-browser-check `
      --hide-scrollbars --force-device-scale-factor=1 --virtual-time-budget=2000 `
      --window-size="$s,$s" --default-background-color=00000000 `
      --screenshot="$out" ("file:///" + $h.Replace('\', '/')) 2>&1 | Out-Null
  if (-not (Test-Path $out)) { throw "render failed for size $s" }
  "{0,-6} -> {1}" -f $s, (Get-Item $out).FullName
}
