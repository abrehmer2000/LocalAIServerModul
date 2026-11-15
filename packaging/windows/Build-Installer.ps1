param(
  [string]$Version = '0.3.0',
  [string]$OutputDirectory = 'work/windows-installer',
  [string]$CertificateThumbprint,
  [string]$TimestampUrl = 'http://timestamp.digicert.com'
)
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT') { throw 'Build this installer on Windows.' }
if ($Version -notmatch '^\d+\.\d+\.\d+([-.][a-zA-Z0-9.]+)?$') { throw 'Invalid application version.' }
$Repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
Push-Location $Repository
try {
  $OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
  $Payload = Join-Path $OutputDirectory 'payload'
  $Output = Join-Path $OutputDirectory 'output'
  $null = New-Item -ItemType Directory -Path $Payload,$Output -Force
  $compiler = Get-Command ISCC.exe -ErrorAction SilentlyContinue
  if ($null -eq $compiler) {
    $candidate = Join-Path ${env:ProgramFiles(x86)} 'Inno Setup 6/ISCC.exe'
    if (-not (Test-Path -LiteralPath $candidate)) { throw 'Install Inno Setup 6 before building this installer.' }
    $compiler = Get-Item -LiteralPath $candidate
  }
  foreach ($target in @(
    @{ Name='ki-basis.exe'; Package='./cmd/ki-basis'; GUI=$false },
    @{ Name='KI-Basis-Manager.exe'; Package='./cmd/ki-basis'; GUI=$true },
    @{ Name='KI-Basis-Server.exe'; Package='./cmd/ki-basis-server-launcher'; GUI=$true }
  )) {
    $arguments = @('build','-trimpath')
    if ($target.GUI) { $arguments += '-ldflags=-H=windowsgui' }
    $arguments += @('-o',(Join-Path $Payload $target.Name),$target.Package)
    & go @arguments
    if ($LASTEXITCODE -ne 0) { throw "Build failed: $($target.Name)" }
  }
  & python scripts/collect_licenses.py --output (Join-Path $Payload 'THIRD-PARTY-NOTICES.txt')
  if ($LASTEXITCODE -ne 0) { throw 'License collection failed.' }
  $commit = & git rev-parse HEAD
  if ($LASTEXITCODE -ne 0) { throw 'Cannot identify source revision.' }
  [IO.File]::WriteAllText((Join-Path $Payload 'BUILD-COMMIT.txt'), $commit.Trim() + "`n")
  [IO.File]::WriteAllText((Join-Path $Payload 'APP-VERSION.txt'), $Version + "`n")
  Copy-Item -LiteralPath 'INSTALLATION.md' -Destination $Payload
  $arguments = @("/DAppVersion=$Version", "/DSourceDir=$Payload", "/DOutputDirectory=$Output")
  if ($CertificateThumbprint) {
    if ($CertificateThumbprint -notmatch '^[0-9a-fA-F]{40}$' -or $TimestampUrl -notmatch '^https?://[a-zA-Z0-9./_-]+$') {
      throw 'Invalid signing certificate or timestamp URL.'
    }
    $signtool = (Get-Command signtool.exe -ErrorAction Stop).Source
    foreach ($file in Get-ChildItem -LiteralPath $Payload -Filter '*.exe') {
      & $signtool sign /sha1 $CertificateThumbprint /fd SHA256 /tr $TimestampUrl /td SHA256 $file.FullName
      if ($LASTEXITCODE -ne 0) { throw "Signing failed: $($file.Name)" }
    }
    $signCommand = '"' + $signtool + '" sign /sha1 ' + $CertificateThumbprint + ' /fd SHA256 /tr ' + $TimestampUrl + ' /td SHA256 $f'
    $arguments += @('/DSignedBuild=1', "/Skibasis=$signCommand")
  } else { Write-Warning 'Building an UNSIGNED test installer. Do not describe it as a verified publisher build.' }
  $compilerPath = if ($compiler.Source) { $compiler.Source } else { $compiler.FullName }
  & $compilerPath @arguments (Join-Path $PSScriptRoot 'installer.iss')
  if ($LASTEXITCODE -ne 0) { throw 'Inno Setup compilation failed.' }
  $installers = @(Get-Item -LiteralPath (Join-Path $Output "KI-Basis-$Version-windows-x64-setup.exe"))
  if ($CertificateThumbprint) {
    foreach ($file in $installers) {
      & $signtool verify /pa $file.FullName
      if ($LASTEXITCODE -ne 0) { throw 'Installer signature verification failed.' }
    }
  }
  $hashes = $installers | ForEach-Object {
    $hash = Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256
    $hash.Hash.ToLowerInvariant() + '  ' + $_.Name
  }
  [IO.File]::WriteAllLines((Join-Path $Output 'SHA256SUMS.txt'), [string[]]$hashes)
} finally { Pop-Location }
