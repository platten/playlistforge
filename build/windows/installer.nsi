Unicode true

!ifndef APP_VERSION
  !error "APP_VERSION is required"
!endif
!ifndef APP_ARCH
  !error "APP_ARCH is required"
!endif
!ifndef APP_BINARY
  !error "APP_BINARY is required"
!endif
!ifndef APP_ICON
  !error "APP_ICON is required"
!endif
!ifndef WEBVIEW_BOOTSTRAPPER
  !error "WEBVIEW_BOOTSTRAPPER is required"
!endif
!ifndef OUTPUT_FILE
  !error "OUTPUT_FILE is required"
!endif

!define PRODUCT_NAME "Playlist Forge"
!define PRODUCT_PUBLISHER "Playlist Forge"
!define PRODUCT_EXE "playlist-forge.exe"
; Match the uninstall identity and install location produced by the former
; Wails v2 NSIS package so upgrades replace it cleanly.
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Playlist ForgePlaylist Forge"

VIProductVersion "${APP_VERSION}.0"
VIFileVersion "${APP_VERSION}.0"
VIAddVersionKey "CompanyName" "${PRODUCT_PUBLISHER}"
VIAddVersionKey "FileDescription" "${PRODUCT_NAME} Installer"
VIAddVersionKey "FileVersion" "${APP_VERSION}"
VIAddVersionKey "LegalCopyright" "Copyright 2026 Paul Pietkiewicz"
VIAddVersionKey "ProductName" "${PRODUCT_NAME}"
VIAddVersionKey "ProductVersion" "${APP_VERSION}"

ManifestDPIAware true
RequestExecutionLevel admin
SetCompressor /SOLID lzma

!include "FileFunc.nsh"
!include "LogicLib.nsh"
!include "MUI2.nsh"
!include "WinVer.nsh"
!include "x64.nsh"

!define MUI_ABORTWARNING
!define MUI_ICON "${APP_ICON}"
!define MUI_UNICON "${APP_ICON}"
!define MUI_FINISHPAGE_NOAUTOCLOSE

Name "${PRODUCT_NAME}"
BrandingText "${PRODUCT_NAME}"
OutFile "${OUTPUT_FILE}"
InstallDir "$PROGRAMFILES64\${PRODUCT_PUBLISHER}\${PRODUCT_NAME}"
InstallDirRegKey HKLM "${UNINSTALL_KEY}" "InstallLocation"
ShowInstDetails show
ShowUninstDetails show

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  ${Unless} ${AtLeastWin10}
    IfSilent unsupported_os
    MessageBox MB_OK|MB_ICONSTOP "${PRODUCT_NAME} requires Windows 10 or later."
    unsupported_os:
      SetErrorLevel 64
      Quit
  ${EndUnless}

  !if "${APP_ARCH}" == "amd64"
    ${Unless} ${IsNativeAMD64}
      IfSilent unsupported_arch
      MessageBox MB_OK|MB_ICONSTOP "This installer requires 64-bit Windows on an Intel or AMD processor."
      unsupported_arch:
        SetErrorLevel 65
        Quit
    ${EndUnless}
  !else if "${APP_ARCH}" == "arm64"
    ${Unless} ${IsNativeARM64}
      IfSilent unsupported_arch
      MessageBox MB_OK|MB_ICONSTOP "This installer requires Windows on an ARM64 processor."
      unsupported_arch:
        SetErrorLevel 65
        Quit
    ${EndUnless}
  !else
    !error "APP_ARCH must be amd64 or arm64"
  !endif
FunctionEnd

Section "Install"
  SetShellVarContext all
  SetRegView 64

  ; The evergreen bootstrapper is Microsoft-signed and installs silently. It
  ; exits quickly when a current WebView2 runtime is already present.
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "/oname=MicrosoftEdgeWebview2Setup.exe" "${WEBVIEW_BOOTSTRAPPER}"
  DetailPrint "Ensuring the Microsoft Edge WebView2 Runtime is available..."
  ExecWait '"$PLUGINSDIR\MicrosoftEdgeWebview2Setup.exe" /silent /install' $0
  ${If} $0 != 0
  ${AndIf} $0 != 3010
    DetailPrint "WebView2 installation failed with exit code $0."
    SetErrorLevel $0
    Abort
  ${EndIf}

  SetOutPath "$INSTDIR"
  SetOverwrite on
  File "/oname=${PRODUCT_EXE}" "${APP_BINARY}"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  CreateDirectory "$SMPROGRAMS\${PRODUCT_NAME}"
  CreateShortcut "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk" "$INSTDIR\${PRODUCT_EXE}"
  CreateShortcut "$DESKTOP\${PRODUCT_NAME}.lnk" "$INSTDIR\${PRODUCT_EXE}"

  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXE}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "Publisher" "${PRODUCT_PUBLISHER}"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
  WriteRegStr HKLM "${UNINSTALL_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
  WriteRegStr HKLM "${UNINSTALL_KEY}" "URLInfoAbout" "https://github.com/platten/playlistforge"
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  WriteRegDWORD HKLM "${UNINSTALL_KEY}" "EstimatedSize" $0
SectionEnd

Section "Uninstall"
  SetShellVarContext all
  SetRegView 64
  Delete "$DESKTOP\${PRODUCT_NAME}.lnk"
  Delete "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk"
  RMDir "$SMPROGRAMS\${PRODUCT_NAME}"
  Delete "$INSTDIR\${PRODUCT_EXE}"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKLM "${UNINSTALL_KEY}"
SectionEnd
