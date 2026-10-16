// Package pack turns a build into a self-describing package folder (fonts,
// manifest, checksums, install notes) and an optional ZIP of it.
package pack

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
)

const (
	FontsDir     = "fonts"
	ManifestName = "manifest.json"
	SumsName     = "SHA256SUMS.txt"
	NotesName    = "安装说明.txt"
	// marker identifies folders this tool created, so it never deletes a
	// directory it does not own.
	marker = "yahei-sarasa-go package"
)

// SourceInfo documents one glyph source.
type SourceInfo struct {
	Label   string `json:"label"`
	SHA256  string `json:"sha256,omitempty"`
	Version string `json:"version"`
}

// Manifest is written as manifest.json.
type Manifest struct {
	Tool      string                 `json:"tool"`
	Marker    string                 `json:"marker"`
	Kind      string                 `json:"kind"` // "preset" or "custom"
	Preset    string                 `json:"preset,omitempty"`
	Style     string                 `json:"style,omitempty"`
	Title     string                 `json:"title"`
	Created   string                 `json:"created"`
	Sarasa    map[string]any         `json:"sarasa,omitempty"`
	Slots     map[recipe.Slot]string `json:"slots"`
	Sources   []SourceInfo           `json:"sources"`
	Outputs   []recipe.OutputInfo    `json:"outputs"`
	Golden    string                 `json:"golden"`
	Warnings  []string               `json:"warnings,omitempty"`
	BuildNote string                 `json:"note,omitempty"`
}

var (
	unsafeName = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)
	dashes     = regexp.MustCompile(`-{2,}`)
)

// SafeName makes a folder/file name from free text.
func SafeName(s string) string {
	s = unsafeName.ReplaceAllString(strings.TrimSpace(s), "-")
	s = dashes.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}
	if s == "" {
		s = "fonts"
	}
	return s
}

// Prepare creates root/name/fonts for a new build. An existing folder of
// the same name is replaced only if it is one of ours (has our manifest);
// otherwise a numbered sibling is used.
func Prepare(root, name string) (pkgDir, fontsDir string, err error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", "", err
	}
	pkgDir = filepath.Join(root, name)
	for i := 2; ; i++ {
		st, err := os.Stat(pkgDir)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err == nil && st.IsDir() && owned(pkgDir) {
			if err := os.RemoveAll(pkgDir); err != nil {
				return "", "", fmt.Errorf("无法覆盖已有的同名输出文件夹 %s：%w（是否被资源管理器或其他程序占用？）", pkgDir, err)
			}
			_ = os.Remove(pkgDir + ".zip")
			break
		}
		if i > 100 {
			return "", "", errors.New("too many existing output folders")
		}
		pkgDir = filepath.Join(root, fmt.Sprintf("%s-%d", name, i))
	}
	fontsDir = filepath.Join(pkgDir, FontsDir)
	if err := os.MkdirAll(fontsDir, 0o755); err != nil {
		return "", "", err
	}
	// Write a provisional manifest immediately so even a failed build leaves
	// a folder this tool recognises (and can replace next time).
	m := Manifest{Tool: "yahei-sarasa-go", Marker: marker, Title: "incomplete build"}
	if b, err := json.Marshal(m); err == nil {
		_ = os.WriteFile(filepath.Join(pkgDir, ManifestName), b, 0o644)
	}
	return pkgDir, fontsDir, nil
}

// ReadManifest returns the manifest of a package folder made by this tool.
func ReadManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Marker != marker {
		return nil, errors.New("not a yahei-sarasa-go package")
	}
	return &m, nil
}

// Listed is one package found under an output folder.
type Listed struct {
	Dir     string `json:"dir"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Created string `json:"created"`
	Files   int    `json:"files"`
}

// List finds complete packages directly under root, newest first.
func List(root string) []Listed {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Listed
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		m, err := ReadManifest(dir)
		if err != nil || len(m.Outputs) == 0 {
			continue // not ours, or an interrupted build
		}
		out = append(out, Listed{Dir: dir, Name: e.Name(), Title: m.Title, Kind: m.Kind, Created: m.Created, Files: len(m.Outputs)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}

func owned(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return false
	}
	var m Manifest
	return json.Unmarshal(b, &m) == nil && m.Marker == marker
}

// Finish writes the manifest, checksums and install notes; with zipIt it
// also writes <pkgDir>.zip next to the folder and returns its path.
func Finish(pkgDir string, m Manifest, zipIt bool) (string, error) {
	m.Tool = "yahei-sarasa-go"
	m.Marker = marker
	if m.Created == "" {
		m.Created = time.Now().Format(time.RFC3339)
	}
	m.Golden = "Windows 11 name IDs 1/2/3/4/6/16/17, OS/2 weight + fsSelection, Segoe UI cmap whitelist (see GOLDEN.md)"
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(pkgDir, ManifestName), b, 0o644); err != nil {
		return "", err
	}
	var sums strings.Builder
	for _, o := range m.Outputs {
		fmt.Fprintf(&sums, "%s  %s/%s\n", o.SHA256, FontsDir, o.Name)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, SumsName), []byte(sums.String()), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(pkgDir, NotesName), []byte(InstallNotes(m)), 0o644); err != nil {
		return "", err
	}
	if !zipIt {
		return "", nil
	}
	zipPath := pkgDir + ".zip"
	if err := zipDir(pkgDir, zipPath); err != nil {
		return "", err
	}
	return zipPath, nil
}

func zipDir(dir, zipPath string) error {
	tmp := zipPath + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(out)
	base := filepath.Base(dir)
	modified := time.Now()
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		h := &zip.FileHeader{Name: base + "/" + filepath.ToSlash(rel), Method: zip.Deflate, Modified: modified}
		// Font files are stored, not deflated: they barely compress and
		// storing keeps the ZIP fast to build and to extract.
		if ext := strings.ToLower(filepath.Ext(path)); ext == ".ttc" || ext == ".ttf" {
			h.Method = zip.Store
		}
		h.SetMode(0o644)
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, zipPath)
}

// InstallNotes renders 安装说明.txt (CRLF line endings for Notepad users).
func InstallNotes(m Manifest) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\r\n", args...) }
	w("%s", m.Title)
	w("由 yahei-sarasa-go 生成于 %s", m.Created)
	w("")
	w("一、包含的文件（fonts 文件夹）")
	for _, o := range m.Outputs {
		var faces []string
		for _, f := range o.Faces {
			faces = append(faces, fmt.Sprintf("%s %s %d ← %s", f.Family, f.Subfamily, f.Weight, f.Source))
		}
		w("  %-12s %s", o.Name, strings.Join(faces, "；"))
	}
	w("")
	w("二、替换方法")
	w("  推荐：在本工具的「系统字体」页选中这个包，点「安装到系统…」，确认并授权后重启电脑。")
	w("  重启时 Windows 会先换上新字体、再清空字体缓存；安装前会把当前的字体照原样备份，")
	w("  之后可在同一页「还原备份」。命令行（管理员终端）：yahei-sarasa-cli system install 本文件夹")
	w("")
	w("  手动方式：")
	w("  这些文件与 C:\\Windows\\Fonts 中的同名系统字体一一对应，用它们覆盖同名文件即可。")
	w("  系统字体受 TrustedInstaller 保护且一直被占用，Windows 运行时无法直接覆盖，")
	w("  通常在 WinPE / 恢复环境的命令行中复制，或使用你惯用的替换方式。")
	w("  覆盖前务必备份当前文件（「系统字体」页的「把当前 6 个文件另存一份」可以一键复制）。")
	w("  替换后重启。如果界面字体显示异常，可清理字体缓存后再重启：")
	w("    C:\\Windows\\ServiceProfiles\\LocalService\\AppData\\Local\\FontCache 下的缓存文件")
	w("    C:\\Windows\\System32\\FNTCACHE.DAT")
	w("")
	w("三、还原")
	w("  用本工具安装的：「系统字体」页 →「还原备份…」，选一份备份，重启生效。")
	w("  手动替换的：用备份的原版文件按同样方式覆盖回去。")
	w("")
	w("四、校验")
	w("  SHA256SUMS.txt 记录了每个字体文件的 SHA-256，manifest.json 记录了来源与每个 face 的身份。")
	w("  PowerShell：Get-FileHash .\\fonts\\msyh.ttc -Algorithm SHA256")
	if len(m.Warnings) > 0 {
		w("")
		w("五、生成时的提示")
		for _, s := range m.Warnings {
			w("  - %s", s)
		}
	}
	return b.String()
}
