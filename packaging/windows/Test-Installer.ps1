param([Parameter(Mandatory=$true)][string]$Installer)
$ErrorActionPreference = 'Stop'
$Installer = (Resolve-Path -LiteralPath $Installer).Path
$serviceName = 'KI-Basis-Desktop'
$root = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { [IO.Path]::GetTempPath() }

function Assert([bool]$Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
function Run-Setup([string]$Mode, [string]$Directory, [string]$Tasks = '') {
  $arguments = "/$Mode /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /DIR=`"$Directory`" /TASKS=`"$Tasks`" /LOG=`"$(Join-Path $root "KI-Basis-$Mode-setup.log")`""
  $process = Start-Process -FilePath $Installer -ArgumentList $arguments -Wait -PassThru
  if ($process.ExitCode -ne 0) { Get-Content -LiteralPath (Join-Path $root "KI-Basis-$Mode-setup.log") -Tail 100 }
  Assert ($process.ExitCode -eq 0) "Installer failed ($Mode), exit $($process.ExitCode)."
}
function Request-Desktop([string]$Directory, [string]$Path, [ValidateSet('GET','POST')][string]$Method = 'GET') {
  $sessionFile = Join-Path $Directory '.desktop-session.json'
  if (-not (Test-Path -LiteralPath $sessionFile)) { return $null }
  $session = Get-Content -LiteralPath $sessionFile -Raw | ConvertFrom-Json
  $base = [Uri]$session.url
  if ($base.Scheme -ne 'http' -or $base.Host -ne '127.0.0.1' -or
      $base.UserInfo -or $base.Query -or $base.Fragment -or $base.AbsolutePath -ne '/' -or
      $session.token -notmatch '^[a-f0-9]{64}$') { throw 'Invalid private desktop session.' }
  if ($Path -notin @('/desktop/status','/desktop/prepare')) { throw 'Unexpected desktop test endpoint.' }
  $request = [Net.HttpWebRequest]::Create([Uri]::new($base, $Path))
  $request.Method = $Method
  $request.Proxy = $null
  $request.Timeout = 5000
  $request.AllowAutoRedirect = $false
  $request.Headers.Add('X-Desktop-Token', $session.token)
  if ($Method -eq 'POST') { $request.ContentLength = 0 }
  $response = $request.GetResponse()
  try {
    $reader = New-Object IO.StreamReader($response.GetResponseStream())
    try {
      $buffer = New-Object char[] 1048577
      $count = $reader.ReadBlock($buffer, 0, $buffer.Length)
      if ($count -gt 1048576) { throw 'Desktop test response exceeded its limit.' }
      return [string]::new($buffer, 0, $count) | ConvertFrom-Json
    } finally { $reader.Dispose() }
  } finally { $response.Dispose() }
}
function Request-Status([string]$Directory) {
  $status = Request-Desktop $Directory '/desktop/status'
  if ($null -eq $status) { return $null }
  Assert ($status.data_directory -eq $Directory) 'Wrong installation answered status.'
  return $status
}
function Prepare-WindowsRuntime([string]$Directory) {
  $accepted = Request-Desktop $Directory '/desktop/prepare' 'POST'
  Assert ($accepted.accepted -eq $true) 'Desktop runtime preparation was not accepted.'
  $deadline = [DateTime]::UtcNow.AddSeconds(180)
  do {
    $status = Request-Status $Directory
    if ($null -ne $status) {
      if ($status.error) {
        $code = if ($status.error -match '^[a-z0-9_]{1,80}$') { $status.error } else { 'unexpected_error' }
        throw "Pinned Windows runtime preparation failed: $code."
      }
      if (-not $status.busy -and $status.connected -and $status.runtime.installed) { return }
    }
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw 'Pinned Windows runtime preparation did not finish within 180 seconds.'
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
  $files = @(Get-ChildItem -LiteralPath $Directory -Recurse -File | Where-Object { $_.Name -in @('config.json','ca.crt','agent.json','llama-server.exe') })
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
$unsafeServerInstall = Join-Path $root 'KI-Basis Unsafe Server'
$rejected = Start-Process -FilePath $Installer -ArgumentList "/ALLUSERS /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /DIR=`"$unsafeServerInstall`"" -Wait -PassThru
Assert ($rejected.ExitCode -ne 0) 'Server installer accepted an executable directory outside Program Files.'
Assert (-not (Test-Path -LiteralPath (Join-Path $unsafeServerInstall 'ki-basis.exe'))) 'Rejected server install copied executable files.'
Assert ($null -eq (Get-Service $serviceName -ErrorAction SilentlyContinue)) 'Rejected server install created its service.'
$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
Run-Setup 'CURRENTUSER' $desktopInstall 'startonlogin'
Assert ((Get-ItemPropertyValue -LiteralPath $runKey -Name 'KI-Basis-Desktop').Contains($desktopInstall)) 'Selected autostart was not registered.'
Assert ($null -eq (Get-Service $serviceName -ErrorAction SilentlyContinue)) 'Desktop install created a system service.'
$process = Start-Process -FilePath (Join-Path $desktopInstall 'KI-Basis-Manager.exe') -ArgumentList "desktop --dir `"$desktopData`" --open-browser=false --quiet-setup" -PassThru
$first = Wait-Ready $desktopData
Assert ([int]$first.process_id -eq $process.Id) 'Wrong desktop process owns the session.'
Prepare-WindowsRuntime $desktopData
$agentIdentity = Join-Path $desktopData 'agent/agent.json'
Assert (Test-Path -LiteralPath $agentIdentity) 'Automatic local enrollment did not persist its agent identity.'
$runtimeSettings = Get-Content -LiteralPath (Join-Path $desktopData 'agent/settings.json') -Raw | ConvertFrom-Json
Assert (Test-Path -LiteralPath $runtimeSettings.binary) 'Verified runtime executable was not retained.'
Assert ((Get-FileHash -LiteralPath $runtimeSettings.binary -Algorithm SHA256).Hash -eq $runtimeSettings.runtime.engine_sha256) 'Persisted runtime pin does not match the verified executable.'
Write-Output 'PASS: pinned Windows CPU runtime downloaded, verified/probed and local agent enrolled without model weights.'
$fingerprints = Data-Fingerprints $desktopData
$reopen = Start-Process -FilePath (Join-Path $desktopInstall 'ki-basis.exe') -ArgumentList "desktop --dir `"$desktopData`" --open-browser=false --quiet-setup" -PassThru -Wait
Assert ($reopen.ExitCode -eq 0) 'Second launch failed to reuse installation.'
Assert ((Request-Status $desktopData).process_id -eq $first.process_id) 'Second launch duplicated the daemon.'
Run-Setup 'CURRENTUSER' $desktopInstall
Assert ($null -eq (Get-ItemProperty -LiteralPath $runKey -Name 'KI-Basis-Desktop' -ErrorAction SilentlyContinue)) 'Unchecking autostart during upgrade left its registration enabled.'
Assert ($process.WaitForExit(10000)) 'Upgrade did not finish stopping the old process.'
Assert ((Data-Fingerprints $desktopData) -eq $fingerprints) 'Desktop upgrade changed persistent identity.'
$otherRegistration = '"C:\Other Installation\KI-Basis-Manager.exe" desktop --open-browser=false'
Set-ItemProperty -LiteralPath $runKey -Name 'KI-Basis-Desktop' -Value $otherRegistration
try {
  & (Join-Path $desktopInstall 'Manage-Installation.ps1') -Action AutostartOff -Mode Desktop -InstallDirectory $desktopInstall -DataDirectory $desktopData -Version '0.3.0'
  Assert ((Get-ItemPropertyValue -LiteralPath $runKey -Name 'KI-Basis-Desktop') -eq $otherRegistration) 'Autostart removal changed another installation registration.'
} finally { Remove-ItemProperty -LiteralPath $runKey -Name 'KI-Basis-Desktop' -ErrorAction SilentlyContinue }
Run-Setup 'CURRENTUSER' $desktopInstall 'startonlogin'
$null = Start-Process -FilePath (Join-Path $desktopInstall 'KI-Basis-Manager.exe') -ArgumentList "desktop --dir `"$desktopData`" --open-browser=false --quiet-setup" -PassThru
$null = Wait-Ready $desktopData
Uninstall $desktopInstall
Assert ($null -eq (Get-ItemProperty -LiteralPath $runKey -Name 'KI-Basis-Desktop' -ErrorAction SilentlyContinue)) 'Desktop uninstall left its own autostart registration.'
Assert ((Data-Fingerprints $desktopData) -eq $fingerprints) 'Desktop uninstall removed persistent data.'

$sentinelDirectory = Join-Path $serverData 'central'
$sentinel = Join-Path $sentinelDirectory 'config.json'
$null = New-Item -ItemType Directory -Path $sentinelDirectory -Force
[IO.File]::WriteAllText($sentinel, 'deliberately invalid test configuration')
$sentinelHash = (Get-FileHash -LiteralPath $sentinel -Algorithm SHA256).Hash
$failedSetupLog = Join-Path $root 'KI-Basis-FAILED-SERVER-setup.log'
$failedSetup = Start-Process -FilePath $Installer -ArgumentList "/ALLUSERS /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /DIR=`"$serverInstall`" /LOG=`"$failedSetupLog`"" -Wait -PassThru
Assert ($failedSetup.ExitCode -ne 0) 'Server setup reported success after a service startup failure.'
Assert ((Get-FileHash -LiteralPath $sentinel -Algorithm SHA256).Hash -eq $sentinelHash) 'Failed server setup modified existing configuration.'
Assert ($null -eq (Get-Service $serviceName -ErrorAction SilentlyContinue)) 'Failed first setup left a broken boot-enabled service.'
# This fixture was created above after requiring clean data. Delete only known
# fixture files and then require empty directories before the normal repair run.
Remove-Item -LiteralPath $sentinel
$fixtureLock = Join-Path $serverData '.desktop.lock'
if (Test-Path -LiteralPath $fixtureLock) { Remove-Item -LiteralPath $fixtureLock -Force }
[IO.Directory]::Delete($sentinelDirectory, $false)
[IO.Directory]::Delete($serverData, $false)
Run-Setup 'ALLUSERS' $serverInstall
$service = Get-CimInstance Win32_Service -Filter "Name='$serviceName'"
if ($null -eq $service -or $service.StartMode -ne 'Auto') {
  if ($null -eq $service) { Write-Output 'Expected KI-Basis service is absent.' }
  else { $service | Select-Object Name,StartMode,State | Format-List }
  Get-Content -LiteralPath (Join-Path $root 'KI-Basis-ALLUSERS-setup.log') -Tail 100
}
Assert ($null -ne $service) 'Server setup did not leave its expected service installed.'
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
