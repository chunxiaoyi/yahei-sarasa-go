//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var (
	ole32    = syscall.NewLazyDLL("ole32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	procCoUninitialize      = ole32.NewProc("CoUninitialize")
	procCoCreateInstance    = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procSetDpiContext       = user32.NewProc("SetProcessDpiAwarenessContext")
	procAttachConsole       = kernel32.NewProc("AttachConsole")
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidFileOpenDialog = guid{0xDC1C5A9C, 0xE88A, 0x4DDE, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = guid{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
)

const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
	clsctxInprocServer      = 0x1
	rpcEChangedMode         = 0x80010106
	errorCancelled          = 0x800704C7
	sigdnFileSysPath        = 0x80058000

	fosNoChangeDir      = 0x8
	fosPickFolders      = 0x20
	fosForceFileSystem  = 0x40
	fosAllowMultiSelect = 0x200
	fosPathMustExist    = 0x800
	fosFileMustExist    = 0x1000

	// IFileOpenDialog vtable (IUnknown, IModalWindow, IFileDialog, IFileOpenDialog).
	vRelease      = 2
	vShow         = 3
	vSetFileTypes = 4
	vSetTypeIndex = 5
	vSetOptions   = 9
	vGetOptions   = 10
	vSetFolder    = 12
	vSetTitle     = 17
	vGetResult    = 20
	vGetResults   = 27
	vItemDisplay  = 5 // IShellItem::GetDisplayName
	vArrayCount   = 7 // IShellItemArray::GetCount
	vArrayItemAt  = 8 // IShellItemArray::GetItemAt
)

var iidShellItem = guid{0x43826D1E, 0xE718, 0x42EE, [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}

var procSHCreateItemFromParsingName = syscall.NewLazyDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")

type filterSpec struct {
	name *uint16
	spec *uint16
}

// vcall invokes method index of a COM object.
func vcall(obj unsafe.Pointer, index int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var s []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		c := *(*uint16)(ptr)
		if c == 0 {
			break
		}
		s = append(s, c)
	}
	return syscall.UTF16ToString(s)
}

// CanPick reports whether Pick shows a native dialog.
func CanPick() bool { return true }

// Pick shows the system "Open" dialog (IFileOpenDialog) owned by the
// foreground window, i.e. the app window the user just clicked in.
func Pick(opt PickOptions) ([]string, error) {
	type result struct {
		paths []string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		// COM apartment state is per thread: pin this goroutine to a fresh
		// OS thread for the dialog's lifetime. The thread is discarded when
		// the goroutine exits while still locked.
		runtime.LockOSThread()
		paths, err := pickOnThread(opt)
		ch <- result{paths, err}
	}()
	r := <-ch
	return r.paths, r.err
}

func pickOnThread(opt PickOptions) ([]string, error) {
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	if !failed(hr) {
		defer procCoUninitialize.Call()
	} else if uint32(hr) != rpcEChangedMode {
		return nil, fmt.Errorf("CoInitializeEx: %#x", uint32(hr))
	}
	var dlg unsafe.Pointer
	hr, _, _ = procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidFileOpenDialog)), uintptr(unsafe.Pointer(&dlg)))
	if failed(hr) || dlg == nil {
		return nil, fmt.Errorf("CoCreateInstance(FileOpenDialog): %#x", uint32(hr))
	}
	defer vcall(dlg, vRelease)

	var opts uint32
	vcall(dlg, vGetOptions, uintptr(unsafe.Pointer(&opts)))
	opts |= fosForceFileSystem | fosNoChangeDir | fosPathMustExist
	if opt.Folder {
		opts |= fosPickFolders
	} else {
		opts |= fosFileMustExist
	}
	if opt.Multi && !opt.Folder {
		opts |= fosAllowMultiSelect
	}
	vcall(dlg, vSetOptions, uintptr(opts))

	var keep []*uint16 // keep UTF-16 buffers alive across the calls
	u16 := func(s string) *uint16 {
		p, _ := syscall.UTF16PtrFromString(s)
		keep = append(keep, p)
		return p
	}
	if opt.Title != "" {
		vcall(dlg, vSetTitle, uintptr(unsafe.Pointer(u16(opt.Title))))
	}
	if !opt.Folder && len(opt.Filters) > 0 {
		specs := make([]filterSpec, len(opt.Filters))
		for i, f := range opt.Filters {
			specs[i] = filterSpec{u16(f.Name), u16(f.Pattern)}
		}
		vcall(dlg, vSetFileTypes, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0])))
		vcall(dlg, vSetTypeIndex, 1)
		runtime.KeepAlive(specs)
	}
	if opt.Initial != "" {
		var folder unsafe.Pointer
		hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(u16(opt.Initial))), 0,
			uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&folder)))
		if !failed(hr) && folder != nil {
			vcall(dlg, vSetFolder, uintptr(folder))
			vcall(folder, vRelease)
		}
	}

	owner, _, _ := procGetForegroundWindow.Call()
	hr = vcall(dlg, vShow, owner)
	runtime.KeepAlive(keep)
	if uint32(hr) == errorCancelled {
		return nil, nil
	}
	if failed(hr) {
		return nil, fmt.Errorf("IFileDialog::Show: %#x", uint32(hr))
	}

	itemPath := func(item unsafe.Pointer) (string, error) {
		var psz *uint16
		hr := vcall(item, vItemDisplay, sigdnFileSysPath, uintptr(unsafe.Pointer(&psz)))
		if failed(hr) {
			return "", fmt.Errorf("GetDisplayName: %#x", uint32(hr))
		}
		s := utf16PtrToString(psz)
		procCoTaskMemFree.Call(uintptr(unsafe.Pointer(psz)))
		return s, nil
	}

	if opt.Multi && !opt.Folder {
		var arr unsafe.Pointer
		if hr := vcall(dlg, vGetResults, uintptr(unsafe.Pointer(&arr))); failed(hr) || arr == nil {
			return nil, fmt.Errorf("GetResults: %#x", uint32(hr))
		}
		defer vcall(arr, vRelease)
		var n uint32
		vcall(arr, vArrayCount, uintptr(unsafe.Pointer(&n)))
		var out []string
		for i := uint32(0); i < n; i++ {
			var item unsafe.Pointer
			if hr := vcall(arr, vArrayItemAt, uintptr(i), uintptr(unsafe.Pointer(&item))); failed(hr) || item == nil {
				continue
			}
			p, err := itemPath(item)
			vcall(item, vRelease)
			if err == nil && p != "" {
				out = append(out, p)
			}
		}
		return out, nil
	}
	var item unsafe.Pointer
	if hr := vcall(dlg, vGetResult, uintptr(unsafe.Pointer(&item))); failed(hr) || item == nil {
		return nil, fmt.Errorf("GetResult: %#x", uint32(hr))
	}
	defer vcall(item, vRelease)
	p, err := itemPath(item)
	if err != nil {
		return nil, err
	}
	return []string{p}, nil
}

// Reveal opens Explorer with path selected (or the folder itself).
func Reveal(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return err
	}
	cmd := exec.Command("explorer.exe")
	if st.IsDir() {
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe "` + abs + `"`}
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + abs + `"`}
	}
	return cmd.Start() // explorer's exit code is meaningless
}

func OpenBrowser(url string) error {
	return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url).Start()
}

// EdgePath finds msedge.exe (always present on Windows 11).
func EdgePath() string {
	var candidates []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if base := os.Getenv(env); base != "" {
			candidates = append(candidates, filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"))
		}
	}
	if p := regString(syscall.HKEY_LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, ""); p != "" {
		candidates = append([]string{p}, candidates...)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// AppWindow opens the UI as a chromeless Microsoft Edge app window with its
// own profile (so it never touches the user's browsing profile).
func AppWindow(url, profileDir string) (*exec.Cmd, error) {
	edge := EdgePath()
	if edge == "" {
		return nil, nil
	}
	cmd := exec.Command(edge,
		"--app="+url,
		"--user-data-dir="+profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--window-size=1200,880",
	)
	return cmd, cmd.Start()
}

func regString(root syscall.Handle, path, name string) string {
	var key syscall.Handle
	p, _ := syscall.UTF16PtrFromString(path)
	if syscall.RegOpenKeyEx(root, p, 0, syscall.KEY_READ, &key) != nil {
		return ""
	}
	defer syscall.RegCloseKey(key)
	var namePtr *uint16
	if name != "" {
		namePtr, _ = syscall.UTF16PtrFromString(name)
	}
	var typ, size uint32
	if syscall.RegQueryValueEx(key, namePtr, nil, &typ, nil, &size) != nil || size == 0 || size > 1<<16 {
		return ""
	}
	buf := make([]uint16, size/2+1)
	if syscall.RegQueryValueEx(key, namePtr, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size) != nil {
		return ""
	}
	if typ != syscall.REG_SZ && typ != syscall.REG_EXPAND_SZ {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func regDWORD(root syscall.Handle, path, name string) (uint32, bool) {
	var key syscall.Handle
	p, _ := syscall.UTF16PtrFromString(path)
	if syscall.RegOpenKeyEx(root, p, 0, syscall.KEY_READ, &key) != nil {
		return 0, false
	}
	defer syscall.RegCloseKey(key)
	n, _ := syscall.UTF16PtrFromString(name)
	var typ, v uint32
	size := uint32(4)
	if syscall.RegQueryValueEx(key, n, nil, &typ, (*byte)(unsafe.Pointer(&v)), &size) != nil || typ != syscall.REG_DWORD {
		return 0, false
	}
	return v, true
}

// SystemProxy returns the Internet Options proxy (what Clash, v2rayN and
// similar clients set in "system proxy" mode), or "" when disabled.
func SystemProxy() string {
	const k = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
	if v, ok := regDWORD(syscall.HKEY_CURRENT_USER, k, "ProxyEnable"); !ok || v == 0 {
		return ""
	}
	return strings.TrimSpace(regString(syscall.HKEY_CURRENT_USER, k, "ProxyServer"))
}

func SystemFontsDir() string {
	win := os.Getenv("WINDIR")
	if win == "" {
		win = `C:\Windows`
	}
	return filepath.Join(win, "Fonts")
}

// Prepare makes native dialogs crisp on high-DPI screens (Windows 10 1703+).
func Prepare() {
	if procSetDpiContext.Find() == nil {
		const perMonitorV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		procSetDpiContext.Call(perMonitorV2)
	}
}

// AttachParentConsole lets the GUI-subsystem build print CLI output when it
// is started from a terminal with arguments.
func AttachParentConsole() {
	if h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != syscall.InvalidHandle {
		return // already has a usable stdout (console build, or redirected)
	}
	const attachParent = ^uintptr(0) // ATTACH_PARENT_PROCESS = -1
	if r, _, _ := procAttachConsole.Call(attachParent); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}
