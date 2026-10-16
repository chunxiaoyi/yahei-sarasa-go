//go:build !windows

package sysinstall

import "errors"

type unsupported struct{}

// System returns the OS implementation for this platform.
func System() OS { return unsupported{} }

// DefaultPaths is only meaningful on Windows.
func DefaultPaths() (Paths, error) { return Paths{}, ErrUnsupported }

func (unsupported) Supported() bool                      { return false }
func (unsupported) IsAdmin() bool                        { return false }
func (unsupported) EnablePrivileges() error              { return ErrUnsupported }
func (unsupported) SecureDataDir(string) error           { return ErrUnsupported }
func (unsupported) ReadSecurity(string) (string, error)  { return "", ErrUnsupported }
func (unsupported) WriteSecurity(string, string) error   { return ErrUnsupported }
func (unsupported) ListFiles(string) ([]string, error)   { return nil, ErrUnsupported }
func (unsupported) ScheduleReplace(string, string) error { return ErrUnsupported }
func (unsupported) ScheduleDelete(string) error          { return ErrUnsupported }
func (unsupported) PendingOps() ([]PendingOp, error)     { return nil, errors.New("unsupported") }
func (unsupported) SetPendingOps([]PendingOp) error      { return ErrUnsupported }
