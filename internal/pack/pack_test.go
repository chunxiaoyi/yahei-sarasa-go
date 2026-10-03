package pack

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
)

func TestPrepareNeverDeletesForeignFolders(t *testing.T) {
	root := t.TempDir()
	foreign := filepath.Join(root, "pkg")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(foreign, "important.txt")
	os.WriteFile(keep, []byte("user data"), 0o644)

	dir, fonts, err := Prepare(root, "pkg")
	if err != nil {
		t.Fatal(err)
	}
	if dir == foreign {
		t.Fatal("reused a folder this tool does not own")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("foreign folder was touched")
	}
	if filepath.Base(fonts) != FontsDir {
		t.Fatal(fonts)
	}

	// Our own folder (manifest marker) is replaced in place.
	os.WriteFile(filepath.Join(fonts, "msyh.ttc"), []byte("x"), 0o644)
	if _, err := Finish(dir, Manifest{Title: "t", Outputs: []recipe.OutputInfo{{Name: "msyh.ttc", SHA256: "ab"}}}, true); err != nil {
		t.Fatal(err)
	}
	dir2, _, err := Prepare(root, filepath.Base(dir))
	if err != nil {
		t.Fatal(err)
	}
	if dir2 != dir {
		t.Fatalf("own folder not reused: %s vs %s", dir2, dir)
	}
	if _, err := os.Stat(filepath.Join(dir2, FontsDir, "msyh.ttc")); err == nil {
		t.Fatal("old fonts survived a rebuild")
	}
	if _, err := os.Stat(dir + ".zip"); err == nil {
		t.Fatal("old zip survived a rebuild")
	}
}

func TestFinishWritesPackage(t *testing.T) {
	root := t.TempDir()
	dir, fonts, err := Prepare(root, "YaHei-Test")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(fonts, "msyh.ttc"), []byte("font"), 0o644)
	m := Manifest{Title: "测试包", Outputs: []recipe.OutputInfo{{Name: "msyh.ttc", SHA256: "deadbeef", Size: 4}}, Warnings: []string{"提示一"}}
	zipPath, err := Finish(dir, m, true)
	if err != nil {
		t.Fatal(err)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, SumsName))
	if string(sums) != "deadbeef  fonts/msyh.ttc\n" {
		t.Fatalf("sums %q", sums)
	}
	notes, _ := os.ReadFile(filepath.Join(dir, NotesName))
	if !strings.Contains(string(notes), "提示一") || !strings.Contains(string(notes), "\r\n") {
		t.Fatal("install notes incomplete")
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		if strings.HasSuffix(f.Name, ".ttc") && f.Method != zip.Store {
			t.Fatal("fonts should be stored, not deflated")
		}
	}
	for _, want := range []string{"YaHei-Test/fonts/msyh.ttc", "YaHei-Test/manifest.json", "YaHei-Test/SHA256SUMS.txt", "YaHei-Test/" + NotesName} {
		if !names[want] {
			t.Fatalf("zip missing %s (have %v)", want, names)
		}
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"YaHei-Custom-思源黑体 CN": "YaHei-Custom-思源黑体-CN", "../..": "fonts", "a  b": "a-b", `x<>:"|?*y`: "x-y"} {
		if got := SafeName(in); got != want {
			t.Fatalf("%q -> %q want %q", in, got, want)
		}
	}
}
