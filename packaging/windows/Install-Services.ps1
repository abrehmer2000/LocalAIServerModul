param(
  [Parameter(Mandatory=$true)][string]$Executable,
  [Parameter(Mandatory=$true)][string]$CentralDirectory,
  [string]$AgentDirectory
)
$ErrorActionPreference = 'Stop'
$Executable = (Resolve-Path $Executable).Path
$CentralDirectory = (Resolve-Path $CentralDirectory).Path

function Grant-ServiceDirectory([string]$Directory, [string]$ServiceName) {
  $acl = Get-Acl $Directory
  $sid = (New-Object System.Security.Principal.NTAccount("NT SERVICE\$ServiceName")).Translate([System.Security.Principal.SecurityIdentifier])
  $rule = New-Object System.Security.AccessControl.FileSystemAccessRule($sid, 'Modify', 'ContainerInherit,ObjectInherit', 'None', 'Allow')
  $acl.AddAccessRule($rule)
  Set-Acl -LiteralPath $Directory -AclObject $acl
}

& $Executable service install --name KI-Basis-Gateway -- serve --config (Join-Path $CentralDirectory 'config.json')
if ($LASTEXITCODE -ne 0) { throw 'Gateway service installation failed' }
Grant-ServiceDirectory $CentralDirectory 'KI-Basis-Gateway'
if ($AgentDirectory) {
  $AgentDirectory = (Resolve-Path $AgentDirectory).Path
  & $Executable service install --name KI-Basis-Agent -- agent-ui --dir $AgentDirectory --open-browser=false --quiet-setup
  if ($LASTEXITCODE -ne 0) { throw 'Agent service installation failed' }
  Grant-ServiceDirectory $AgentDirectory 'KI-Basis-Agent'
}
# Installation never starts services or reboots. Verify runtime/model read access,
# privacy policy and maintenance/import state before the explicit Start-Service.
Write-Output 'Services installed for automatic boot. Check ACLs and host hardening, then start in the announced maintenance window.'
