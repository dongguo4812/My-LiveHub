#define RepoRoot GetEnv("GITHUB_WORKSPACE")
#define PackageDir RepoRoot + "\app\bin\package"
#define IconPath RepoRoot + "\app\build\icon.ico"
#define AppVersion GetEnv("APP_VERSION")

[Setup]
AppId={{7C2B5A42-7B9D-4C68-9E43-7E6B5C7E1A21}
AppName=My-LiveHub
AppVersion={#AppVersion}
AppPublisher=My-LiveHub
AppPublisherURL=https://github.com/dongguo4812/My-LiveHub
DefaultDirName={localappdata}\Programs\My-LiveHub
DefaultGroupName=My-LiveHub
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
WizardStyle=modern
SetupIconFile={#IconPath}
UninstallDisplayIcon={app}\My-LiveHub.exe
OutputDir={#RepoRoot}\app\bin
OutputBaseFilename=My-LiveHub-Setup
Compression=lzma2
SolidCompression=yes
CloseApplications=yes
RestartApplications=no
Uninstallable=yes
VersionInfoVersion={#AppVersion}
VersionInfoProductName=My-LiveHub
VersionInfoDescription=My-LiveHub Windows Installer

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加快捷方式："; Flags: unchecked

[Dirs]
Name: "{app}\config\local"; Flags: uninsneveruninstall
Name: "{app}\cache"; Flags: uninsneveruninstall
Name: "{app}\logs"; Flags: uninsneveruninstall
Name: "{app}\secure"; Flags: uninsneveruninstall
Name: "{app}\extensions\lol_hex_display\data"; Flags: uninsneveruninstall
Name: "{app}\extensions\lol_hex_display\screenshots"; Flags: uninsneveruninstall
Name: "{app}\extensions\lol_hex_display\logs"; Flags: uninsneveruninstall
Name: "{app}\extensions\lol_hex_display\dist\lol_hex_display\logs"; Flags: uninsneveruninstall

[Files]
Source: "{#PackageDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{autoprograms}\My-LiveHub"; Filename: "{app}\My-LiveHub.exe"
Name: "{autodesktop}\My-LiveHub"; Filename: "{app}\My-LiveHub.exe"; Tasks: desktopicon

[Run]
Filename: "{app}\My-LiveHub.exe"; Description: "启动 My-LiveHub"; Flags: nowait postinstall skipifsilent

[Code]
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    SaveStringToFile(ExpandConstant('{app}\\.my-livehub-installed'), 'My-LiveHub installer package', False);
end;
