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

func TestPrepareResultDirCreatesMissingFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "result")
	if err := PrepareResultDir(dir); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
}

func TestPrepareResultDirClearsPreviousOutputs(t *testing.T) {
	dir := t.TempDir()
	// A previous run (any version) left the generated fonts; names may come
	// back in another case after a manual copy on Windows.
	for _, n := range []string{recipe.OutMSYH, strings.ToUpper(recipe.OutSegoeUI), recipe.OutSimSunB} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := PrepareResultDir(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("stale outputs left behind: %v", entries)
	}
}

func TestPrepareResultDirNeverDeletesForeignContent(t *testing.T) {
	cases := map[string]func(dir string){
		"other file": func(dir string) {
			os.WriteFile(filepath.Join(dir, "important.txt"), []byte("user data"), 0o644)
		},
		"subfolder": func(dir string) {
			os.MkdirAll(filepath.Join(dir, "photos"), 0o755)
		},
		"folder named like an output": func(dir string) {
			os.MkdirAll(filepath.Join(dir, recipe.OutMSYHL), 0o755)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			font := filepath.Join(dir, recipe.OutMSYH)
			os.WriteFile(font, []byte("old"), 0o644)
			setup(dir)
			before, _ := os.ReadDir(dir)
			if err := PrepareResultDir(dir); err == nil {
				t.Fatal("reused a folder with content this tool did not write")
			}
			after, _ := os.ReadDir(dir)
			if len(after) != len(before) {
				t.Fatalf("folder was changed: before %v, after %v", before, after)
			}
			if b, err := os.ReadFile(font); err != nil || string(b) != "old" {
				t.Fatal("output-named file was removed although the folder was refused")
			}
		})
	}
}

func TestPrepareResultDirRejectsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "result")
	os.WriteFile(f, []byte("x"), 0o644)
	if err := PrepareResultDir(f); err == nil {
		t.Fatal("accepted a file as the result folder")
	}
	if b, err := os.ReadFile(f); err != nil || string(b) != "x" {
		t.Fatal("file was changed")
	}
}
