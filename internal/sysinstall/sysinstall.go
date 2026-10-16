// Package sysinstall installs a font package into the Windows Fonts folder
// and restores the originals.
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
// Safety rules:
//   - Nothing is installed until a verified copy of the Windows originals
//     exists, taken from the current system files when they are genuine, or
//     imported from a backup folder.
//   - Every file is checked twice (in the package and again after staging):
//     hash, and that each face carries the Windows identity of its file name.
//   - Staged files get the exact owner and permissions of the system file
//     they replace (TrustedInstaller, read-only for everyone else).
//   - The data folder is created with a protected DACL and refused if
//     someone else owns it, so files the elevated helper installs cannot be
//     planted by a non-administrator.
//   - Any failure before the operations are registered rolls back
//     completely; a pending install can be cancelled before restarting.
package sysinstall

import (
	"bufio"
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
	"sort"
	"strings"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/pack"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
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

func (p Paths) OriginalsDir() string    { return filepath.Join(p.DataDir, "originals") }
func (p Paths) ResultsDir() string      { return filepath.Join(p.DataDir, "results") }
func (p Paths) stagingRoot() string     { return filepath.Join(p.DataDir, "staging") }
func (p Paths) statePath() string       { return filepath.Join(p.DataDir, "state.json") }
func (p Paths) originalsRecord() string { return filepath.Join(p.OriginalsDir(), "originals.json") }

// target is the system file for name, matching the case used on disk.
func (p Paths) target(name string) string {
	if t := findName(p.FontsDir, name); t != "" {
		return t
	}
	return filepath.Join(p.FontsDir, name)
}

// ---- records -----------------------------------------------------------------

// FileRecord describes one original file. An empty SHA256 means the file did
// not exist, so restoring removes it.
type FileRecord struct {
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size,omitempty"`
	State   string `json:"state,omitempty"`
	Version string `json:"version,omitempty"`
}

// Originals is originals/originals.json.
type Originals struct {
	Created string                `json:"created"`
	Source  string                `json:"source"` // "system" or the imported backup folder
	Files   map[string]FileRecord `json:"files"`
}

// Installed records what the system files should be after the pending (or
// last) operation took effect.
type Installed struct {
	Kind    string            `json:"kind"` // "package" or "originals"
	Package string            `json:"package,omitempty"`
	Title   string            `json:"title,omitempty"`
	Files   map[string]string `json:"files"` // name -> sha256; "" = must be absent
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

// LoadOriginals returns the originals record, or nil if none was made.
func LoadOriginals(p Paths) (*Originals, error) {
	var o Originals
	if err := readJSON(p.originalsRecord(), &o); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取原版备份记录：%w", err)
	}
	return &o, nil
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
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); wantSHA != "" && got != wantSHA {
		return fmt.Errorf("%s 复制后校验不一致", filepath.Base(dst))
	}
	return nil
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var resultIDPattern = regexp.MustCompile(`^[0-9a-f]{16,64}$`)

// ValidResultID guards the result-file name passed to the elevated helper.
func ValidResultID(id string) bool { return resultIDPattern.MatchString(id) }

// ---- package verification ----------------------------------------------------------

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

// ---- originals ------------------------------------------------------------------

// OriginalsEntry says where the original of one file comes from.
type OriginalsEntry struct {
	Name    string `json:"name"`
	From    string `json:"from"` // system, import, absent, none
	Path    string `json:"path,omitempty"`
	State   string `json:"state,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Version string `json:"version,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// planOriginals decides, per file, where a new originals backup comes from:
// the system file when it is a genuine Microsoft file, otherwise the import
// folder when it holds a genuine copy. A file the system does not have is
// recorded as absent.
func planOriginals(p Paths, importDir string) ([]OriginalsEntry, []string) {
	var out []OriginalsEntry
	var problems []string
	for _, name := range recipe.AllOutputs {
		sys := sysfonts.StatusOf(p.FontsDir, name)
		e := OriginalsEntry{Name: name, State: sys.State}
		switch {
		case sysfonts.Genuine(sys.State):
			e.From, e.Path, e.SHA256, e.Size, e.Version = "system", sys.Path, sys.SHA256, sys.Size, sys.Version
		case sys.State == sysfonts.StateMissing:
			e.From = "absent"
		case importDir != "":
			imp := sysfonts.StatusOf(importDir, name)
			if sysfonts.Genuine(imp.State) {
				e.From, e.Path, e.State, e.SHA256, e.Size, e.Version = "import", imp.Path, imp.State, imp.SHA256, imp.Size, imp.Version
			} else {
				e.From = "none"
				e.Problem = fmt.Sprintf("系统里的 %s 已不是微软原版（%s），导入的文件夹里也没有它的原版", name, stateLabel(sys.State))
			}
		default:
			e.From = "none"
			e.Problem = fmt.Sprintf("系统里的 %s 已不是微软原版（%s），无法作为原版备份", name, stateLabel(sys.State))
		}
		if e.Problem != "" {
			problems = append(problems, e.Problem)
		}
		out = append(out, e)
	}
	return out, problems
}

func stateLabel(s string) string {
	switch s {
	case sysfonts.StateOriginal:
		return "Windows 11 原版"
	case sysfonts.StateMicrosoft:
		return "微软原版，其他版本"
	case sysfonts.StateOurs:
		return "本工具生成的替换字体"
	case sysfonts.StateReplaced:
		return "已被其他字体替换"
	case sysfonts.StateMissing:
		return "不存在"
	}
	return "无法读取"
}

// verifyOriginals checks that every recorded original is present and intact.
func verifyOriginals(p Paths, o *Originals) error {
	for _, name := range recipe.AllOutputs {
		rec, ok := o.Files[name]
		if !ok {
			return fmt.Errorf("原版备份记录缺少 %s", name)
		}
		if rec.SHA256 == "" {
			continue
		}
		got, _, err := hashFile(filepath.Join(p.OriginalsDir(), name))
		if err != nil {
			return fmt.Errorf("原版备份里的 %s 读不到：%w", name, err)
		}
		if got != rec.SHA256 {
			return fmt.Errorf("原版备份里的 %s 已损坏（哈希不一致）", name)
		}
	}
	return nil
}

// ensureOriginals returns the verified originals backup, creating it first
// when there is none.
func ensureOriginals(p Paths, importDir string, log Logf) (*Originals, error) {
	o, err := LoadOriginals(p)
	if err != nil {
		return nil, err
	}
	if o != nil {
		if err := verifyOriginals(p, o); err != nil {
			return nil, err
		}
		log("原版备份已存在（%s 建立），校验通过", o.Created)
		return o, nil
	}
	entries, problems := planOriginals(p, importDir)
	if len(problems) > 0 {
		return nil, fmt.Errorf("没有可用的原版备份，不能安装：%s。请在「系统字体」页选择一个保存着原版的备份文件夹导入", strings.Join(problems, "；"))
	}
	dir := p.OriginalsDir()
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	o = &Originals{Created: now(), Source: "system", Files: map[string]FileRecord{}}
	if importDir != "" {
		o.Source = importDir
	}
	for _, e := range entries {
		rec := FileRecord{State: e.State}
		if e.From == "system" || e.From == "import" {
			if err := copyVerified(e.Path, filepath.Join(dir, e.Name), e.SHA256); err != nil {
				return nil, fmt.Errorf("备份原版 %s 失败：%w", e.Name, err)
			}
			rec.SHA256, rec.Size, rec.Version = e.SHA256, e.Size, e.Version
			log("已备份原版 %s（来自%s，%s）", e.Name, map[string]string{"system": "系统", "import": "导入的文件夹"}[e.From], stateLabel(e.State))
		} else {
			log("系统里没有 %s，记为不存在；还原时会删除装上去的这个文件", e.Name)
		}
		o.Files[e.Name] = rec
	}
	if err := writeJSON(p.originalsRecord(), o); err != nil {
		return nil, err
	}
	log("原版备份保存在 %s", dir)
	return o, nil
}

// ---- plans (dry run, no privileges needed) -------------------------------------

// PlanFile is one file an operation will change.
type PlanFile struct {
	Name    string `json:"name"`
	Current string `json:"current"` // system file state
	After   string `json:"after"`   // what it becomes
}

// Plan describes an operation before it runs. The elevated helper repeats
// every check itself; a plan is only for showing the user.
type Plan struct {
	Action        string           `json:"action"`
	Package       *Package         `json:"package,omitempty"`
	Files         []PlanFile       `json:"files"`
	Originals     []OriginalsEntry `json:"originals,omitempty"`
	HaveOriginals bool             `json:"haveOriginals"`
	Steps         []string         `json:"steps"`
	Warnings      []string         `json:"warnings,omitempty"`
	Blockers      []string         `json:"blockers,omitempty"`
	NeedsImport   bool             `json:"needsImport,omitempty"`
}

func (p Paths) cacheSteps() []string {
	return []string{
		"删除字体缓存：" + p.CacheDir + " 里的全部文件，以及 " + p.FntCache,
		"重启后 Windows 会用新字体重建缓存，第一次开机可能稍慢",
	}
}

// PlanInstall describes installing the package in pkgDir.
func PlanInstall(sys OS, p Paths, pkgDir, importDir string) Plan {
	plan := Plan{Action: "install"}
	if !sys.Supported() {
		plan.Blockers = append(plan.Blockers, ErrUnsupported.Error())
		return plan
	}
	pkg, err := VerifyPackage(pkgDir)
	if err != nil {
		plan.Blockers = append(plan.Blockers, err.Error())
		return plan
	}
	plan.Package = pkg
	plan.Warnings = append(plan.Warnings, pkg.Notes...)
	for _, f := range pkg.Files {
		cur := sysfonts.StatusOf(p.FontsDir, f.Name)
		after := "字体包里的版本"
		if cur.SHA256 == f.SHA256 {
			after = "不变（已经是这个版本）"
		}
		plan.Files = append(plan.Files, PlanFile{Name: f.Name, Current: cur.State, After: after})
	}
	o, err := LoadOriginals(p)
	switch {
	case err != nil:
		plan.Blockers = append(plan.Blockers, err.Error())
	case o != nil:
		plan.HaveOriginals = true
		if err := verifyOriginals(p, o); err != nil {
			plan.Blockers = append(plan.Blockers, err.Error())
		}
	default:
		entries, problems := planOriginals(p, importDir)
		plan.Originals = entries
		if len(problems) > 0 {
			plan.Blockers = append(plan.Blockers, problems...)
			plan.NeedsImport = true
		}
	}
	st, _ := LoadState(p)
	if st != nil && st.Pending != nil && pendingActive(sys, st) {
		plan.Warnings = append(plan.Warnings, "已有一个待重启生效的操作（"+actionLabel(st.Pending.Action)+"），会先取消它")
	}
	if !plan.HaveOriginals && !plan.NeedsImport {
		plan.Steps = append(plan.Steps, "先把当前系统里的原版字体复制到 "+p.OriginalsDir()+" 并校验，用于以后还原")
	}
	plan.Steps = append(plan.Steps,
		"把字体包复制到受保护的暂存文件夹，再次校验哈希与字体身份",
		"让新文件沿用系统原文件的所有者与权限（TrustedInstaller）",
		"登记「下次启动时替换」：重启时 Windows 在加载字体之前换上新文件")
	plan.Steps = append(plan.Steps, p.cacheSteps()...)
	plan.Steps = append(plan.Steps, "重启前都可以取消；之后随时可以一键还原原版")
	return plan
}

// PlanRestore describes restoring the originals.
func PlanRestore(sys OS, p Paths) Plan {
	plan := Plan{Action: "restore"}
	if !sys.Supported() {
		plan.Blockers = append(plan.Blockers, ErrUnsupported.Error())
		return plan
	}
	o, err := LoadOriginals(p)
	if err != nil {
		plan.Blockers = append(plan.Blockers, err.Error())
		return plan
	}
	if o == nil {
		plan.Blockers = append(plan.Blockers, "还没有原版备份：本工具从未在这台电脑上安装过字体，没有可还原的内容")
		return plan
	}
	plan.HaveOriginals = true
	if err := verifyOriginals(p, o); err != nil {
		plan.Blockers = append(plan.Blockers, err.Error())
		return plan
	}
	for _, name := range recipe.AllOutputs {
		rec := o.Files[name]
		cur := sysfonts.StatusOf(p.FontsDir, name)
		switch {
		case rec.SHA256 == "" && cur.State != sysfonts.StateMissing:
			plan.Files = append(plan.Files, PlanFile{Name: name, Current: cur.State, After: "删除（原本没有这个文件）"})
		case rec.SHA256 != "" && cur.SHA256 != rec.SHA256:
			plan.Files = append(plan.Files, PlanFile{Name: name, Current: cur.State, After: "原版"})
		}
	}
	if len(plan.Files) == 0 {
		plan.Warnings = append(plan.Warnings, "系统字体已经是原版，不需要还原")
	}
	plan.Steps = append(plan.Steps,
		"从 "+p.OriginalsDir()+" 取出原版并校验",
		"登记「下次启动时替换」，重启时换回原版")
	plan.Steps = append(plan.Steps, p.cacheSteps()...)
	return plan
}

// PlanClearCache describes clearing the font caches at the next restart.
func PlanClearCache(sys OS, p Paths) Plan {
	plan := Plan{Action: "clear-cache"}
	if !sys.Supported() {
		plan.Blockers = append(plan.Blockers, ErrUnsupported.Error())
		return plan
	}
	plan.Steps = append(plan.Steps, "登记「下次启动时删除」：")
	plan.Steps = append(plan.Steps, p.cacheSteps()...)
	return plan
}

func actionLabel(a string) string {
	switch a {
	case "install":
		return "安装字体包"
	case "restore":
		return "还原原版"
	case "clear-cache":
		return "清空字体缓存"
	}
	return a
}

// ---- status ----------------------------------------------------------------------

// FileReport is one system file compared with the records.
type FileReport struct {
	sysfonts.FileStatus
	Original         *FileRecord `json:"original,omitempty"`
	Expected         *string     `json:"expected,omitempty"` // sha256 the last operation set up ("" = absent)
	MatchesOriginal  bool        `json:"matchesOriginal"`
	MatchesInstalled bool        `json:"matchesInstalled"`
}

// Report is the state shown on the system-font page.
type Report struct {
	Supported     bool         `json:"supported"`
	Admin         bool         `json:"admin"`
	Paths         Paths        `json:"paths"`
	Files         []FileReport `json:"files"`
	Originals     *Originals   `json:"originals,omitempty"`
	State         *State       `json:"state,omitempty"`
	PendingActive bool         `json:"pendingActive"`
	PendingCount  int          `json:"pendingCount"`
	Phase         string       `json:"phase"` // none, pending, applied, original, failed, modified
	Message       string       `json:"message"`
	Error         string       `json:"error,omitempty"`
}

func pendingActive(sys OS, st *State) bool {
	if st == nil || st.Pending == nil {
		return false
	}
	ops, err := sys.PendingOps()
	return err == nil && countPresent(ops, st.Pending.Ops) > 0
}

func matches(sha string, want string) bool {
	if want == "" {
		return sha == ""
	}
	return sha == want
}

// GetStatus needs no privileges.
func GetStatus(sys OS, p Paths) Report {
	r := Report{Supported: sys.Supported(), Paths: p, Phase: "none"}
	if !r.Supported {
		return r
	}
	r.Admin = sys.IsAdmin()
	o, err := LoadOriginals(p)
	if err != nil {
		r.Error = err.Error()
	}
	r.Originals = o
	st, err := LoadState(p)
	if err != nil {
		r.Error = err.Error()
		st = &State{}
	}
	r.State = st
	if st.Pending != nil {
		if ops, err := sys.PendingOps(); err == nil {
			r.PendingCount = countPresent(ops, st.Pending.Ops)
			r.PendingActive = r.PendingCount > 0
		}
	}

	allInstalled, allOriginal := st.Installed != nil, o != nil
	for _, name := range recipe.AllOutputs {
		fr := FileReport{FileStatus: sysfonts.StatusOf(p.FontsDir, name)}
		if o != nil {
			if rec, ok := o.Files[name]; ok {
				rec := rec
				fr.Original = &rec
				fr.MatchesOriginal = matches(fr.SHA256, rec.SHA256)
			}
			if !fr.MatchesOriginal {
				allOriginal = false
			}
		}
		if st.Installed != nil {
			if want, ok := st.Installed.Files[name]; ok {
				want := want
				fr.Expected = &want
				fr.MatchesInstalled = matches(fr.SHA256, want)
				if !fr.MatchesInstalled {
					allInstalled = false
				}
			}
		}
		r.Files = append(r.Files, fr)
	}

	switch {
	case r.PendingActive:
		r.Phase = "pending"
		r.Message = "已安排在下次重启时" + actionLabel(st.Pending.Action) + "，重启后生效"
		if st.Pending.Action != "clear-cache" {
			r.Message += "（同时清空字体缓存）"
		}
	case st.Installed != nil && allInstalled && st.Installed.Kind == "originals":
		r.Phase, r.Message = "original", "系统字体是原版（已由本工具还原）"
	case st.Installed != nil && allInstalled:
		r.Phase = "applied"
		r.Message = "已安装：" + firstNonEmpty(st.Installed.Title, st.Installed.Package)
	case st.Installed != nil && st.Pending != nil:
		r.Phase = "failed"
		r.Message = "上次安排的" + actionLabel(st.Pending.Action) + "在重启后没有生效。可能是还没重启、重启时被安全软件拦截，或系统还原了文件"
	case st.Installed != nil:
		r.Phase = "modified"
		r.Message = "系统字体在安装之后被改动过（例如 Windows 更新或 sfc 修复换回了原版）"
	case o != nil && allOriginal:
		r.Phase, r.Message = "original", "系统字体是原版"
	default:
		r.Message = "还没有用本工具安装过字体"
	}
	return r
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---- privileged operations ----------------------------------------------------------

// prepare is the common start of every privileged operation.
func prepare(sys OS, p Paths, log Logf) (*State, error) {
	if !sys.Supported() {
		return nil, ErrUnsupported
	}
	if !sys.IsAdmin() {
		return nil, ErrNotAdmin
	}
	if err := sys.EnablePrivileges(); err != nil {
		return nil, fmt.Errorf("无法启用备份/还原权限：%w", err)
	}
	if err := sys.SecureDataDir(p.DataDir); err != nil {
		return nil, err
	}
	for _, d := range []string{p.OriginalsDir(), p.stagingRoot(), p.ResultsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	st, err := LoadState(p)
	if err != nil {
		return nil, err
	}
	// A pending record whose operations are all gone was carried out by a
	// restart (or removed by someone else): forget it and its staging. If
	// the list cannot be read, stop rather than guess: deleting the staging
	// folder of a still-registered install would break it.
	if st.Pending != nil {
		ops, err := sys.PendingOps()
		if err != nil {
			return nil, fmt.Errorf("读取待重启操作失败：%w", err)
		}
		if countPresent(ops, st.Pending.Ops) > 0 {
			cleanupStaging(p, st)
			cleanupResults(p)
			return st, nil
		}
		removeStaging(p, st.Pending.Staging)
		st.Pending = nil
		if err := writeJSON(p.statePath(), st); err != nil {
			return nil, err
		}
	}
	cleanupStaging(p, st)
	cleanupResults(p)
	return st, nil
}

// cleanupResults drops helper results older than a day (the GUI, running
// without admin rights, cannot delete them itself).
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

// removeStaging deletes a staging folder, but only inside our staging root.
func removeStaging(p Paths, dir string) {
	if dir == "" {
		return
	}
	rel, err := filepath.Rel(p.stagingRoot(), dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return
	}
	_ = os.RemoveAll(dir)
}

// cleanupStaging removes leftover staging folders not referenced by state.
func cleanupStaging(p Paths, st *State) {
	entries, err := os.ReadDir(p.stagingRoot())
	if err != nil {
		return
	}
	for _, e := range entries {
		dir := filepath.Join(p.stagingRoot(), e.Name())
		if st.Pending != nil && strings.EqualFold(dir, st.Pending.Staging) {
			continue
		}
		removeStaging(p, dir)
	}
}

// fallbackFontSD mirrors the permissions of an inbox Windows 11 font file,
// used only when no system file exists to copy them from.
const fallbackFontSD = "O:S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464" +
	"D:P(A;;FA;;;S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464)" +
	"(A;;0x1200a9;;;SY)(A;;0x1200a9;;;BA)(A;;0x1200a9;;;BU)(A;;0x1200a9;;;AC)(A;;0x1200a9;;;S-1-15-2-2)"

var aceFlagsID = regexp.MustCompile(`\(([^;()]*);([^;()]*);`)

// explicitSDDL turns inherited ACEs into explicit ones and protects the
// DACL, so the permissions survive the move into the Fonts folder exactly.
func explicitSDDL(sddl string) string {
	i := strings.Index(sddl, "D:")
	if i < 0 {
		return sddl
	}
	j := strings.Index(sddl[i:], "(")
	if j < 0 {
		return sddl[:i] + "D:P"
	}
	head := sddl[:i] + "D:P"
	aces := aceFlagsID.ReplaceAllStringFunc(sddl[i+j:], func(m string) string {
		parts := aceFlagsID.FindStringSubmatch(m)
		flags := strings.ReplaceAll(parts[2], "ID", "")
		return "(" + parts[1] + ";" + flags + ";"
	})
	return head + aces
}

// withSystemFull adds SYSTEM full control, so the boot-time replacement is
// allowed to delete the old file whatever its original permissions.
func withSystemFull(sddl string) string {
	s := explicitSDDL(sddl)
	i := strings.Index(s, "D:P")
	if i < 0 {
		return s
	}
	return s[:i+3] + "(A;;FA;;;SY)" + s[i+3:]
}

type stagedFile struct {
	name   string
	staged string // "" = delete the target
	sha    string
}

// schedule registers the boot-time operations. On any error everything it
// changed is put back.
func schedule(sys OS, p Paths, st *State, action string, files []stagedFile, staging string, installed *Installed, log Logf) (err error) {
	saved := map[string]string{}
	var before []PendingOp
	haveBefore := false
	defer func() {
		if err == nil {
			return
		}
		// Remove whatever this call registered, even if it failed half-way.
		if haveBefore {
			if cur, e := sys.PendingOps(); e == nil {
				if mine := diffOps(before, cur); len(mine) > 0 {
					_ = sys.SetPendingOps(removeOps(cur, mine))
				}
			}
		}
		for target, sd := range saved {
			_ = sys.WriteSecurity(target, sd)
		}
		removeStaging(p, staging)
		log("已撤销本次的全部改动")
	}()

	// Permissions for the new files: copy them from the file being replaced,
	// or from any existing system font when the target does not exist yet.
	template := ""
	for _, name := range recipe.AllOutputs {
		if t := p.target(name); fileExists(t) {
			if sd, e := sys.ReadSecurity(t); e == nil {
				template = sd
				break
			}
		}
	}
	if template == "" {
		template = fallbackFontSD
	}
	for _, f := range files {
		if f.staged == "" {
			continue
		}
		sd := template
		if t := p.target(f.name); fileExists(t) {
			if sd, err = sys.ReadSecurity(t); err != nil {
				return fmt.Errorf("读取 %s 的权限失败：%w", t, err)
			}
		}
		if err = sys.WriteSecurity(f.staged, explicitSDDL(sd)); err != nil {
			return fmt.Errorf("设置 %s 的权限失败：%w", f.name, err)
		}
	}

	// Let SYSTEM delete the old files at boot (they are replaced anyway; the
	// previous permissions are kept for cancelling).
	for _, f := range files {
		t := p.target(f.name)
		if !fileExists(t) {
			continue
		}
		sd, e := sys.ReadSecurity(t)
		if e != nil {
			return fmt.Errorf("读取 %s 的权限失败：%w", t, e)
		}
		if err = sys.WriteSecurity(t, withSystemFull(sd)); err != nil {
			return fmt.Errorf("调整 %s 的权限失败：%w", t, err)
		}
		saved[t] = sd
	}

	if before, err = sys.PendingOps(); err != nil {
		return fmt.Errorf("读取待重启操作失败：%w", err)
	}
	haveBefore = true
	expected := 0
	for _, f := range files {
		t := filepath.Join(p.FontsDir, f.name)
		if existing := p.target(f.name); fileExists(existing) {
			t = existing
		}
		if f.staged == "" {
			if !fileExists(t) {
				continue
			}
			if err = sys.ScheduleDelete(t); err != nil {
				return fmt.Errorf("登记删除 %s 失败：%w", f.name, err)
			}
			log("已登记：重启时删除 %s", f.name)
		} else {
			if err = sys.ScheduleReplace(f.staged, t); err != nil {
				return fmt.Errorf("登记替换 %s 失败：%w", f.name, err)
			}
			log("已登记：重启时替换 %s", f.name)
		}
		expected++
	}

	cache := cacheFiles(sys, p, log)
	for _, c := range cache {
		if err = sys.ScheduleDelete(c); err != nil {
			return fmt.Errorf("登记删除字体缓存 %s 失败：%w", c, err)
		}
		expected++
	}
	log("已登记：重启时删除 %d 个字体缓存文件", len(cache))

	after, err := sys.PendingOps()
	if err != nil {
		return fmt.Errorf("读取待重启操作失败：%w", err)
	}
	added := diffOps(before, after)
	if len(added) != expected {
		return fmt.Errorf("系统只记下了 %d / %d 个待重启操作", len(added), expected)
	}

	st.Pending = &Pending{Action: action, Staging: staging, Ops: added, SavedSD: saved, Previous: st.Installed, CacheFiles: len(cache), Time: now()}
	if installed != nil {
		st.Installed = installed
	}
	if err = writeJSON(p.statePath(), st); err != nil {
		return err
	}
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// cacheFiles lists everything to delete for a clean font cache rebuild.
func cacheFiles(sys OS, p Paths, log Logf) []string {
	var out []string
	files, err := sys.ListFiles(p.CacheDir)
	switch {
	case err == nil:
		out = append(out, files...)
	case errors.Is(err, os.ErrNotExist):
		log("没有找到 %s，跳过", p.CacheDir)
	default:
		log("无法列出 %s：%v（只清 FNTCACHE.DAT）", p.CacheDir, err)
	}
	if fileExists(p.FntCache) {
		out = append(out, p.FntCache)
	}
	sort.Strings(out)
	return out
}

// cancelPending removes our registered operations and undoes their side
// effects.
func cancelPending(sys OS, p Paths, st *State, log Logf) error {
	pd := st.Pending
	if pd == nil {
		return nil
	}
	cur, err := sys.PendingOps()
	if err != nil {
		return fmt.Errorf("读取待重启操作失败：%w", err)
	}
	if err := sys.SetPendingOps(removeOps(cur, pd.Ops)); err != nil {
		return fmt.Errorf("移除待重启操作失败：%w", err)
	}
	for target, sd := range pd.SavedSD {
		if fileExists(target) {
			if err := sys.WriteSecurity(target, sd); err != nil {
				log("恢复 %s 的权限失败：%v", target, err)
			}
		}
	}
	removeStaging(p, pd.Staging)
	st.Installed = pd.Previous
	st.Pending = nil
	log("已取消待重启的%s", actionLabel(pd.Action))
	return writeJSON(p.statePath(), st)
}

// newStaging creates a fresh staging folder inside the protected data dir,
// on the same volume as the Fonts folder (required for the boot-time rename).
func newStaging(p Paths) (string, error) {
	if !strings.EqualFold(filepath.VolumeName(p.stagingRoot()), filepath.VolumeName(p.FontsDir)) {
		return "", fmt.Errorf("%s 与 %s 不在同一个磁盘，无法安排重启时替换", p.DataDir, p.FontsDir)
	}
	dir := filepath.Join(p.stagingRoot(), time.Now().Format("20060102-150405")+"-"+randomID())
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Options for Install.
type Options struct {
	// ImportOriginals is a folder holding genuine originals, used when the
	// system files are already replaced and no originals backup exists.
	ImportOriginals string
}

// Install schedules the package in pkgDir for the next restart.
func Install(sys OS, p Paths, pkgDir string, opt Options, log Logf) error {
	st, err := prepare(sys, p, log)
	if err != nil {
		return err
	}
	pkg, err := VerifyPackage(pkgDir)
	if err != nil {
		return err
	}
	for _, n := range pkg.Notes {
		log("%s", n)
	}
	log("字体包：%s（%d 个文件，校验通过）", firstNonEmpty(pkg.Title, pkg.Name), len(pkg.Files))
	if _, err := ensureOriginals(p, opt.ImportOriginals, log); err != nil {
		return err
	}
	if st.Pending != nil {
		if err := cancelPending(sys, p, st, log); err != nil {
			return err
		}
	}

	staging, err := newStaging(p)
	if err != nil {
		return err
	}
	var files []stagedFile
	installed := &Installed{Kind: "package", Package: pkg.Name, Title: pkg.Title, Files: map[string]string{}, Time: now()}
	for _, f := range pkg.Files {
		dst := filepath.Join(staging, f.Name)
		if err := copyVerified(f.Path, dst, f.SHA256); err != nil {
			removeStaging(p, staging)
			return fmt.Errorf("暂存 %s 失败：%w", f.Name, err)
		}
		// Check the copy we will actually install, not the user's folder.
		data, err := os.ReadFile(dst)
		if err == nil {
			err = verifyFontFile(f.Name, data)
		}
		if err != nil {
			removeStaging(p, staging)
			return err
		}
		installed.Files[f.Name] = f.SHA256
		if cur := sysfonts.StatusOf(p.FontsDir, f.Name); cur.SHA256 == f.SHA256 {
			log("%s 已经是这个版本，跳过", f.Name)
			continue
		}
		files = append(files, stagedFile{name: f.Name, staged: dst, sha: f.SHA256})
	}
	if len(files) == 0 {
		log("系统里已经是这个字体包，只登记清空字体缓存")
	} else {
		log("已暂存到 %s", staging)
	}
	if err := schedule(sys, p, st, "install", files, staging, installed, log); err != nil {
		return err
	}
	log("完成：重启电脑后生效。重启前可以在「系统字体」页取消")
	return nil
}

// Restore schedules the originals for the next restart.
func Restore(sys OS, p Paths, log Logf) error {
	st, err := prepare(sys, p, log)
	if err != nil {
		return err
	}
	o, err := LoadOriginals(p)
	if err != nil {
		return err
	}
	if o == nil {
		return errors.New("还没有原版备份，没有可还原的内容")
	}
	if err := verifyOriginals(p, o); err != nil {
		return err
	}
	if st.Pending != nil {
		if err := cancelPending(sys, p, st, log); err != nil {
			return err
		}
	}
	staging, err := newStaging(p)
	if err != nil {
		return err
	}
	var files []stagedFile
	installed := &Installed{Kind: "originals", Files: map[string]string{}, Time: now()}
	for _, name := range recipe.AllOutputs {
		rec := o.Files[name]
		installed.Files[name] = rec.SHA256
		cur := sysfonts.StatusOf(p.FontsDir, name)
		if matches(cur.SHA256, rec.SHA256) {
			continue
		}
		if rec.SHA256 == "" {
			files = append(files, stagedFile{name: name})
			continue
		}
		dst := filepath.Join(staging, name)
		if err := copyVerified(filepath.Join(p.OriginalsDir(), name), dst, rec.SHA256); err != nil {
			removeStaging(p, staging)
			return fmt.Errorf("暂存原版 %s 失败：%w", name, err)
		}
		files = append(files, stagedFile{name: name, staged: dst, sha: rec.SHA256})
	}
	if len(files) == 0 {
		removeStaging(p, staging)
		st.Installed = installed
		if err := writeJSON(p.statePath(), st); err != nil {
			return err
		}
		log("系统字体已经是原版，不需要还原")
		return nil
	}
	if err := schedule(sys, p, st, "restore", files, staging, installed, log); err != nil {
		return err
	}
	log("完成：重启电脑后换回原版")
	return nil
}

// ClearCache schedules only the font-cache deletion.
func ClearCache(sys OS, p Paths, log Logf) error {
	st, err := prepare(sys, p, log)
	if err != nil {
		return err
	}
	if st.Pending != nil {
		log("已有待重启的%s，它已经包含清空字体缓存", actionLabel(st.Pending.Action))
		return nil
	}
	if err := schedule(sys, p, st, "clear-cache", nil, "", nil, log); err != nil {
		return err
	}
	log("完成：重启电脑后字体缓存会被清空并重建")
	return nil
}

// Cancel removes the pending operation, if any.
func Cancel(sys OS, p Paths, log Logf) error {
	st, err := prepare(sys, p, log)
	if err != nil {
		return err
	}
	if st.Pending == nil {
		log("没有待重启的操作")
		return nil
	}
	return cancelPending(sys, p, st, log)
}

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

// ReadResult returns and removes the helper's result.
func ReadResult(p Paths, id string) (*Result, error) {
	if !ValidResultID(id) {
		return nil, errors.New("invalid result id")
	}
	path := filepath.Join(p.ResultsDir(), id+".json")
	var r Result
	if err := readJSON(path, &r); err != nil {
		return nil, err
	}
	_ = os.Remove(path) // may fail without admin rights; harmless
	return &r, nil
}
