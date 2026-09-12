# Installs another on Windows.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nxxxsooo/another/main/scripts/install.ps1 | iex"
#
# Honors $env:INSTALL_DIR (default %LOCALAPPDATA%\another) and $env:VERSION
# (default latest), mirroring scripts/install.sh on Unix. `another update`
# re-runs this script with INSTALL_DIR set, so the two have to agree on the
# destination contract.

$ErrorActionPreference = 'Stop'

$Repo = 'nxxxsooo/another'
$InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'another' }
$Version = if ($env:VERSION) { $env:VERSION } else { 'latest' }

# No shell detection here: a custom destination is an explicit caller choice,
# so PATH persistence only applies to the default, exactly like install.sh.
function Ensure-Path {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $entries = $userPath -split ';' | Where-Object { $_ -ne '' }
  if ($entries -contains $InstallDir) { return }
  if ($InstallDir -ne (Join-Path $env:LOCALAPPDATA 'another')) {
    Write-Warning "Add $InstallDir to PATH before running another."
    return
  }
  [Environment]::SetEnvironmentVariable('Path', ($entries + $InstallDir) -join ';', 'User')
  Write-Output "Added $InstallDir to the user PATH. Restart the terminal to use it."
}

switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { $arch = 'amd64' }
  'ARM64' { $arch = 'arm64' }
  default { Write-Error "unsupported arch: $env:PROCESSOR_ARCHITECTURE"; exit 1 }
}

if ($Version -eq 'latest') {
  $Version = (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest").tag_name
}

$bare = $Version.TrimStart('v')
$url = "https://github.com/$Repo/releases/download/$Version/another_${bare}_windows_${arch}.zip"
$tmpdir = Join-Path ([IO.Path]::GetTempPath()) ("another-install-" + [Guid]::NewGuid().ToString('N'))

try {
  Write-Output "Installing another $Version for windows/$arch..."
  New-Item -ItemType Directory -Path $tmpdir | Out-Null
  $zip = Join-Path $tmpdir 'another.zip'
  Invoke-WebRequest $url -OutFile $zip
  Expand-Archive $zip -DestinationPath $tmpdir
  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  Copy-Item (Join-Path $tmpdir 'another.exe') (Join-Path $InstallDir 'another.exe') -Force
  Write-Output "Installed to $(Join-Path $InstallDir 'another.exe')"
} finally {
  Remove-Item $tmpdir -Recurse -Force -ErrorAction SilentlyContinue
}

Ensure-Path
if (($env:Path -split ';') -contains $InstallDir) {
  Write-Output 'Run: another --help'
} else {
  Write-Output "Run now: $(Join-Path $InstallDir 'another.exe') --help"
}
