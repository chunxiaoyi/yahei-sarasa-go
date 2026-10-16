//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	shell32Elevate       = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteExW  = shell32Elevate.NewProc("ShellExecuteExW")
	kernel32Elevate      = syscall.NewLazyDLL("kernel32.dll")
	procGetExitCodeProc  = kernel32Elevate.NewProc("GetExitCodeProcess")
	errorCancelledByUser = syscall.Errno(1223) // ERROR_CANCELLED
)

// ErrElevationCancelled means the user declined the UAC prompt.
var ErrElevationCancelled = errors.New("已取消管理员授权")

type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           uintptr
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       uintptr
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      uintptr
	dwHotKey       uint32
	hIconOrMonitor uintptr
	hProcess       uintptr
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	swHide                = 0
)

// CanElevate reports whether RunElevated is available.
func CanElevate() bool { return true }

// RunElevated starts exe with args through the UAC prompt ("runas"), hidden,
// and waits for it to finish. It returns the process exit code.
func RunElevated(exe string, args []string) (uint32, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = syscall.EscapeArg(a)
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return 0, err
	}
	params, err := syscall.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return 0, err
	}
	owner, _, _ := procGetForegroundWindow.Call()
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync,
		hwnd:         owner,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: params,
		nShow:        swHide,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, e := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if errno, ok := e.(syscall.Errno); ok && errno == errorCancelledByUser {
			return 0, ErrElevationCancelled
		}
		return 0, fmt.Errorf("ShellExecuteEx(runas): %v", e)
	}
	if info.hProcess == 0 {
		return 0, errors.New("提权后的进程没有启动")
	}
	h := syscall.Handle(info.hProcess)
	defer syscall.CloseHandle(h)
	if _, err := syscall.WaitForSingleObject(h, syscall.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	if r, _, e := procGetExitCodeProc.Call(uintptr(h), uintptr(unsafe.Pointer(&code))); r == 0 {
		return 0, fmt.Errorf("GetExitCodeProcess: %v", e)
	}
	return code, nil
}

// Reboot restarts Windows after a short delay (a normal user may do this).
func Reboot() error {
	cmd := exec.Command("shutdown.exe", "/r", "/t", "5", "/c", "雅黑更纱：重启以完成字体替换")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}
