; The per-user Windows installer: it installs bundle.sh's output under the user's own
; Programs folder, so it needs no administrator rights. Build it with Inno Setup 6:
;
;   iscc /DAppVersion=1.2.3 /DBundleDir=C:\path\to\bundle /OC:\path\to\output tmux-tabbed-terminal.iss

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef BundleDir
  #define BundleDir "bundle"
#endif

[Setup]
AppId={{4828F54D-63F4-44BA-9E37-4128A522CF02}
AppName=tmux-tabbed-terminal
AppVersion={#AppVersion}
AppPublisher=Will Rouesnel
AppPublisherURL=https://github.com/wrouesnel/tmux-tabbed-terminal
AppSupportURL=https://github.com/wrouesnel/tmux-tabbed-terminal/issues
LicenseFile={#BundleDir}\licenses\tmux-tabbed-terminal.txt
DefaultDirName={autopf}\tmux-tabbed-terminal
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputBaseFilename=tmux-tabbed-terminal-{#AppVersion}-windows-amd64-setup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\bin\tmux-tabbed-terminal.exe
UninstallDisplayName=tmux-tabbed-terminal
; Files in use, such as a running tmux server's, are closed with the user's agreement.
CloseApplications=yes

[Tasks]
Name: desktopicon; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#BundleDir}\*"; DestDir: "{app}"; Flags: recursesubdirs createallsubdirs ignoreversion

[Dirs]
; tmux's sockets go here.
Name: "{app}\msys\tmp"

[Icons]
Name: "{autoprograms}\tmux-tabbed-terminal"; Filename: "{app}\bin\tmux-tabbed-terminal.exe"
Name: "{autodesktop}\tmux-tabbed-terminal"; Filename: "{app}\bin\tmux-tabbed-terminal.exe"; Tasks: desktopicon

[Run]
Filename: "{app}\bin\tmux-tabbed-terminal.exe"; Description: "{cm:LaunchProgram,tmux-tabbed-terminal}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; The bundled tmux server, and its sessions, go with the program.
Filename: "{app}\msys\usr\bin\tmux.exe"; Parameters: "kill-server"; Flags: runhidden; RunOnceId: "KillTmux"

[UninstallDelete]
Type: filesandordirs; Name: "{app}\msys\tmp"
