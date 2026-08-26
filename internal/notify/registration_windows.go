// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

//go:build windows

package notify

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	appRegistrationKey     = `SOFTWARE\Classes\AppUserModelId\` + appID
	protocolKey            = `SOFTWARE\Classes\` + protocolScheme
	shortcutName           = "Agent Watch.lnk"
	legacyShortcutName     = "agent-watch.lnk"
	activationScript       = "activate-notification.vbs"
	legacyActivationScript = "activate-notification.ps1"

	clsctxInprocServer      = 1
	coinitApartmentThreaded = 2
	vtLPWSTR                = 31
)

var (
	ole32                        = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx           = ole32.NewProc("CoInitializeEx")
	procCoUninitialize           = ole32.NewProc("CoUninitialize")
	procCoCreateInstance         = ole32.NewProc("CoCreateInstance")
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWnd         = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")

	clsidShellLink   = mustGUID("{00021401-0000-0000-C000-000000000046}")
	iidShellLinkW    = mustGUID("{000214F9-0000-0000-C000-000000000046}")
	iidPropertyStore = mustGUID("{886D8EEB-8CF2-4446-8D02-CDBA1DBDCF99}")
	iidPersistFile   = mustGUID("{0000010B-0000-0000-C000-000000000046}")

	pkeyAppUserModelID = propertyKey{
		FormatID:   mustGUID("{9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3}"),
		PropertyID: 5,
	}
)

type iUnknown struct {
	vtable *iUnknownVTable
}

type iUnknownVTable struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
}

type iShellLinkW struct {
	vtable *iShellLinkWVTable
}

type iShellLinkWVTable struct {
	queryInterface      uintptr
	addRef              uintptr
	release             uintptr
	getPath             uintptr
	getIDList           uintptr
	setIDList           uintptr
	getDescription      uintptr
	setDescription      uintptr
	getWorkingDirectory uintptr
	setWorkingDirectory uintptr
	getArguments        uintptr
	setArguments        uintptr
	getHotkey           uintptr
	setHotkey           uintptr
	getShowCmd          uintptr
	setShowCmd          uintptr
	getIconLocation     uintptr
	setIconLocation     uintptr
	setRelativePath     uintptr
	resolve             uintptr
	setPath             uintptr
}

type iPropertyStore struct {
	vtable *iPropertyStoreVTable
}

type iPropertyStoreVTable struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
	getCount       uintptr
	getAt          uintptr
	getValue       uintptr
	setValue       uintptr
	commit         uintptr
}

type iPersistFile struct {
	vtable *iPersistFileVTable
}

type iPersistFileVTable struct {
	queryInterface uintptr
	addRef         uintptr
	release        uintptr
	getClassID     uintptr
	isDirty        uintptr
	load           uintptr
	save           uintptr
	saveCompleted  uintptr
	getCurFile     uintptr
}

type propertyKey struct {
	FormatID   windows.GUID
	PropertyID uint32
}

type propVariant struct {
	ValueType uint16
	Reserved1 uint16
	Reserved2 uint16
	Reserved3 uint16
	Value     uintptr
}

func ensureAppRegistration() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("resolve absolute executable path: %w", err)
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user configuration directory: %w", err)
	}
	shortcutPath := filepath.Join(configDir, "Microsoft", "Windows", "Start Menu", "Programs", shortcutName)

	iconPath, err := ensureAppIcon()
	if err != nil {
		return "", err
	}
	scriptPath := filepath.Join(filepath.Dir(iconPath), activationScript)
	if err := ensureActivationScript(scriptPath); err != nil {
		return "", err
	}
	if err := registerActivationProtocol(scriptPath); err != nil {
		return "", err
	}
	if err := registerAppMetadata(executable, iconPath); err != nil {
		return "", err
	}
	if registrationIsCurrent(executable, shortcutPath) {
		return iconPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(shortcutPath), 0o755); err != nil {
		return "", fmt.Errorf("create Start Menu directory: %w", err)
	}
	if err := createStartMenuShortcut(executable, shortcutPath); err != nil {
		return "", err
	}
	legacyShortcutPath := filepath.Join(filepath.Dir(shortcutPath), legacyShortcutName)
	if err := os.Remove(legacyShortcutPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove legacy Start Menu shortcut: %w", err)
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, appRegistrationKey, registry.SET_VALUE)
	if err != nil {
		return "", fmt.Errorf("open app registration key: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue("ExecutablePath", executable); err != nil {
		return "", fmt.Errorf("record registered executable path: %w", err)
	}
	return iconPath, nil
}

func ensureActivationScript(path string) error {
	const script = `Option Explicit

If WScript.Arguments.Count <> 1 Then
    WScript.Quit 2
End If

Dim activationUri, queryStart
activationUri = WScript.Arguments(0)
If LCase(Left(activationUri, 23)) <> "agent-watch://activate?" Then
    WScript.Quit 2
End If

queryStart = InStr(activationUri, "?")
If queryStart = 0 Then
    WScript.Quit 2
End If

Dim parameters, part, pair, name, value
Set parameters = CreateObject("Scripting.Dictionary")
For Each part In Split(Mid(activationUri, queryStart + 1), "&")
    pair = Split(part, "=", 2)
    name = LCase(pair(0))
    value = ""
    If UBound(pair) = 1 Then
        value = pair(1)
    End If
    parameters(name) = value
Next

If Not parameters.Exists("port") Or Not IsNumeric(parameters("port")) Then
    WScript.Quit 3
End If
Dim port
port = CLng(parameters("port"))
If port < 1 Or port > 65535 Then
    WScript.Quit 3
End If

If Not parameters.Exists("token") Then
    WScript.Quit 4
End If
Dim tokenPattern
Set tokenPattern = New RegExp
tokenPattern.Pattern = "^[0-9a-f]{48}$"
If Not tokenPattern.Test(parameters("token")) Then
    WScript.Quit 4
End If

Dim request
Set request = CreateObject("WinHttp.WinHttpRequest.5.1")
request.SetTimeouts 2000, 2000, 2000, 5000
request.Open "POST", "http://127.0.0.1:" & port & "/activate/" & parameters("token"), False
request.Send
If request.Status <> 204 Then
    WScript.Quit 5
End If

If parameters.Exists("process") And IsNumeric(parameters("process")) Then
    Dim terminalProcess, shell
    terminalProcess = CLng(parameters("process"))
    If terminalProcess > 0 Then
        Set shell = CreateObject("WScript.Shell")
        shell.AppActivate terminalProcess
    End If
End If
`
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		return fmt.Errorf("write notification activation script: %w", err)
	}
	legacyPath := filepath.Join(filepath.Dir(path), legacyActivationScript)
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove legacy notification activation script: %w", err)
	}
	return nil
}

func registerActivationProtocol(scriptPath string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, protocolKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create notification protocol key: %w", err)
	}
	if err := key.SetStringValue("", "URL:Agent Watch Protocol"); err != nil {
		key.Close()
		return fmt.Errorf("set notification protocol name: %w", err)
	}
	if err := key.SetStringValue("URL Protocol", ""); err != nil {
		key.Close()
		return fmt.Errorf("mark notification protocol as URL protocol: %w", err)
	}
	key.Close()

	commandKey, _, err := registry.CreateKey(
		registry.CURRENT_USER,
		protocolKey+`\shell\open\command`,
		registry.SET_VALUE,
	)
	if err != nil {
		return fmt.Errorf("create notification protocol command: %w", err)
	}
	defer commandKey.Close()

	if err := commandKey.SetStringValue("", activationProtocolCommand(scriptPath)); err != nil {
		return fmt.Errorf("set notification protocol command: %w", err)
	}
	return nil
}

func activationProtocolCommand(scriptPath string) string {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	wscript := filepath.Join(systemRoot, "System32", "wscript.exe")
	return fmt.Sprintf(
		`"%s" //B //Nologo "%s" "%%1"`,
		wscript,
		scriptPath,
	)
}

func foregroundWindow() uintptr {
	window, _, _ := procGetForegroundWnd.Call()
	return window
}

func windowProcessID(window uintptr) uint32 {
	if window == 0 {
		return 0
	}
	var processID uint32
	procGetWindowThreadProcessID.Call(window, uintptr(unsafe.Pointer(&processID)))
	return processID
}

func registerAppMetadata(executable, iconPath string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, appRegistrationKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("create app registration key: %w", err)
	}
	defer key.Close()

	values := map[string]string{
		"DisplayName": "Agent Watch",
		"IconUri":     iconPath,
	}
	for name, value := range values {
		if err := key.SetStringValue(name, value); err != nil {
			return fmt.Errorf("set app registration value %s: %w", name, err)
		}
	}
	if err := key.SetDWordValue("ShowInSettings", 1); err != nil {
		return fmt.Errorf("enable app in notification settings: %w", err)
	}
	return nil
}

func ensureAppIcon() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	iconDir := filepath.Join(cacheDir, "agent-watch")
	iconPath := filepath.Join(iconDir, "agent-watch-agent.png")
	if info, err := os.Stat(iconPath); err == nil && info.Size() > 0 {
		return iconPath, nil
	}
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		return "", fmt.Errorf("create app icon directory: %w", err)
	}

	const size = 128
	icon := image.NewRGBA(image.Rect(0, 0, size, size))
	center := float64(size-1) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			distance := math.Hypot(float64(x)-center, float64(y)-center)
			if distance > 60 {
				continue
			}
			shade := uint8(116 - distance*0.6)
			icon.SetRGBA(x, y, color.RGBA{R: 28, G: shade, B: 210, A: 255})
		}
	}

	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	face := color.RGBA{R: 25, G: 55, B: 130, A: 255}
	accent := color.RGBA{R: 89, G: 227, B: 255, A: 255}

	drawIconLine(icon, center, 37, center, 22, 5, white)
	drawIconCircle(icon, center, 19, 6, accent)
	fillRoundedRect(icon, 27, 38, 101, 96, 15, white)
	fillRoundedRect(icon, 37, 49, 91, 80, 10, face)
	fillRoundedRect(icon, 20, 56, 31, 78, 5, white)
	fillRoundedRect(icon, 97, 56, 108, 78, 5, white)
	drawIconCircle(icon, 52, 64, 6, accent)
	drawIconCircle(icon, 76, 64, 6, accent)
	drawIconLine(icon, 52, 86, 76, 86, 5, face)

	file, err := os.Create(iconPath)
	if err != nil {
		return "", fmt.Errorf("create app icon: %w", err)
	}
	if err := png.Encode(file, icon); err != nil {
		file.Close()
		return "", fmt.Errorf("encode app icon: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close app icon: %w", err)
	}
	return iconPath, nil
}

func fillRoundedRect(img *image.RGBA, left, top, right, bottom, radius int, fill color.RGBA) {
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			nearestX := math.Max(float64(left+radius), math.Min(float64(right-radius-1), float64(x)))
			nearestY := math.Max(float64(top+radius), math.Min(float64(bottom-radius-1), float64(y)))
			if math.Hypot(float64(x)-nearestX, float64(y)-nearestY) <= float64(radius) {
				img.SetRGBA(x, y, fill)
			}
		}
	}
}

func drawIconCircle(img *image.RGBA, centerX, centerY, radius float64, fill color.RGBA) {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if math.Hypot(float64(x)-centerX, float64(y)-centerY) <= radius {
				img.SetRGBA(x, y, fill)
			}
		}
	}
}

func drawIconLine(img *image.RGBA, x1, y1, x2, y2, width float64, lineColor color.RGBA) {
	dx := x2 - x1
	dy := y2 - y1
	lengthSquared := dx*dx + dy*dy
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			px := float64(x) - x1
			py := float64(y) - y1
			t := (px*dx + py*dy) / lengthSquared
			t = math.Max(0, math.Min(1, t))
			nearestX := x1 + t*dx
			nearestY := y1 + t*dy
			if math.Hypot(float64(x)-nearestX, float64(y)-nearestY) <= width/2 {
				img.SetRGBA(x, y, lineColor)
			}
		}
	}
}

func registrationIsCurrent(executable, shortcutPath string) bool {
	if _, err := os.Stat(shortcutPath); err != nil {
		return false
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, appRegistrationKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	registeredPath, _, err := key.GetStringValue("ExecutablePath")
	return err == nil && filepath.Clean(registeredPath) == filepath.Clean(executable)
}

func createStartMenuShortcut(executable, shortcutPath string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	result, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if failedHRESULT(result) {
		return fmt.Errorf("initialize COM: HRESULT 0x%08X", uint32(result))
	}
	defer procCoUninitialize.Call()

	var shellLink *iShellLinkW
	result, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)),
		uintptr(unsafe.Pointer(&shellLink)),
	)
	if failedHRESULT(result) {
		return fmt.Errorf("create ShellLink: HRESULT 0x%08X", uint32(result))
	}
	defer shellLink.release()

	if err := shellLink.setPath(executable); err != nil {
		return err
	}
	if err := shellLink.setWorkingDirectory(filepath.Dir(executable)); err != nil {
		return err
	}
	if err := shellLink.setDescription("Monitor coding agent sessions"); err != nil {
		return err
	}
	if err := shellLink.setIconLocation(executable); err != nil {
		return err
	}

	var propertyStore *iPropertyStore
	if err := shellLink.queryInterface(&iidPropertyStore, unsafe.Pointer(&propertyStore)); err != nil {
		return fmt.Errorf("open shortcut property store: %w", err)
	}
	defer propertyStore.release()

	appIDPointer, err := windows.UTF16PtrFromString(appID)
	if err != nil {
		return fmt.Errorf("encode AppUserModelID: %w", err)
	}
	value := propVariant{
		ValueType: vtLPWSTR,
		Value:     uintptr(unsafe.Pointer(appIDPointer)),
	}
	result, _, _ = syscall.SyscallN(
		propertyStore.vtable.setValue,
		uintptr(unsafe.Pointer(propertyStore)),
		uintptr(unsafe.Pointer(&pkeyAppUserModelID)),
		uintptr(unsafe.Pointer(&value)),
	)
	runtime.KeepAlive(appIDPointer)
	if failedHRESULT(result) {
		return fmt.Errorf("set shortcut AppUserModelID: HRESULT 0x%08X", uint32(result))
	}
	result, _, _ = syscall.SyscallN(
		propertyStore.vtable.commit,
		uintptr(unsafe.Pointer(propertyStore)),
	)
	if failedHRESULT(result) {
		return fmt.Errorf("commit shortcut properties: HRESULT 0x%08X", uint32(result))
	}

	var persistFile *iPersistFile
	if err := shellLink.queryInterface(&iidPersistFile, unsafe.Pointer(&persistFile)); err != nil {
		return fmt.Errorf("open shortcut persistence interface: %w", err)
	}
	defer persistFile.release()

	pathPointer, err := windows.UTF16PtrFromString(shortcutPath)
	if err != nil {
		return fmt.Errorf("encode shortcut path: %w", err)
	}
	result, _, _ = syscall.SyscallN(
		persistFile.vtable.save,
		uintptr(unsafe.Pointer(persistFile)),
		uintptr(unsafe.Pointer(pathPointer)),
		1,
	)
	runtime.KeepAlive(pathPointer)
	if failedHRESULT(result) {
		return fmt.Errorf("save Start Menu shortcut: HRESULT 0x%08X", uint32(result))
	}
	return nil
}

func (link *iShellLinkW) queryInterface(interfaceID *windows.GUID, object unsafe.Pointer) error {
	result, _, _ := syscall.SyscallN(
		link.vtable.queryInterface,
		uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(interfaceID)),
		uintptr(object),
	)
	if failedHRESULT(result) {
		return fmt.Errorf("QueryInterface: HRESULT 0x%08X", uint32(result))
	}
	return nil
}

func (link *iShellLinkW) setPath(path string) error {
	return callShellLinkStringMethod(link, link.vtable.setPath, path, "set shortcut path")
}

func (link *iShellLinkW) setWorkingDirectory(path string) error {
	return callShellLinkStringMethod(link, link.vtable.setWorkingDirectory, path, "set shortcut working directory")
}

func (link *iShellLinkW) setDescription(description string) error {
	return callShellLinkStringMethod(link, link.vtable.setDescription, description, "set shortcut description")
}

func (link *iShellLinkW) setIconLocation(path string) error {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode shortcut icon path: %w", err)
	}
	result, _, _ := syscall.SyscallN(
		link.vtable.setIconLocation,
		uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(pathPointer)),
		0,
	)
	runtime.KeepAlive(pathPointer)
	if failedHRESULT(result) {
		return fmt.Errorf("set shortcut icon: HRESULT 0x%08X", uint32(result))
	}
	return nil
}

func callShellLinkStringMethod(link *iShellLinkW, method uintptr, value, operation string) error {
	valuePointer, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return fmt.Errorf("%s: encode value: %w", operation, err)
	}
	result, _, _ := syscall.SyscallN(
		method,
		uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(valuePointer)),
	)
	runtime.KeepAlive(valuePointer)
	if failedHRESULT(result) {
		return fmt.Errorf("%s: HRESULT 0x%08X", operation, uint32(result))
	}
	return nil
}

func (link *iShellLinkW) release() {
	syscall.SyscallN(link.vtable.release, uintptr(unsafe.Pointer(link)))
}

func (store *iPropertyStore) release() {
	syscall.SyscallN(store.vtable.release, uintptr(unsafe.Pointer(store)))
}

func (file *iPersistFile) release() {
	syscall.SyscallN(file.vtable.release, uintptr(unsafe.Pointer(file)))
}

func failedHRESULT(result uintptr) bool {
	return int32(result) < 0
}

func mustGUID(value string) windows.GUID {
	guid, err := windows.GUIDFromString(value)
	if err != nil {
		panic(err)
	}
	return guid
}
