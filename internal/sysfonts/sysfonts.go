// Package sysfonts reports on, and backs up, the six system font files this
// tool replaces.
package sysfonts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/config"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/progress"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
)

// State of one system file.
const (
	StateOriginal  = "original"  // hash equals the Windows 11 golden source
	StateMicrosoft = "microsoft" // Microsoft font, different build
	StateOurs      = "ours"      // made by this tool's Sarasa presets
	StateReplaced  = "replaced"  // carries the Windows identity but not Microsoft glyphs
	StateMissing   = "missing"
	StateUnknown   = "unknown"
)

// FileStatus describes one system font file.
type FileStatus struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`
	State     string `json:"state"`
	Family    string `json:"family,omitempty"`
	Version   string `json:"version,omitempty"`
	Copyright string `json:"copyright,omitempty"`
	Faces     int    `json:"faces,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Status inspects the six target files in dir (normally C:\Windows\Fonts).
func Status(dir string) []FileStatus {
	var out []FileStatus
	for _, name := range recipe.AllOutputs {
		out = append(out, status(dir, name))
	}
	return out
}

// StatusOf inspects one of the six files in any folder (the system Fonts
// folder, or a backup the user points at).
func StatusOf(dir, name string) FileStatus { return status(dir, name) }

// Genuine reports whether a state counts as an original Microsoft file.
func Genuine(state string) bool { return state == StateOriginal || state == StateMicrosoft }

// fromMicrosoft looks for Microsoft in the copyright, trademark,
// manufacturer, designer and vendor-URL records, and in the OS/2 vendor ID.
// SimSun, for example, names its foundry in the copyright and Microsoft only
// in other records.
func fromMicrosoft(f *otfont.Font) bool {
	if os2 := f.Tables["OS/2"]; len(os2) >= 62 && string(os2[58:62]) == "MS  " {
		return true
	}
	recs, err := otfont.ParseNameRecords(f.Tables["name"])
	if err != nil {
		return false
	}
	for _, r := range recs {
		switch r.NameID {
		case 0, 7, 8, 9, 11:
			if strings.Contains(r.Value, "Microsoft") {
				return true
			}
		}
	}
	return false
}

func hasCustomMarker(f *otfont.Font) bool {
	recs, err := otfont.ParseNameRecords(f.Tables["name"])
	if err != nil {
		return false
	}
	for _, r := range recs {
		if r.NameID == 10 && strings.HasPrefix(r.Value, config.CustomMarkerPrefix) {
			return true
		}
	}
	return false
}

func status(dir, name string) FileStatus {
	fs := FileStatus{Name: name, Path: findCaseInsensitive(dir, name)}
	b, err := os.ReadFile(fs.Path)
	if errors.Is(err, os.ErrNotExist) {
		fs.State = StateMissing
		return fs
	}
	if err != nil {
		fs.State, fs.Error = StateUnknown, err.Error()
		return fs
	}
	fs.Size = int64(len(b))
	sum := sha256.Sum256(b)
	fs.SHA256 = hex.EncodeToString(sum[:])
	faces, _, err := otfont.ParseFaces(b)
	if err != nil || len(faces) == 0 {
		fs.State, fs.Error = StateUnknown, fmt.Sprint(err)
		return fs
	}
	fs.Faces = len(faces)
	facts := otfont.ReadFacts(faces[0])
	fs.Family, fs.Version, fs.Copyright = facts.Family, facts.VersionString, facts.Copyright
	switch {
	case config.Windows11OriginalSHA256[name] == fs.SHA256:
		fs.State = StateOriginal
	case config.IsProjectCopyright(facts.Copyright) || hasCustomMarker(faces[0]):
		fs.State = StateOurs
	case fromMicrosoft(faces[0]):
		fs.State = StateMicrosoft
	default:
		fs.State = StateReplaced
	}
	return fs
}

// findCaseInsensitive returns dir/name, matching the on-disk case (the
// Fonts folder stores e.g. "SEGOEUI.TTF" on some installs).
func findCaseInsensitive(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return p
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name())
		}
	}
	return p
}

// Backup copies the six files to a new folder under destRoot, verifying
// each copy by hash, and writes backup.json. It returns the folder.
func Backup(dir, destRoot string, sink progress.Sink) (string, error) {
	stats := Status(dir)
	dest := filepath.Join(destRoot, "系统字体备份-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	var copied []FileStatus
	for i, st := range stats {
		sink.Progress("备份", int64(i), int64(len(stats)))
		if st.State == StateMissing {
			sink.Warnf("%s 不存在，跳过", st.Name)
			continue
		}
		if st.SHA256 == "" {
			return "", fmt.Errorf("%s 无法读取：%s", st.Name, st.Error)
		}
		target := filepath.Join(dest, st.Name)
		if err := copyFile(st.Path, target); err != nil {
			return "", fmt.Errorf("复制 %s 失败：%w", st.Name, err)
		}
		got, err := hashFile(target)
		if err != nil || got != st.SHA256 {
			return "", fmt.Errorf("%s 备份校验失败", st.Name)
		}
		sink.OKf("已备份 %s（%s）", st.Name, describe(st.State))
		copied = append(copied, st)
	}
	sink.Progress("备份", int64(len(stats)), int64(len(stats)))
	b, _ := json.MarshalIndent(map[string]any{"created": time.Now().Format(time.RFC3339), "source": dir, "files": copied}, "", "  ")
	if err := os.WriteFile(filepath.Join(dest, "backup.json"), b, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

func describe(state string) string {
	switch state {
	case StateOriginal:
		return "Windows 11 原版，与金标准哈希一致"
	case StateMicrosoft:
		return "微软原版，但与本项目记录的 Win11 版本不同"
	case StateOurs:
		return "已是本工具生成的替换字体"
	case StateReplaced:
		return "已被其他字体替换"
	}
	return state
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
