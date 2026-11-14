param(
  [Parameter(Mandatory=$true)][ValidateSet('Prepare','Install','Uninstall')][string]$Action,
  [Parameter(Mandatory=$true)][ValidateSet('Desktop','Server')][string]$Mode,
  [Parameter(Mandatory=$true)][string]$InstallDirectory,
  [Parameter(Mandatory=$true)][string]$DataDirectory,
  [Parameter(Mandatory=$true)][string]$Version
)
$ErrorActionPreference = 'Stop'
$ServiceName = 'KI-Basis-Desktop'
$Executable = Join-Path $InstallDirectory 'ki-basis.exe'
$Manager = Join-Path $InstallDirectory 'KI-Basis-Manager.exe'
$SessionPath = Join-Path $DataDirectory '.desktop-session.json'

function Assert-AbsolutePath([string]$Path) {
  if ($Path -notmatch '^[A-Za-z]:[\\/]' -or $Path.TrimEnd('\','/') -match '^[A-Za-z]:$') {
    throw 'Installation requires a local absolute path.'
  }
}
Assert-AbsolutePath $InstallDirectory
Assert-AbsolutePath $DataDirectory
$InstallDirectory = [IO.Path]::GetFullPath($InstallDirectory).TrimEnd('\')
$DataDirectory = [IO.Path]::GetFullPath($DataDirectory).TrimEnd('\')

function Assert-NoReparseAncestors([string]$Path) {
  $candidate = $Path
  while ($candidate) {
    if (Test-Path -LiteralPath $candidate) {
      $item = Get-Item -LiteralPath $candidate -Force
      if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw 'Installation paths must not pass through symbolic links or junctions.'
      }
    }
    $candidate = [IO.Path]::GetDirectoryName($candidate)
  }
}
Assert-NoReparseAncestors $InstallDirectory
Assert-NoReparseAncestors $DataDirectory

function Get-OwnedService {
  $service = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction Stop
  if ($null -eq $service) { return $null }
  # Never stop or remove a different product that happens to use our service name.
  if ($service.PathName -notmatch '^(?:"([^"]+)"|(\S+))\s+service\s+run\s+' -or
      -not [string]::Equals([IO.Path]::GetFullPath($(if ($Matches[1]) { $Matches[1] } else { $Matches[2] })), [IO.Path]::GetFullPath($Executable), [StringComparison]::OrdinalIgnoreCase) -or
      -not [string]::Equals($service.StartName, "NT SERVICE\$ServiceName", [StringComparison]::OrdinalIgnoreCase)) {
    throw 'The KI-Basis service belongs to a different installation. Resolve that installation first.'
  }
  return $service
}

function Stop-OwnedService {
  $owned = Get-OwnedService
  if ($null -eq $owned) { return }
  $service = Get-Service -Name $ServiceName
  if ($service.Status -ne 'Stopped') {
    Stop-Service -Name $ServiceName -ErrorAction Stop
    $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(90))
  }
}

function Invoke-LocalApi([Uri]$Base, [string]$Token, [string]$Path, [string]$Method) {
  if ($Base.Scheme -ne 'http' -or $Base.Host -ne '127.0.0.1' -or
      $Base.AbsolutePath -ne '/' -or $Base.UserInfo -or $Base.Query -or $Base.Fragment -or
      $Token -notmatch '^[a-f0-9]{64}$') {
    throw 'The local setup session is invalid.'
  }
  $request = [Net.HttpWebRequest]::Create([Uri]::new($Base, $Path))
  $request.Method = $Method
  $request.Proxy = $null
  $request.AllowAutoRedirect = $false
  $request.Timeout = 5000
  $request.Headers.Add('X-Desktop-Token', $Token)
  if ($Method -eq 'POST') { $request.ContentLength = 0 }
  $response = $request.GetResponse()
  try {
    $reader = New-Object IO.StreamReader($response.GetResponseStream())
    try {
      $buffer = New-Object char[] 65537
      $count = $reader.ReadBlock($buffer, 0, $buffer.Length)
      if ($count -gt 65536) { throw 'The local setup response is too large.' }
      return [string]::new($buffer, 0, $count) | ConvertFrom-Json
    } finally { $reader.Dispose() }
  } finally { $response.Dispose() }
}

function Stop-OwnedDesktop {
  if (-not (Test-Path -LiteralPath $SessionPath)) { return }
  $session = Get-Content -LiteralPath $SessionPath -Raw | ConvertFrom-Json
  $uri = [Uri]$session.url
  if ($uri.Scheme -ne 'http' -or $uri.Host -ne '127.0.0.1' -or
      $uri.AbsolutePath -ne '/' -or $uri.UserInfo -or $uri.Query -or $uri.Fragment -or
      $session.token -notmatch '^[a-f0-9]{64}$') {
    throw 'The local setup session is invalid; close KI-Basis before continuing.'
  }
  try { $status = Invoke-LocalApi $uri $session.token '/desktop/status' 'GET' }
  catch [Net.WebException] {
    if ($_.Exception.Status -eq [Net.WebExceptionStatus]::ConnectFailure) { return }
    throw 'KI-Basis cannot be contacted safely; close it before continuing.'
  }
  if (-not [string]::Equals($status.data_directory, $DataDirectory, [StringComparison]::OrdinalIgnoreCase)) {
    throw 'A different KI-Basis data directory is running.'
  }
  $process = Get-CimInstance Win32_Process -Filter "ProcessId=$([int]$status.process_id)"
  if ($null -eq $process -or
      -not ([string]::Equals($process.ExecutablePath, $Manager, [StringComparison]::OrdinalIgnoreCase) -or
            [string]::Equals($process.ExecutablePath, $Executable, [StringComparison]::OrdinalIgnoreCase))) {
    throw 'KI-Basis is running from a different folder. Close it before installing here.'
  }
  $processIDToWait = [int]$status.process_id
  $null = Invoke-LocalApi $uri $session.token '/desktop/quit' 'POST'
  # Waiting for the owning process, rather than just its HTTP port, keeps updates
  # from replacing files while a model process or audit shutdown still runs.
  $deadline = [DateTime]::UtcNow.AddSeconds(90)
  while (Get-Process -Id $processIDToWait -ErrorAction SilentlyContinue) {
    if ([DateTime]::UtcNow -ge $deadline) { throw 'KI-Basis did not finish stopping. No program files were replaced.' }
    Start-Sleep -Milliseconds 200
  }
}

function Set-PrivateServerDirectory {
  $inheritance = [Security.AccessControl.InheritanceFlags]'ContainerInherit,ObjectInherit'
  $propagation = [Security.AccessControl.PropagationFlags]::None
  $serviceSid = (New-Object Security.Principal.NTAccount("NT SERVICE\$ServiceName")).Translate([Security.Principal.SecurityIdentifier])
  $currentSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  # Protect the application parent as well: a writable parent could let another
  # local user replace a protected data directory before the service starts.
  foreach ($directory in @([IO.Path]::GetDirectoryName($DataDirectory), $DataDirectory)) {
    if (Test-Path -LiteralPath $directory) {
      $owner = (Get-Acl -LiteralPath $directory).GetOwner([Security.Principal.SecurityIdentifier]).Value
      if ($owner -notin @('S-1-5-18', 'S-1-5-32-544', $serviceSid.Value, $currentSid)) {
        throw 'The server data folder is owned by another account. Move that folder before installing.'
      }
    } else { $null = New-Item -ItemType Directory -Path $directory }
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetAccessRuleProtection($true, $false)
    foreach ($identity in @('S-1-5-18', 'S-1-5-32-544')) {
      $sid = New-Object Security.Principal.SecurityIdentifier($identity)
      $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($sid, 'FullControl', $inheritance, $propagation, 'Allow')))
    }
    $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($serviceSid, 'Modify', $inheritance, $propagation, 'Allow')))
    Set-Acl -LiteralPath $directory -AclObject $acl
  }
}

if ($Action -eq 'Prepare') {
  $versionFile = Join-Path $InstallDirectory 'APP-VERSION.txt'
  if (Test-Path -LiteralPath $versionFile) {
    $installedVersion = [version]((Get-Content -LiteralPath $versionFile -Raw).Trim() -replace '[-+].*$', '')
    $incomingVersion = [version]($Version -replace '[-+].*$', '')
    if ($installedVersion -gt $incomingVersion) { throw 'Downgrading this installation is not supported. Use a current installer.' }
  }
  if ($Mode -eq 'Server') { Stop-OwnedService } else { Stop-OwnedDesktop }
  exit 0
}
if ($Action -eq 'Uninstall') {
  if ($Mode -eq 'Server') {
    Stop-OwnedService
    if ($null -ne (Get-OwnedService)) {
      & "$env:SystemRoot\System32\sc.exe" delete $ServiceName | Out-Null
      if ($LASTEXITCODE -ne 0) { throw 'The KI-Basis service could not be removed.' }
    }
  } else { Stop-OwnedDesktop }
  # Program removal intentionally leaves models, certificates, audit and backups.
  exit 0
}
if ($Mode -ne 'Server') { exit 0 }
$created = $false
try {
  if ($null -eq (Get-OwnedService)) {
    & $Executable service install --name $ServiceName -- desktop --dir $DataDirectory --open-browser=false --quiet-setup --service-mode
    if ($LASTEXITCODE -ne 0) { throw 'The KI-Basis server service could not be installed.' }
    $created = $true
  }
  Set-PrivateServerDirectory
  Start-Service -Name $ServiceName -ErrorAction Stop
  (Get-Service -Name $ServiceName).WaitForStatus('Running', [TimeSpan]::FromSeconds(45))
  $deadline = [DateTime]::UtcNow.AddSeconds(60)
  do {
    if (Test-Path -LiteralPath $SessionPath) {
      $session = Get-Content -LiteralPath $SessionPath -Raw | ConvertFrom-Json
      try {
        $status = Invoke-LocalApi ([Uri]$session.url) $session.token '/desktop/status' 'GET'
        if ($status.data_directory -eq $DataDirectory) { exit 0 }
      } catch { }
    }
    if ((Get-Service -Name $ServiceName).Status -eq 'Stopped') { break }
    Start-Sleep -Milliseconds 500
  } while ([DateTime]::UtcNow -lt $deadline)
  throw 'The server did not become ready. Installation data was preserved.'
} catch {
  # A failed first installation must not leave a reboot-starting broken service.
  if ($created) {
    Stop-Service -Name $ServiceName -ErrorAction SilentlyContinue
    & "$env:SystemRoot\System32\sc.exe" delete $ServiceName | Out-Null
  }
  throw
}
