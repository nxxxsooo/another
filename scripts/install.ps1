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
$url = if ($env:ANOTHER_DOWNLOAD_URL) {
  # An explicit URL makes the exact installer testable before a release owns
  # an asset, and supports mirrors without changing the normal trust path.
  $env:ANOTHER_DOWNLOAD_URL
} else {
  "https://github.com/$Repo/releases/download/$Version/another_${bare}_windows_${arch}.zip"
}
$tmpdir = Join-Path ([IO.Path]::GetTempPath()) ("another-install-" + [Guid]::NewGuid().ToString('N'))

try {
  Write-Output "Installing another $Version for windows/$arch..."
  New-Item -ItemType Directory -Path $tmpdir | Out-Null
  $zip = Join-Path $tmpdir 'another.zip'
  Invoke-WebRequest $url -OutFile $zip
  Expand-Archive $zip -DestinationPath $tmpdir
  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  $source = Join-Path $tmpdir 'another.exe'
  $destination = Join-Path $InstallDir 'another.exe'
  $old = $null
  if (Test-Path -LiteralPath $destination) {
    # Windows will not overwrite a running executable, but it does permit a
    # rename. Keep the old image beside the new one until the process whose
    # pid `another update` supplied has exited. A direct installer run has no
    # such process and removes the old image immediately.
    $suffix = if ($env:ANOTHER_UPDATE_PID) { $env:ANOTHER_UPDATE_PID } else { [Guid]::NewGuid().ToString('N') }
    $old = "$destination.old-$suffix"
    Move-Item -LiteralPath $destination -Destination $old -Force
  }
  try {
    Copy-Item -LiteralPath $source -Destination $destination -Force
  } catch {
    if ($old -and -not (Test-Path -LiteralPath $destination)) {
      Move-Item -LiteralPath $old -Destination $destination -Force
    }
    throw
  }
  Write-Output "Installed to $destination"

  if ($old) {
    if ($env:ANOTHER_UPDATE_PID) {
      $quotedOld = $old.Replace("'", "''")
      $cleanup = "Wait-Process -Id $env:ANOTHER_UPDATE_PID -ErrorAction SilentlyContinue; Remove-Item -LiteralPath '$quotedOld' -Force -ErrorAction SilentlyContinue"
      $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($cleanup))
      Start-Process powershell.exe -WindowStyle Hidden -ArgumentList '-NoProfile', '-EncodedCommand', $encoded | Out-Null
    } else {
      Remove-Item -LiteralPath $old -Force
    }
  }
} finally {
  Remove-Item $tmpdir -Recurse -Force -ErrorAction SilentlyContinue
}

Ensure-Path
& (Join-Path $InstallDir 'another.exe') aliases install --shell (Get-Process -Id $PID).Path
if ($LASTEXITCODE -ne 0) {
  Write-Warning 'Optional alias skipped; run another aliases install to retry.'
}
if (($env:Path -split ';') -contains $InstallDir) {
  Write-Output 'Run: another --help'
} else {
  Write-Output "Run now: $(Join-Path $InstallDir 'another.exe') --help"
}
