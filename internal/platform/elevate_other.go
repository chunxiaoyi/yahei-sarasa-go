//go:build !windows

package platform

import "errors"

// ErrElevationCancelled means the user declined the UAC prompt.
var ErrElevationCancelled = errors.New("已取消管理员授权")

// CanElevate reports whether RunElevated is available.
func CanElevate() bool { return false }

// RunElevated is only available on Windows.
func RunElevated(string, []string) (uint32, error) { return 0, ErrUnsupported }

// Reboot is only available on Windows.
func Reboot() error { return ErrUnsupported }
