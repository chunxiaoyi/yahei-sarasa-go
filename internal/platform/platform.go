// Package platform wraps the few OS services the GUI needs: native file
// dialogs, revealing files, opening the UI window, the system proxy and the
// system font folder. Windows is the real target; other systems get working
// fallbacks so the tool can be developed and tested anywhere.
package platform

import "errors"

// ErrUnsupported means the native feature does not exist on this OS; the GUI
// then offers drag-and-drop or a typed path instead.
var ErrUnsupported = errors.New("not supported on this platform")

// PickOptions configures a native open dialog.
type PickOptions struct {
	Title   string
	Folder  bool // pick a folder instead of files
	Multi   bool
	Filters []Filter
	Initial string // initial folder
}

// Filter is one file-type entry ("字体文件", "*.ttf;*.otf").
type Filter struct {
	Name    string
	Pattern string
}
