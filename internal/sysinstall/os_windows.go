//go:build windows

package sysinstall

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	procLookupPrivilegeValueW        = advapi32.NewProc("LookupPrivilegeValueW")
	procAdjustTokenPrivileges        = advapi32.NewProc("AdjustTokenPrivileges")
	procGetSecurityInfo              = advapi32.NewProc("GetSecurityInfo")
	procSetSecurityInfo              = advapi32.NewProc("SetSecurityInfo")
	procSDToStringSD                 = advapi32.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW")
	procStringSDToSD                 = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procGetSecurityDescriptorOwner   = advapi32.NewProc("GetSecurityDescriptorOwner")
	procGetSecurityDescriptorDacl    = advapi32.NewProc("GetSecurityDescriptorDacl")
	procConvertSidToStringSidW       = advapi32.NewProc("ConvertSidToStringSidW")
	procRegSetValueExW               = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW              = advapi32.NewProc("RegDeleteValueW")
	procMoveFileExW                  = kernel32.NewProc("MoveFileExW")
	procLocalFree                    = kernel32.NewProc("LocalFree")
	procGetFileInformationByHandleEx = kernel32.NewProc("GetFileInformationByHandleEx")
	procGetSystemWindowsDirectoryW   = kernel32.NewProc("GetSystemWindowsDirectoryW")
	procSHGetKnownFolderPath         = shell32.NewProc("SHGetKnownFolderPath")
	procCoTaskMemFree                = ole32.NewProc("CoTaskMemFree")
)

const (
	seFileObject                     = 1
	ownerSecurityInformation         = 0x00000001
	daclSecurityInformation          = 0x00000004
	protectedDaclSecurityInformation = 0x80000000
	sddlRevision1                    = 1

	readControl               = 0x00020000
	writeDAC                  = 0x00040000
	writeOwner                = 0x00080000
	fileListDirectory         = 0x00000001
	fileFlagBackupSemantics   = 0x02000000
	fileFlagOpenReparsePoint  = 0x00200000
	fileAttributeDirectory    = 0x00000010
	fileAttributeReparsePoint = 0x00000400

	movefileReplaceExisting  = 0x1
	movefileDelayUntilReboot = 0x4

	sePrivilegeEnabled  = 0x00000002
	errorNotAllAssigned = 1300
	tokenElevationClass = 20

	fileFullDirectoryInfo        = 14
	fileFullDirectoryRestartInfo = 15
	errorNoMoreFiles             = 18
	errorMoreData                = 234

	regMultiSZ    = 7
	keyQueryValue = 0x0001
	keySetValue   = 0x0002

	sessionManagerKey = `SYSTEM\CurrentControlSet\Control\Session Manager`
	pendingValueName  = "PendingFileRenameOperations"

	sidAdministrators = "S-1-5-32-544"
	sidSystem         = "S-1-5-18"

	// SYSTEM and Administrators full control, Users read; inherited by
	// everything created inside, never inherited from ProgramData.
	dataDirSDDL = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)"
)

type winOS struct{}

// System returns the Windows implementation.
func System() OS { return winOS{} }

func (winOS) Supported() bool { return true }

func (winOS) IsAdmin() bool {
	t, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer t.Close()
	var elevated, n uint32
	if err := syscall.GetTokenInformation(t, tokenElevationClass, (*byte)(unsafe.Pointer(&elevated)), 4, &n); err != nil {
		return false
	}
	return elevated != 0
}

type luid struct {
	Low  uint32
	High int32
}

type luidAndAttributes struct {
	Luid       luid
	Attributes uint32
}

type tokenPrivileges struct {
	Count      uint32
	Privileges [3]luidAndAttributes
}

func (winOS) EnablePrivileges() error {
	proc, err := syscall.GetCurrentProcess()
	if err != nil {
		return err
	}
	var t syscall.Token
	if err := syscall.OpenProcessToken(proc, syscall.TOKEN_ADJUST_PRIVILEGES|syscall.TOKEN_QUERY, &t); err != nil {
		return err
	}
	defer t.Close()
	names := []string{"SeBackupPrivilege", "SeRestorePrivilege", "SeTakeOwnershipPrivilege"}
	tp := tokenPrivileges{Count: uint32(len(names))}
	for i, n := range names {
		p, _ := syscall.UTF16PtrFromString(n)
		if r, _, e := procLookupPrivilegeValueW.Call(0, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&tp.Privileges[i].Luid))); r == 0 {
			return fmt.Errorf("LookupPrivilegeValue(%s): %v", n, e)
		}
		tp.Privileges[i].Attributes = sePrivilegeEnabled
	}
	r, _, e := procAdjustTokenPrivileges.Call(uintptr(t), 0, uintptr(unsafe.Pointer(&tp)), 0, 0, 0)
	if r == 0 {
		return fmt.Errorf("AdjustTokenPrivileges: %v", e)
	}
	if errno, ok := e.(syscall.Errno); ok && errno == errorNotAllAssigned {
		return errors.New("当前账户没有备份/还原文件的特权")
	}
	return nil
}

// openHandle opens path (file or directory) without following reparse
// points, using backup semantics so the enabled privileges apply.
func openHandle(path string, access uint32) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, err := syscall.CreateFile(p, access,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, fileFlagBackupSemantics|fileFlagOpenReparsePoint, 0)
	if err != nil {
		return 0, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return h, nil
}

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
	return string(utf16.Decode(s))
}

func (winOS) ReadSecurity(path string) (string, error) {
	h, err := openHandle(path, readControl)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	var owner, dacl, sd uintptr
	if r, _, _ := procGetSecurityInfo.Call(uintptr(h), seFileObject, ownerSecurityInformation|daclSecurityInformation,
		uintptr(unsafe.Pointer(&owner)), 0, uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd))); r != 0 {
		return "", fmt.Errorf("GetSecurityInfo(%s): %w", path, syscall.Errno(r))
	}
	defer procLocalFree.Call(sd)
	var str *uint16
	var n uint32
	if r, _, e := procSDToStringSD.Call(sd, sddlRevision1, ownerSecurityInformation|daclSecurityInformation,
		uintptr(unsafe.Pointer(&str)), uintptr(unsafe.Pointer(&n))); r == 0 {
		return "", fmt.Errorf("ConvertSecurityDescriptorToStringSecurityDescriptor: %v", e)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(str)))
	return utf16PtrToString(str), nil
}

func (winOS) WriteSecurity(path, sddl string) error {
	s, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		return err
	}
	var sd uintptr
	if r, _, e := procStringSDToSD.Call(uintptr(unsafe.Pointer(s)), sddlRevision1, uintptr(unsafe.Pointer(&sd)), 0); r == 0 {
		return fmt.Errorf("ConvertStringSecurityDescriptorToSecurityDescriptor: %v", e)
	}
	defer procLocalFree.Call(sd)
	var owner, dacl uintptr
	var present, defaulted int32
	procGetSecurityDescriptorOwner.Call(sd, uintptr(unsafe.Pointer(&owner)), uintptr(unsafe.Pointer(&defaulted)))
	procGetSecurityDescriptorDacl.Call(sd, uintptr(unsafe.Pointer(&present)), uintptr(unsafe.Pointer(&dacl)), uintptr(unsafe.Pointer(&defaulted)))
	if present == 0 || dacl == 0 {
		return errors.New("SDDL has no DACL")
	}
	info := uint32(daclSecurityInformation | protectedDaclSecurityInformation)
	access := uint32(writeDAC | readControl)
	if owner != 0 {
		info |= ownerSecurityInformation
		access |= writeOwner
	}
	h, err := openHandle(path, access)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	if r, _, _ := procSetSecurityInfo.Call(uintptr(h), seFileObject, uintptr(info), owner, 0, dacl, 0); r != 0 {
		return fmt.Errorf("SetSecurityInfo(%s): %w", path, syscall.Errno(r))
	}
	return nil
}

func ownerSID(path string) (string, error) {
	h, err := openHandle(path, readControl)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	var owner, sd uintptr
	if r, _, _ := procGetSecurityInfo.Call(uintptr(h), seFileObject, ownerSecurityInformation,
		uintptr(unsafe.Pointer(&owner)), 0, 0, 0, uintptr(unsafe.Pointer(&sd))); r != 0 {
		return "", fmt.Errorf("GetSecurityInfo(%s): %w", path, syscall.Errno(r))
	}
	defer procLocalFree.Call(sd)
	var str *uint16
	if r, _, e := procConvertSidToStringSidW.Call(owner, uintptr(unsafe.Pointer(&str))); r == 0 {
		return "", fmt.Errorf("ConvertSidToStringSid: %v", e)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(str)))
	return utf16PtrToString(str), nil
}

func (w winOS) SecureDataDir(dir string) error {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	attrs, err := syscall.GetFileAttributes(p)
	created := false
	switch {
	case err == nil:
		if attrs&fileAttributeReparsePoint != 0 {
			return fmt.Errorf("%s 是一个链接（reparse point），出于安全考虑拒绝使用", dir)
		}
		if attrs&fileAttributeDirectory == 0 {
			return fmt.Errorf("%s 不是文件夹", dir)
		}
	case errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_PATH_NOT_FOUND):
		if err := os.Mkdir(dir, 0o755); err != nil {
			return err
		}
		created = true
	default:
		return err
	}
	if created {
		// Our own new folder: make Administrators the owner regardless of
		// the "default owner" policy, then lock it down.
		return w.WriteSecurity(dir, "O:BA"+dataDirSDDL)
	}
	owner, err := ownerSID(dir)
	if err != nil {
		return err
	}
	if owner != sidAdministrators && owner != sidSystem {
		return fmt.Errorf("%s 的所有者是 %s，不是管理员；它可能被其他账户预先创建，出于安全考虑拒绝使用。请确认后删除这个文件夹再试", dir, owner)
	}
	return w.WriteSecurity(dir, dataDirSDDL)
}

func (winOS) ListFiles(dir string) ([]string, error) {
	var out []string
	if err := listDir(dir, 0, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// listDir enumerates with GetFileInformationByHandleEx on a handle opened
// with backup semantics, which works on folders whose DACL excludes
// Administrators (the font cache folder belongs to LocalService).
func listDir(dir string, depth int, out *[]string) error {
	h, err := openHandle(dir, fileListDirectory|syscall.SYNCHRONIZE)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	buf := make([]byte, 64<<10)
	class := uintptr(fileFullDirectoryRestartInfo)
	for {
		r, _, e := procGetFileInformationByHandleEx.Call(uintptr(h), class, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		class = fileFullDirectoryInfo
		if r == 0 {
			if errno, ok := e.(syscall.Errno); ok && errno == errorNoMoreFiles {
				return nil
			}
			return fmt.Errorf("list %s: %v", dir, e)
		}
		for off := 0; off+68 <= len(buf); {
			next := int(binary.LittleEndian.Uint32(buf[off:]))
			attrs := binary.LittleEndian.Uint32(buf[off+56:])
			nameLen := int(binary.LittleEndian.Uint32(buf[off+60:]))
			if off+68+nameLen > len(buf) {
				break
			}
			u := make([]uint16, nameLen/2)
			for i := range u {
				u[i] = binary.LittleEndian.Uint16(buf[off+68+2*i:])
			}
			name := string(utf16.Decode(u))
			if name != "." && name != ".." {
				full := filepath.Join(dir, name)
				switch {
				case attrs&fileAttributeReparsePoint != 0:
					// never follow links out of the cache folder
				case attrs&fileAttributeDirectory != 0:
					if depth < 3 {
						_ = listDir(full, depth+1, out)
					}
				default:
					*out = append(*out, full)
				}
			}
			if next == 0 {
				break
			}
			off += next
		}
	}
}

func moveFileEx(src string, dst *uint16, flags uint32) error {
	s, err := syscall.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	if r, _, e := procMoveFileExW.Call(uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(dst)), uintptr(flags)); r == 0 {
		return e
	}
	return nil
}

func (winOS) ScheduleReplace(src, dst string) error {
	d, err := syscall.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return moveFileEx(src, d, movefileReplaceExisting|movefileDelayUntilReboot)
}

func (winOS) ScheduleDelete(path string) error {
	return moveFileEx(path, nil, movefileDelayUntilReboot)
}

func openSessionManager(access uint32) (syscall.Handle, error) {
	k, _ := syscall.UTF16PtrFromString(sessionManagerKey)
	var key syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_LOCAL_MACHINE, k, 0, access, &key); err != nil {
		return 0, err
	}
	return key, nil
}

func (winOS) PendingOps() ([]PendingOp, error) {
	key, err := openSessionManager(keyQueryValue)
	if err != nil {
		return nil, err
	}
	defer syscall.RegCloseKey(key)
	name, _ := syscall.UTF16PtrFromString(pendingValueName)
	for attempt := 0; attempt < 5; attempt++ {
		var typ, size uint32
		err := syscall.RegQueryValueEx(key, name, nil, &typ, nil, &size)
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if size < 2 {
			return nil, nil
		}
		buf := make([]uint16, size/2+1)
		got := uint32(len(buf) * 2)
		err = syscall.RegQueryValueEx(key, name, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &got)
		if errno, ok := err.(syscall.Errno); ok && errno == errorMoreData {
			continue // grew in between
		}
		if err != nil {
			return nil, err
		}
		if typ != regMultiSZ {
			return nil, fmt.Errorf("%s 的类型是 %d，不是 REG_MULTI_SZ", pendingValueName, typ)
		}
		return DecodePending(buf[:got/2]), nil
	}
	return nil, errors.New("PendingFileRenameOperations keeps changing")
}

func (winOS) SetPendingOps(ops []PendingOp) error {
	key, err := openSessionManager(keySetValue)
	if err != nil {
		return err
	}
	defer syscall.RegCloseKey(key)
	name, _ := syscall.UTF16PtrFromString(pendingValueName)
	if len(ops) == 0 {
		r, _, _ := procRegDeleteValueW.Call(uintptr(key), uintptr(unsafe.Pointer(name)))
		if r != 0 && syscall.Errno(r) != syscall.ERROR_FILE_NOT_FOUND {
			return syscall.Errno(r)
		}
		return nil
	}
	data := EncodePending(ops)
	if r, _, _ := procRegSetValueExW.Call(uintptr(key), uintptr(unsafe.Pointer(name)), 0, regMultiSZ,
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)*2)); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var folderIDProgramData = guid{0x62AB5D82, 0xFDC1, 0x4DC3, [8]byte{0xA9, 0xDD, 0x07, 0x0D, 0x1D, 0x49, 0x5D, 0x97}}

func windowsDir() (string, error) {
	buf := make([]uint16, syscall.MAX_PATH)
	r, _, e := procGetSystemWindowsDirectoryW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 || int(r) > len(buf) {
		return "", fmt.Errorf("GetSystemWindowsDirectory: %v", e)
	}
	return syscall.UTF16ToString(buf[:r]), nil
}

func programData() (string, error) {
	var p *uint16
	if hr, _, _ := procSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(&folderIDProgramData)), 0, 0, uintptr(unsafe.Pointer(&p))); hr != 0 {
		return "", fmt.Errorf("SHGetKnownFolderPath(ProgramData): %#x", uint32(hr))
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
	return utf16PtrToString(p), nil
}

// DefaultPaths resolves the real locations from the OS, not from
// environment variables a caller could set.
func DefaultPaths() (Paths, error) {
	win, err := windowsDir()
	if err != nil {
		return Paths{}, err
	}
	pd, err := programData()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		FontsDir: filepath.Join(win, "Fonts"),
		CacheDir: filepath.Join(win, "ServiceProfiles", "LocalService", "AppData", "Local", "FontCache"),
		FntCache: filepath.Join(win, "System32", "FNTCACHE.DAT"),
		DataDir:  filepath.Join(pd, "yahei-sarasa-go"),
	}, nil
}
