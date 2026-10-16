package sysinstall

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/pack"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
)

// PackageFile is one font of a package.
type PackageFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Package is a verified set of replacement fonts.
type Package struct {
	Dir      string        `json:"dir"`
	FontsDir string        `json:"fontsDir"`
	Name     string        `json:"name"`
	Title    string        `json:"title"`
	Files    []PackageFile `json:"files"`
	Notes    []string      `json:"notes,omitempty"`
}

func findName(dir, name string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// VerifyPackage checks a package folder (or a bare folder of fonts): every
// recognised file must parse, carry the Windows identity of its name, and
// match SHA256SUMS.txt when the package has one.
func VerifyPackage(dir string) (*Package, error) {
	abs, err := filepath.Abs(strings.Trim(strings.TrimSpace(dir), `"`))
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil || !st.IsDir() {
		return nil, fmt.Errorf("找不到字体包文件夹：%s", abs)
	}
	pkg := &Package{Dir: abs, FontsDir: abs, Name: filepath.Base(abs)}
	if fd := filepath.Join(abs, pack.FontsDir); isDir(fd) {
		pkg.FontsDir = fd
	} else if filepath.Base(abs) == pack.FontsDir {
		// The user picked the fonts folder itself.
		pkg.Dir = filepath.Dir(abs)
		pkg.Name = filepath.Base(pkg.Dir)
	}
	if m, err := pack.ReadManifest(pkg.Dir); err == nil {
		pkg.Title = m.Title
	}
	sums, hasSums := readSums(pkg.Dir)

	known := map[string]bool{}
	for _, name := range recipe.AllOutputs {
		known[name] = true
		path := findName(pkg.FontsDir, name)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		f := PackageFile{Name: name, Path: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
		if hasSums {
			want, ok := sums[name]
			if !ok {
				return nil, fmt.Errorf("%s 不在 SHA256SUMS.txt 里，字体包可能被改动过", name)
			}
			if want != f.SHA256 {
				return nil, fmt.Errorf("%s 与 SHA256SUMS.txt 不一致，字体包可能已损坏或被改动", name)
			}
		}
		if err := verifyFontFile(name, data); err != nil {
			return nil, err
		}
		pkg.Files = append(pkg.Files, f)
	}
	if len(pkg.Files) == 0 {
		return nil, fmt.Errorf("%s 里没有可安装的字体（需要 msyh.ttc、msyhbd.ttc、msyhl.ttc、simsun.ttc、simsunb.ttf、segoeui.ttf 中的至少一个）", pkg.FontsDir)
	}
	if entries, err := os.ReadDir(pkg.FontsDir); err == nil {
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if !e.IsDir() && (ext == ".ttf" || ext == ".ttc" || ext == ".otf") && !known[strings.ToLower(e.Name())] {
				pkg.Notes = append(pkg.Notes, "忽略不认识的文件 "+e.Name())
			}
		}
	}
	return pkg, nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// readSums parses SHA256SUMS.txt ("<hash>  fonts/<name>").
func readSums(dir string) (map[string]string, bool) {
	f, err := os.Open(filepath.Join(dir, pack.SumsName))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		name := strings.ToLower(filepath.Base(filepath.FromSlash(strings.TrimPrefix(fields[1], "*"))))
		out[name] = strings.ToLower(fields[0])
	}
	return out, true
}

// verifyFontFile checks that data can stand in for the system file name:
// same faces in the same order, each with the Windows family name and
// weight, and (for Segoe UI) no CJK in the cmap, which would break the
// Microsoft YaHei UI fallback.
func verifyFontFile(name string, data []byte) error {
	want := recipe.ExpectedFaces(name)
	faces, _, err := otfont.ParseFaces(data)
	if err != nil {
		return fmt.Errorf("%s 无法解析：%w", name, err)
	}
	if len(faces) != len(want) {
		return fmt.Errorf("%s 应有 %d 个字体，实际 %d 个", name, len(want), len(faces))
	}
	for i, f := range faces {
		recs, err := otfont.ParseNameRecords(f.Tables["name"])
		if err != nil {
			return fmt.Errorf("%s #%d name 表无法解析：%w", name, i, err)
		}
		family := ""
		for _, r := range recs {
			if r.PlatformID == 3 && r.LanguageID == 0x0409 && r.NameID == 1 {
				family = r.Value
				break
			}
		}
		if family != want[i].Family {
			return fmt.Errorf("%s #%d 的字体名是 %q，应为 %q；这不是给 %s 用的文件", name, i, family, want[i].Family, name)
		}
		if facts := otfont.ReadFacts(f); facts.WeightClass != want[i].Weight {
			return fmt.Errorf("%s #%d 字重为 %d，应为 %d", name, i, facts.WeightClass, want[i].Weight)
		}
	}
	if name == recipe.OutSegoeUI {
		m, err := otfont.CmapMappings(faces[0].Tables["cmap"])
		if err != nil {
			return fmt.Errorf("segoeui.ttf cmap 无法解析：%w", err)
		}
		for cp := range m {
			if cp >= 0x4E00 && cp <= 0x9FFF {
				return errors.New("segoeui.ttf 包含汉字映射，装上后会打断微软雅黑 UI 的中文回退，拒绝安装")
			}
		}
	}
	return nil
}
