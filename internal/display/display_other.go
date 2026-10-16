//go:build !windows

package display

// Detect is only implemented for Windows, the platform whose fonts this
// tool replaces; elsewhere Recommend falls back to hinted.
func Detect() Info { return Info{Supported: false} }
