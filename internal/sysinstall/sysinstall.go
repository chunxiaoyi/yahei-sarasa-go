// Package sysinstall installs a font package into the Windows Fonts folder
// and restores earlier states.
//
// The six target files are always in use (the kernel and the font cache
// service hold them open), so nothing is overwritten while Windows runs.
// Instead the new files are staged in a protected folder and registered with
// the Session Manager (MoveFileEx with MOVEFILE_DELAY_UNTIL_REBOOT). At the
// next boot, before any service starts, Windows swaps the files in and then
// deletes the font caches (the FontCache folder of the Windows Font Cache
// Service and System32\FNTCACHE.DAT), so the caches are rebuilt from the new
// fonts.
//
// Backups: before anything replaces the system fonts, the six current files
// are saved exactly as they are, whatever they are. Files that are not
// Microsoft originals are the valuable ones (a font someone installed by
// hand may be hard to find again, while the originals can always be taken
// from a Windows image), so they are backed up with a note rather than
// refused. Backups are content-addressed (one copy per distinct file), every
// pre-change state is kept as a snapshot, and nothing is ever deleted
// unless the user asks.
//
// Safety rules:
//   - Every package file is checked twice (in the package and again after
//     staging): hash, and that each face carries the Windows identity of its
//     file name.
//   - Staged files get the exact owner and permissions of the system file
//     they replace (TrustedInstaller, read-only for everyone else).
//   - The data folder is created with a protected DACL and refused if it is
//     a link or someone else owns it, so files the elevated helper installs
//     cannot be planted by a non-administrator.
//   - Any failure before the operations are registered rolls back
//     completely; a pending operation can be cancelled before restarting.
package sysinstall

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/sysfonts"
)

var (
	ErrUnsupported = errors.New("自动安装只能在 Windows 上使用")
	ErrNotAdmin    = errors.New("需要以管理员身份运行")
)

// Logf receives progress lines.
type Logf func(format string, args ...any)

// OS is the privileged surface, implemented for Windows in os_windows.go.
type OS interface {
	Supported() bool
	IsAdmin() bool
	// EnablePrivileges turns on SeBackupPrivilege, SeRestorePrivilege and
	// SeTakeOwnershipPrivilege for this process.
	EnablePrivileges() error
	// SecureDataDir creates dir if needed, refuses it when it is a reparse
	// point or not owned by Administrators/SYSTEM, and applies a protected
	// DACL: SYSTEM and Administrators full control, Users read.
	SecureDataDir(dir string) error
	// ReadSecurity returns the owner and DACL of path as SDDL.
	ReadSecurity(path string) (string, error)
	// WriteSecurity sets owner and DACL from SDDL (DACL protected).
	WriteSecurity(path, sddl string) error
	// ListFiles lists regular files under dir recursively, using backup
	// semantics so folders closed to Administrators can still be read.
	ListFiles(dir string) ([]string, error)
	ScheduleReplace(src, dst string) error
	ScheduleDelete(path string) error
	PendingOps() ([]PendingOp, error)
	SetPendingOps([]PendingOp) error
}

// Paths are the locations the installer works with.
type Paths struct {
	FontsDir string `json:"fontsDir"` // %SystemRoot%\Fonts
	CacheDir string `json:"cacheDir"` // ...\ServiceProfiles\LocalService\AppData\Local\FontCache
	FntCache string `json:"fntCache"` // %SystemRoot%\System32\FNTCACHE.DAT
	DataDir  string `json:"dataDir"`  // %ProgramData%\yahei-sarasa-go
}

func (p Paths) BackupsDir() string   { return filepath.Join(p.DataDir, "backups") }
func (p Paths) ResultsDir() string   { return filepath.Join(p.DataDir, "results") }
func (p Paths) blobsDir() string     { return filepath.Join(p.BackupsDir(), "files") }
func (p Paths) snapshotsDir() string { return filepath.Join(p.BackupsDir(), "snapshots") }
func (p Paths) stagingRoot() string  { return filepath.Join(p.DataDir, "staging") }
func (p Paths) statePath() string    { return filepath.Join(p.DataDir, "state.json") }

// target is the system file for name, matching the case used on disk.
func (p Paths) target(name string) string {
	if t := findName(p.FontsDir, name); t != "" {
		return t
	}
	return filepath.Join(p.FontsDir, name)
}

// ---- records -----------------------------------------------------------------

// FileRecord describes one backed-up file. An empty SHA256 means the file
// did not exist, so restoring that state removes it.
type FileRecord struct {
	SHA256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size,omitempty"`
	State     string `json:"state,omitempty"`
	Version   string `json:"version,omitempty"`
	Copyright string `json:"copyright,omitempty"`
}

// Installed records what the system files should be after the pending (or
// last) operation took effect.
type Installed struct {
	Kind    string            `json:"kind"` // "package" or "snapshot"
	Package string            `json:"package,omitempty"`
	Title   string            `json:"title,omitempty"`
	Before  string            `json:"before,omitempty"` // snapshot taken right before
	Files   map[string]string `json:"files"`            // name -> sha256; "" = must be absent
	Time    string            `json:"time"`
}

// Pending records operations registered for the next restart.
type Pending struct {
	Action     string            `json:"action"` // install, restore, clear-cache
	Staging    string            `json:"staging,omitempty"`
	Ops        []PendingOp       `json:"ops"`
	SavedSD    map[string]string `json:"savedSD,omitempty"` // target -> SDDL before SYSTEM was granted delete
	Previous   *Installed        `json:"previous,omitempty"`
	CacheFiles int               `json:"cacheFiles"`
	Time       string            `json:"time"`
}

// State is state.json.
type State struct {
	Installed *Installed `json:"installed,omitempty"`
	Pending   *Pending   `json:"pending,omitempty"`
}

func now() string { return time.Now().Format(time.RFC3339) }

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadState returns the state, or an empty one.
func LoadState(p Paths) (*State, error) {
	var st State
	if err := readJSON(p.statePath(), &st); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("读取 %s：%w", p.statePath(), err)
	}
	return &st, nil
}

// ---- hashing & copying -------------------------------------------------------

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// copyVerified copies src to a new file dst and checks the copy's hash.
func copyVerified(src, dst, wantSHA string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); wantSHA != "" && got != wantSHA {
		os.Remove(dst)
		return fmt.Errorf("%s 复制后校验不一致（复制过程中文件被改动？）", filepath.Base(src))
	}
	return nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func matches(sha, want string) bool {
	if want == "" {
		return sha == ""
	}
	return sha == want
}

func stateLabel(s string) string {
	switch s {
	case sysfonts.StateOriginal:
		return "Windows 11 原版"
	case sysfonts.StateMicrosoft:
		return "微软原版，其他版本"
	case sysfonts.StateOurs:
		return "本工具生成"
	case sysfonts.StateReplaced:
		return "其他字体，非原版"
	case sysfonts.StateMissing:
		return "不存在"
	}
	return "无法读取"
}

func actionLabel(a string) string {
	switch a {
	case "install":
		return "安装字体包"
	case "restore":
		return "还原备份"
	case "clear-cache":
		return "清空字体缓存"
	}
	return a
}

// ---- helper results -------------------------------------------------------------

var resultIDPattern = regexp.MustCompile(`^[0-9a-f]{16,64}$`)

// ValidResultID guards the result-file name passed to the elevated helper.
func ValidResultID(id string) bool { return resultIDPattern.MatchString(id) }

// Result is what the elevated helper reports back to the GUI.
type Result struct {
	OK    bool     `json:"ok"`
	Error string   `json:"error,omitempty"`
	Log   []string `json:"log"`
	Time  string   `json:"time"`
}

// WriteResult stores the helper's result in the protected results folder.
func WriteResult(sys OS, p Paths, id string, r Result) error {
	if !ValidResultID(id) {
		return errors.New("invalid result id")
	}
	if err := sys.SecureDataDir(p.DataDir); err != nil {
		return err
	}
	if err := os.MkdirAll(p.ResultsDir(), 0o755); err != nil {
		return err
	}
	r.Time = now()
	return writeJSON(filepath.Join(p.ResultsDir(), id+".json"), r)
}

// ReadResult returns the helper's result (the GUI cannot delete it without
// admin rights; the helper prunes old ones).
func ReadResult(p Paths, id string) (*Result, error) {
	if !ValidResultID(id) {
		return nil, errors.New("invalid result id")
	}
	var r Result
	if err := readJSON(filepath.Join(p.ResultsDir(), id+".json"), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// cleanupResults drops helper results older than a day.
func cleanupResults(p Paths) {
	entries, err := os.ReadDir(p.ResultsDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(p.ResultsDir(), e.Name()))
		}
	}
}
