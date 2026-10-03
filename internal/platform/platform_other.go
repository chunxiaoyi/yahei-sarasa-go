//go:build !windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func Pick(PickOptions) ([]string, error) { return nil, ErrUnsupported }

// CanPick reports whether Pick shows a native dialog.
func CanPick() bool { return false }

func Reveal(path string) error {
	dir := path
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		dir = filepath.Dir(path)
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("open", "-R", path).Start()
	}
	return exec.Command("xdg-open", dir).Start()
}

func OpenBrowser(url string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}

// AppWindow starts a chromeless browser window when a Chromium-family
// browser is installed. It returns nil, nil when none is found.
func AppWindow(url, profileDir string) (*exec.Cmd, error) {
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "microsoft-edge"} {
		if p, err := exec.LookPath(name); err == nil {
			cmd := exec.Command(p, "--app="+url, "--user-data-dir="+profileDir, "--no-first-run", "--no-default-browser-check", "--window-size=1200,880")
			return cmd, cmd.Start()
		}
	}
	return nil, nil
}

func SystemProxy() string { return "" }

func SystemFontsDir() string { return "" }

func Prepare() {}

func AttachParentConsole() {}
