# Install mcpit (Windows). Usage in PowerShell:
#   irm https://github.com/jstdlee/mcpit/releases/latest/download/install.ps1 | iex
# Options: $env:MCPIT_VERSION = "v0.3.12"; $env:MCPIT_BIN_DIR = "$env:LOCALAPPDATA\mcpit\bin"
$ErrorActionPreference = "Stop"
$repo = "jstdlee/mcpit"
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$name = "mcpit_windows_$arch"
$base = if ($env:MCPIT_VERSION) { "https://github.com/$repo/releases/download/$($env:MCPIT_VERSION)" } else { "https://github.com/$repo/releases/latest/download" }
$bin = if ($env:MCPIT_BIN_DIR) { $env:MCPIT_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "mcpit\bin" }
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("mcpit-" + [guid]::NewGuid())
New-Item -ItemType Directory -Force -Path $tmp, $bin | Out-Null
try {
  Write-Host "downloading $base/$name.zip"
  Invoke-WebRequest "$base/$name.zip" -OutFile "$tmp\$name.zip"
  Invoke-WebRequest "$base/SHA256SUMS" -OutFile "$tmp\SHA256SUMS"
  $want = ((Get-Content "$tmp\SHA256SUMS") | Where-Object { $_ -match " $name.zip$" }).Split(" ")[0]
  $got = (Get-FileHash "$tmp\$name.zip" -Algorithm SHA256).Hash.ToLower()
  if ($want -ne $got) { throw "mcpit: checksum mismatch" }
  Expand-Archive "$tmp\$name.zip" -DestinationPath $tmp -Force
  Copy-Item "$tmp\$name\mcpit.exe" "$bin\mcpit.exe" -Force
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if (-not ($userPath -split ";" | Where-Object { $_ -eq $bin })) {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$bin", "User")
    Write-Host "added $bin to your user PATH (open a new terminal)"
  }
  & "$bin\mcpit.exe" version
  Write-Host "next: mcpit doctor   then   mcpit setup <claude|codex|cursor|gemini|omp|vscode>"
} finally {
  Remove-Item -Recurse -Force $tmp
}
