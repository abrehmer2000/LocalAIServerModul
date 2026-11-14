param([Parameter(Mandatory=$true)][string]$Installer)
$ErrorActionPreference = 'Stop'
$Installer = (Resolve-Path -LiteralPath $Installer).Path
$serviceName = 'KI-Basis-Desktop'
$root = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }

function Assert([bool]$Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
function Run-Setup([string]$Mode, [string]$Directory) {
  $arguments = "/$Mode /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /DIR=`"$Directory`" /LOG=`"$(Join-Path $root "KI-Basis-$Mode-setup.log")`""
  $process = Start-Process -FilePath $Installer -ArgumentList $arguments -Wait -PassThru
  Assert ($process.ExitCode -eq 0) "Installer failed ($Mode), exit $($process.ExitCode)."
}
function Request-Status([string]$Directory) {
  $path = Join-Path $Directory '.desktop-session.json'
  if (-not (Test-Path -LiteralPath $path)) { return $null }
  $session = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
  $request = [Net.HttpWebRequest]::Create($session.url + '/desktop/status')
  $request.Proxy = $null
  $request.Timeout = 3000
  $request.AllowAutoRedirect = $false
  $request.Headers.Add('X-Desktop-Token', $session.token)
  $response = $request.GetResponse()
  try {
    $reader = New-Object IO.StreamReader($response.GetResponseStream())
    try { $status = $reader.ReadToEnd() | ConvertFrom-Json } finally { $reader.Dispose() }
  } finally { $response.Dispose() }
  Assert ($status.data_directory -eq $Directory) 'Wrong installation answered status.'
  return $status
}
function Wait-Ready([string]$Directory) {
  $deadline = [DateTime]::UtcNow.AddSeconds(60)
  do {
    try { $status = Request-Status $Directory; if ($null -ne $status) { return $status } } catch { }
    Start-Sleep -Milliseconds 300
  } while ([DateTime]::UtcNow -lt $deadline)
  throw 'Installed application did not become reachable.'
}
function Data-Fingerprints([string]$Directory) {
  $files = @(Get-ChildItem -LiteralPath $Directory -Recurse -File | Where-Object { $_.Name -in @('config.json','ca.crt') })
  Assert ($files.Count -ge 2) 'No persistent central identity/configuration was created.'
  return @($files | Sort-Object FullName | ForEach-Object { (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash }) -join ','
}
function Uninstall([string]$Directory) {
  $uninstaller = Join-Path $Directory 'unins000.exe'
  $process = Start-Process -FilePath $uninstaller -ArgumentList '/VERYSILENT /SUPPRESSMSGBOXES /NORESTART' -Wait -PassThru
  Assert ($process.ExitCode -eq 0) 'Uninstaller failed.'
  Assert (-not (Test-Path -LiteralPath (Join-Path $Directory 'KI-Basis-Manager.exe'))) 'Program files remain after uninstall.'
}

$desktopInstall = Join-Path $root 'KI-Basis Desktop Smoke'
$desktopData = Join-Path $env:APPDATA 'KI-Basis/desktop'
$serverInstall = Join-Path $env:ProgramFiles 'KI-Basis'
$serverData = Join-Path $env:ProgramData 'KI-Basis/desktop'
Assert (-not (Test-Path -LiteralPath $desktopData)) 'Desktop smoke needs clean runner data.'
Assert (-not (Test-Path -LiteralPath $serverData)) 'Server smoke needs clean runner data.'
Run-Setup 'CURRENTUSER' $desktopInstall
Assert ($null -eq (Get-Service $serviceName -ErrorAction SilentlyContinue)) 'Desktop install created a system service.'
$process = Start-Process -FilePath (Join-Path $desktopInstall 'KI-Basis-Manager.exe') -ArgumentList "desktop --dir `"$desktopData`" --open-browser=false --quiet-setup" -PassThru
$first = Wait-Ready $desktopData
Assert ([int]$first.process_id -eq $process.Id) 'Wrong desktop process owns the session.'
$fingerprints = Data-Fingerprints $desktopData
$reopen = Start-Process -FilePath (Join-Path $desktopInstall 'ki-basis.exe') -ArgumentList "desktop --dir `"$desktopData`" --open-browser=false --quiet-setup" -PassThru -Wait
Assert ($reopen.ExitCode -eq 0) 'Second launch failed to reuse installation.'
Assert ((Request-Status $desktopData).process_id -eq $first.process_id) 'Second launch duplicated the daemon.'
Run-Setup 'CURRENTUSER' $desktopInstall
Assert ($process.WaitForExit(10000)) 'Upgrade did not finish stopping the old process.'
Assert ((Data-Fingerprints $desktopData) -eq $fingerprints) 'Desktop upgrade changed persistent identity.'
$null = Start-Process -FilePath (Join-Path $desktopInstall 'KI-Basis-Manager.exe') -ArgumentList "desktop --dir `"$desktopData`" --open-browser=false --quiet-setup" -PassThru
$null = Wait-Ready $desktopData
Uninstall $desktopInstall
Assert ((Data-Fingerprints $desktopData) -eq $fingerprints) 'Desktop uninstall removed persistent data.'

Run-Setup 'ALLUSERS' $serverInstall
$service = Get-CimInstance Win32_Service -Filter "Name='$serviceName'"
Assert ($service.StartMode -eq 'Auto') 'Server service is not boot enabled.'
Assert ($service.StartName -eq "NT SERVICE\$serviceName") 'Server runs with an unexpected service account.'
$null = Wait-Ready $serverData
$acl = Get-Acl -LiteralPath $serverData
Assert $acl.AreAccessRulesProtected 'Server data inherits broad parent permissions.'
foreach ($rule in $acl.Access) {
  if ($rule.AccessControlType -ne 'Allow') { continue }
  $sid = $rule.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value
  Assert ($sid -notin @('S-1-1-0','S-1-5-11','S-1-5-32-545')) 'Server data is readable by ordinary local users.'
}
$fingerprints = Data-Fingerprints $serverData
Stop-Service $serviceName
(Get-Service $serviceName).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(60))
Start-Service $serviceName
$null = Wait-Ready $serverData
Assert ((Data-Fingerprints $serverData) -eq $fingerprints) 'Service restart changed identity.'
Run-Setup 'ALLUSERS' $serverInstall
$null = Wait-Ready $serverData
Assert ((Data-Fingerprints $serverData) -eq $fingerprints) 'Server upgrade changed identity.'
Uninstall $serverInstall
Assert ($null -eq (Get-Service $serviceName -ErrorAction SilentlyContinue)) 'Server uninstall left its service installed.'
Assert ((Data-Fingerprints $serverData) -eq $fingerprints) 'Server uninstall removed persistent data.'
Write-Output 'PASS: installer modes, desktop reuse, upgrade, service restart/ACL and data-preserving uninstall.'
Write-Output 'Not covered: real model inference, interactive UAC, logout/reboot, signing reputation and target hardware.'
