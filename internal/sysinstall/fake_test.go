package sysinstall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/custom"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/pack"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
)

// fakeOS stands in for Windows: the Session Manager list is a slice,
// security descriptors are strings in a map, and reboot() carries out the
// registered operations the way Windows does at boot.
type fakeOS struct {
	t         *testing.T
	noSupport bool
	notAdmin  bool
	sd        map[string]string
	pending   []PendingOp

	// Failure injection.
	privErr        error
	pendingReadErr error
	failReplaceN   int // fail the n-th ScheduleReplace call (1-based); 0 = never
	replaceCalls   int
}

const defaultSD = "O:S-1-5-80-1D:(A;ID;FA;;;S-1-5-80-1)(A;ID;0x1200a9;;;BU)"

func newFakeOS(t *testing.T) *fakeOS {
	return &fakeOS{t: t, sd: map[string]string{}}
}

func ntPath(p string) string { return `\??\` + p }

func (f *fakeOS) Supported() bool         { return !f.noSupport }
func (f *fakeOS) IsAdmin() bool           { return !f.notAdmin }
func (f *fakeOS) EnablePrivileges() error { return f.privErr }
func (f *fakeOS) SecureDataDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

func (f *fakeOS) ReadSecurity(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	if sd, ok := f.sd[path]; ok {
		return sd, nil
	}
	return defaultSD, nil
}

func (f *fakeOS) WriteSecurity(path, sddl string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	f.sd[path] = sddl
	return nil
}

func (f *fakeOS) ListFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

func (f *fakeOS) ScheduleReplace(src, dst string) error {
	f.replaceCalls++
	if f.failReplaceN != 0 && f.replaceCalls == f.failReplaceN {
		return errors.New("injected MoveFileEx failure")
	}
	f.pending = append(f.pending, PendingOp{ntPath(src), "!" + ntPath(dst)})
	return nil
}

func (f *fakeOS) ScheduleDelete(path string) error {
	f.pending = append(f.pending, PendingOp{ntPath(path), ""})
	return nil
}

func (f *fakeOS) PendingOps() ([]PendingOp, error) {
	if f.pendingReadErr != nil {
		return nil, f.pendingReadErr
	}
	return append([]PendingOp(nil), f.pending...), nil
}

func (f *fakeOS) SetPendingOps(ops []PendingOp) error {
	f.pending = append([]PendingOp(nil), ops...)
	return nil
}

// reboot performs every registered operation and empties the list, as the
// Session Manager does. Entries of other programs are performed too.
func (f *fakeOS) reboot() {
	f.t.Helper()
	for _, op := range f.pending {
		src := strings.TrimPrefix(op.Source, `\??\`)
		if op.Target == "" {
			if err := os.Remove(src); err != nil && !errors.Is(err, os.ErrNotExist) {
				f.t.Fatalf("boot delete %s: %v", src, err)
			}
			continue
		}
		dst := strings.TrimPrefix(op.Target, `!\??\`)
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) && op == otherProgram {
			continue // not part of the fake tree
		}
		if err := os.Rename(src, dst); err != nil {
			f.t.Fatalf("boot replace %s -> %s: %v", src, dst, err)
		}
		if sd, ok := f.sd[src]; ok {
			f.sd[dst] = sd
			delete(f.sd, src)
		}
	}
	f.pending = nil
}

// testPaths lays out a fake Windows tree in a temp dir.
func testPaths(t *testing.T) Paths {
	root := t.TempDir()
	p := Paths{
		FontsDir: filepath.Join(root, "Windows", "Fonts"),
		CacheDir: filepath.Join(root, "Windows", "ServiceProfiles", "LocalService", "AppData", "Local", "FontCache"),
		FntCache: filepath.Join(root, "Windows", "System32", "FNTCACHE.DAT"),
		DataDir:  filepath.Join(root, "ProgramData", "yahei-sarasa-go"),
	}
	for _, d := range []string{p.FontsDir, p.CacheDir, filepath.Dir(p.FntCache)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// writeSystemFonts puts placeholder files in the Fonts folder; missing
// names are left out.
func writeSystemFonts(t *testing.T, p Paths, missing ...string) map[string][]byte {
	t.Helper()
	skip := map[string]bool{}
	for _, m := range missing {
		skip[m] = true
	}
	files := map[string][]byte{}
	for _, name := range recipe.AllOutputs {
		if skip[name] {
			continue
		}
		b := []byte("system copy of " + name)
		if err := os.WriteFile(filepath.Join(p.FontsDir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
		files[name] = b
	}
	return files
}

func writeCache(t *testing.T, p Paths) {
	t.Helper()
	sub := filepath.Join(p.CacheDir, "Fonts")
	os.MkdirAll(sub, 0o755)
	for _, f := range []string{filepath.Join(p.CacheDir, "~FontCache-System.dat"), filepath.Join(sub, "Download-1.dat"), p.FntCache} {
		if err := os.WriteFile(f, []byte("cache"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var (
	pkgOnce sync.Once
	pkgDir  string
	pkgErr  error
)

// testPackage builds one real package (all six files, Windows identities)
// from the custom-font fixtures, shared by every test in the package.
func testPackage(t *testing.T) string {
	t.Helper()
	pkgOnce.Do(func() {
		pkgDir, pkgErr = buildPackage()
	})
	if pkgErr != nil {
		t.Fatal(pkgErr)
	}
	return pkgDir
}

func buildPackage() (string, error) {
	var faces []*custom.Face
	for _, n := range []string{"fixture-light.ttf", "fixture-regular.ttf", "fixture-medium.ttf", "fixture-bold.ttf"} {
		f, err := custom.Inspect(filepath.Join("..", "custom", "testdata", n))
		if err != nil {
			return "", err
		}
		if f.Error != "" {
			return "", fmt.Errorf("%s: %s", n, f.Error)
		}
		faces = append(faces, f.Faces...)
	}
	ev := custom.Evaluate(faces, custom.Request{Style: custom.StyleRegular})
	if !ev.CanBuild {
		return "", fmt.Errorf("fixtures cannot build: %+v", ev.Issues)
	}
	plan, err := custom.ToRecipe(faces, ev)
	if err != nil {
		return "", err
	}
	root, err := os.MkdirTemp("", "sysinstall-pkg-")
	if err != nil {
		return "", err
	}
	dir, fontsDir, err := pack.Prepare(root, "pkg")
	if err != nil {
		return "", err
	}
	outs, err := recipe.Build(plan, fontsDir, nil)
	if err != nil {
		return "", err
	}
	if _, err := pack.Finish(dir, pack.Manifest{Kind: "custom", Title: "test package", Outputs: outs}, false); err != nil {
		return "", err
	}
	return dir, nil
}

func TestMain(m *testing.M) {
	code := m.Run()
	if pkgDir != "" {
		os.RemoveAll(filepath.Dir(pkgDir))
	}
	os.Exit(code)
}
