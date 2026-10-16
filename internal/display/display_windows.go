//go:build windows

package display

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	shcore = syscall.NewLazyDLL("shcore.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")

	procEnumDisplayMonitors           = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	procEnumDisplaySettingsW          = user32.NewProc("EnumDisplaySettingsW")
	procSystemParametersInfoW         = user32.NewProc("SystemParametersInfoW")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procGetDpiForMonitor              = shcore.NewProc("GetDpiForMonitor")
	procGetDeviceCaps                 = gdi32.NewProc("GetDeviceCaps")
)

const (
	mdtEffectiveDPI          = 0
	monitorInfoFPrimary      = 1
	enumCurrentSettings      = 0xFFFFFFFF // ENUM_CURRENT_SETTINGS = (DWORD)-1
	spiGetFontSmoothing      = 0x004A
	spiGetFontSmoothingType  = 0x200A
	feFontSmoothingClearType = 2
	logPixelsX               = 88
	devModeSize              = 220 // sizeof(DEVMODEW)
)

type rect struct{ Left, Top, Right, Bottom int32 }

type monitorInfoEx struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
	szDevice  [32]uint16
}

// Detect enumerates monitors with their physical resolution and effective
// DPI, plus the font-smoothing setting.
func Detect() Info {
	info := Info{Supported: true}
	// Effective DPI is only reported truthfully to DPI-aware processes.
	// Harmless if awareness was already set (it can only be set once).
	if procSetProcessDpiAwarenessContext.Find() == nil {
		procSetProcessDpiAwarenessContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
	}

	var handles []uintptr
	cb := syscall.NewCallback(func(hmon, hdc, lprc, lparam uintptr) uintptr {
		handles = append(handles, hmon)
		return 1
	})
	if r, _, err := procEnumDisplayMonitors.Call(0, 0, cb, 0); r == 0 {
		info.Error = fmt.Sprintf("EnumDisplayMonitors: %v", err)
		return info
	}
	systemDPI := systemDPI()
	for _, h := range handles {
		mi := monitorInfoEx{cbSize: uint32(unsafe.Sizeof(monitorInfoEx{}))}
		if r, _, _ := procGetMonitorInfoW.Call(h, uintptr(unsafe.Pointer(&mi))); r == 0 {
			continue
		}
		m := Monitor{
			Name:    syscall.UTF16ToString(mi.szDevice[:]),
			Width:   int(mi.rcMonitor.Right - mi.rcMonitor.Left),
			Height:  int(mi.rcMonitor.Bottom - mi.rcMonitor.Top),
			Primary: mi.dwFlags&monitorInfoFPrimary != 0,
		}
		// The current display mode gives physical pixels even if the
		// process ended up DPI-unaware.
		var dm [devModeSize]byte
		*(*uint16)(unsafe.Pointer(&dm[68])) = devModeSize // dmSize
		if r, _, _ := procEnumDisplaySettingsW.Call(uintptr(unsafe.Pointer(&mi.szDevice[0])), enumCurrentSettings, uintptr(unsafe.Pointer(&dm[0]))); r != 0 {
			w := int(*(*uint32)(unsafe.Pointer(&dm[172]))) // dmPelsWidth
			hgt := int(*(*uint32)(unsafe.Pointer(&dm[176])))
			if w > 0 && hgt > 0 {
				m.Width, m.Height = w, hgt
			}
		}
		var dpiX, dpiY uint32
		if procGetDpiForMonitor.Find() == nil {
			if r, _, _ := procGetDpiForMonitor.Call(h, mdtEffectiveDPI, uintptr(unsafe.Pointer(&dpiX)), uintptr(unsafe.Pointer(&dpiY))); r == 0 {
				m.DPI = int(dpiX)
			}
		}
		if m.DPI == 0 {
			m.DPI = systemDPI
		}
		m.Scale = ScaleFromDPI(m.DPI)
		info.Monitors = append(info.Monitors, m)
	}

	var on int32
	if r, _, _ := procSystemParametersInfoW.Call(spiGetFontSmoothing, 0, uintptr(unsafe.Pointer(&on)), 0); r != 0 {
		if on == 0 {
			info.Smoothing = SmoothingOff
		} else {
			var typ uint32
			if r, _, _ := procSystemParametersInfoW.Call(spiGetFontSmoothingType, 0, uintptr(unsafe.Pointer(&typ)), 0); r != 0 && typ == feFontSmoothingClearType {
				info.Smoothing = SmoothingClearType
			} else {
				info.Smoothing = SmoothingGrayscale
			}
		}
	}
	return info
}

func systemDPI() int {
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return 96
	}
	defer procReleaseDC.Call(0, hdc)
	if v, _, _ := procGetDeviceCaps.Call(hdc, logPixelsX); v > 0 {
		return int(v)
	}
	return 96
}
