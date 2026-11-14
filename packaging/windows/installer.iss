#ifndef AppVersion
  #define AppVersion "0.3.0"
#endif
#ifndef SourceDir
  #define SourceDir "..\..\work\windows-installer\payload"
#endif
#ifndef OutputDirectory
  #define OutputDirectory "..\..\work\windows-installer\output"
#endif

[Setup]
AppId={code:InstallationId}
AppName=KI-Basis
AppVersion={#AppVersion}
AppPublisher=KI-Basis
AppPublisherURL=https://github.com/abrehmer2000/LocalAIServerModul
AppSupportURL=https://github.com/abrehmer2000/LocalAIServerModul/issues
DefaultDirName={autopf}\KI-Basis
DefaultGroupName=KI-Basis
UsePreviousAppDir=no
UsePreviousGroup=no
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog commandline
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.19045
WizardStyle=modern
DisableProgramGroupPage=yes
DisableWelcomePage=no
UninstallDisplayName={code:InstallationName}
UninstallDisplayIcon={app}\KI-Basis-Manager.exe
OutputDir={#OutputDirectory}
OutputBaseFilename=KI-Basis-{#AppVersion}-windows-x64-setup
Compression=lzma2
SolidCompression=yes
CloseApplications=no
RestartApplications=no
SetupLogging=yes
#ifdef SignedBuild
SignTool=kibasis
SignedUninstaller=yes
#else
AppComments=Unsignierter Testbuild; die Herausgeberidentitaet wurde nicht zertifiziert.
#endif

[Languages]
Name: german; MessagesFile: compiler:Languages\German.isl
Name: english; MessagesFile: compiler:Default.isl

[Messages]
german.WelcomeLabel2=Dieses Setup installiert KI-Basis ohne zusätzliche Entwicklertools. Modelle und Laufzeiten werden erst nach Ihrer Bestätigung in der Anwendung geladen.%n%nDesktopbetrieb läuft unter Ihrem Benutzerkonto. Serverbetrieb benötigt Administratorrechte und läuft auch ohne Anmeldung.%n%nBei Updates und Deinstallation bleiben Modelle, Zertifikate und gespeicherte Daten erhalten.
english.WelcomeLabel2=This setup installs KI-Basis without developer tools. Models and runtimes download only after confirmation inside the application.%n%nDesktop mode runs under your account. Server mode requires administrator privileges and continues without login.%n%nUpdates and uninstall preserve models, certificates and saved data.
german.PrivilegesRequiredOverrideTitle=KI-Basis installieren
german.PrivilegesRequiredOverrideInstruction=Wie soll KI-Basis laufen?
german.PrivilegesRequiredOverrideAllUsers=Serverbetrieb für alle Benutzer (Administratorrechte erforderlich)
german.PrivilegesRequiredOverrideCurrentUser=Desktopbetrieb nur für mich
german.PrivilegesRequiredOverrideCurrentUserRecommended=Desktopbetrieb nur für mich (empfohlen)
english.PrivilegesRequiredOverrideTitle=Install KI-Basis
english.PrivilegesRequiredOverrideInstruction=How should KI-Basis run?
english.PrivilegesRequiredOverrideAllUsers=Unattended server for all users (requires administrator privileges)
english.PrivilegesRequiredOverrideCurrentUser=Desktop for me only
english.PrivilegesRequiredOverrideCurrentUserRecommended=Desktop for me only (recommended)

[Tasks]
Name: startonlogin; Description: KI-Basis bei meiner Windows-Anmeldung im Hintergrund starten; Check: IsDesktopMode; Flags: unchecked

[Files]
Source: {#SourceDir}\KI-Basis-Manager.exe; DestDir: {app}; Flags: ignoreversion
Source: {#SourceDir}\KI-Basis-Server.exe; DestDir: {app}; Flags: ignoreversion
Source: {#SourceDir}\ki-basis.exe; DestDir: {app}; Flags: ignoreversion
Source: {#SourceDir}\THIRD-PARTY-NOTICES.txt; DestDir: {app}; Flags: ignoreversion
Source: {#SourceDir}\BUILD-COMMIT.txt; DestDir: {app}; Flags: ignoreversion
Source: {#SourceDir}\APP-VERSION.txt; DestDir: {app}; Flags: ignoreversion
Source: {#SourceDir}\INSTALLATION.md; DestDir: {app}; Flags: ignoreversion
Source: Manage-Installation.ps1; DestDir: {app}; Flags: ignoreversion
Source: Manage-Installation.ps1; Flags: dontcopy

[Icons]
Name: {group}\KI-Basis; Filename: {app}\KI-Basis-Manager.exe; Parameters: desktop --dir "{userappdata}\KI-Basis\desktop"; WorkingDir: {app}; Check: IsDesktopMode
Name: {group}\KI-Basis Server verwalten; Filename: {app}\KI-Basis-Server.exe; WorkingDir: {app}; Check: IsServerMode
Name: {group}\KI-Basis deinstallieren; Filename: {uninstallexe}

[Registry]
Root: HKCU; Subkey: Software\Microsoft\Windows\CurrentVersion\Run; ValueType: string; ValueName: KI-Basis-Desktop; ValueData: """{app}\KI-Basis-Manager.exe"" desktop --dir ""{userappdata}\KI-Basis\desktop"" --open-browser=false --quiet-setup"; Tasks: startonlogin; Check: IsDesktopMode; Flags: uninsdeletevalue

[Run]
Filename: {app}\KI-Basis-Manager.exe; Parameters: desktop --dir "{userappdata}\KI-Basis\desktop"; Description: KI-Basis öffnen; Check: IsDesktopMode; Flags: nowait postinstall skipifsilent runasoriginaluser
Filename: {app}\KI-Basis-Server.exe; Description: KI-Basis Serververwaltung öffnen; Check: IsServerMode; Flags: nowait postinstall skipifsilent

[Code]
function IsServerMode: Boolean;
begin
  Result := IsAdminInstallMode;
end;

function IsDesktopMode: Boolean;
begin
  Result := not IsServerMode;
end;

function InstallationId(Param: String): String;
begin
  if IsServerMode then Result := 'KI-Basis.Server'
  else Result := 'KI-Basis.Desktop';
end;

function InstallationName(Param: String): String;
begin
  if IsServerMode then Result := 'KI-Basis (Server)'
  else Result := 'KI-Basis (Desktop)';
end;

function InstallationDataDirectory: String;
begin
  if IsServerMode then Result := ExpandConstant('{commonappdata}\KI-Basis\desktop')
  else Result := ExpandConstant('{userappdata}\KI-Basis\desktop');
end;

function InstallationMode: String;
begin
  if IsServerMode then Result := 'Server' else Result := 'Desktop';
end;

function ManageInstallation(Script, Action: String): Boolean;
var
  ExitCode: Integer;
  Parameters: String;
begin
  Parameters := '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File ' + AddQuotes(Script) +
    ' -Action ' + Action + ' -Mode ' + InstallationMode +
    ' -InstallDirectory ' + AddQuotes(ExpandConstant('{app}')) +
    ' -DataDirectory ' + AddQuotes(InstallationDataDirectory) + ' -Version ' + AddQuotes('{#AppVersion}');
  Result := Exec(ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'), Parameters,
    '', SW_HIDE, ewWaitUntilTerminated, ExitCode);
  Result := Result and (ExitCode = 0);
  if not Result then Log('KI-Basis lifecycle action failed: ' + Action + ', exit ' + IntToStr(ExitCode));
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  if IsServerMode and (CompareText(RemoveBackslashUnlessRoot(ExpandConstant('{app}')),
      ExpandConstant('{autopf}\KI-Basis')) <> 0) then begin
    Result := 'Serverbetrieb wird nur im geschützten Standardordner unter Programme unterstützt. Bitte den Standardordner verwenden.';
    Exit;
  end;
  ExtractTemporaryFile('Manage-Installation.ps1');
  if not ManageInstallation(ExpandConstant('{tmp}\Manage-Installation.ps1'), 'Prepare') then
    Result := 'KI-Basis konnte nicht sicher beendet werden. Bitte die vorhandene KI-Basis-Installation schließen und erneut versuchen. Es werden keine laufenden Programme erzwungen beendet.'
  else Result := '';
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if (CurStep = ssPostInstall) and IsServerMode then
    if not ManageInstallation(ExpandConstant('{app}\Manage-Installation.ps1'), 'Install') then
      RaiseException('Der KI-Basis-Dienst konnte nicht gestartet werden. Daten wurden erhalten. Bitte das Installationsprotokoll prüfen.');
end;

function InitializeUninstall: Boolean;
begin
  Result := ManageInstallation(ExpandConstant('{app}\Manage-Installation.ps1'), 'Uninstall');
  if not Result then
    MsgBox('KI-Basis konnte nicht sicher beendet werden. Die Deinstallation wurde abgebrochen; Programme und Daten bleiben erhalten.', mbError, MB_OK);
end;
