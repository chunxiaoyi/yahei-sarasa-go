package sysinstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
)

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
	for _, d := range []string{p.blobsDir(), p.snapshotsDir(), p.stagingRoot(), p.ResultsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	migrateLegacyOriginals(p, log)
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
		if countPresent(ops, st.Pending.Ops) == 0 {
			removeStaging(p, st.Pending.Staging)
			st.Pending = nil
			if err := writeJSON(p.statePath(), st); err != nil {
				return nil, err
			}
		}
	}
	cleanupStaging(p, st)
	cleanupResults(p)
	return st, nil
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
	dir := filepath.Join(p.stagingRoot(), time.Now().Format("20060102-150405")+"-"+randomHex(8))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Install backs up the current system fonts, then schedules the package in
// pkgDir for the next restart.
func Install(sys OS, p Paths, pkgDir string, log Logf) error {
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
	title := firstNonEmpty(pkg.Title, pkg.Name)
	log("字体包：%s（%d 个文件，校验通过）", title, len(pkg.Files))
	if st.Pending != nil {
		if err := cancelPending(sys, p, st, log); err != nil {
			return err
		}
	}
	before, err := takeSnapshot(p, "安装「"+title+"」之前", log)
	if err != nil {
		return err
	}

	staging, err := newStaging(p)
	if err != nil {
		return err
	}
	var files []stagedFile
	installed := &Installed{Kind: "package", Package: pkg.Name, Title: title, Before: before.ID, Files: map[string]string{}, Time: now()}
	for _, f := range pkg.Files {
		dst := filepath.Join(staging, f.Name)
		if err := copyVerified(f.Path, dst, f.SHA256); err != nil {
			removeStaging(p, staging)
			return fmt.Errorf("暂存 %s 失败：%w", f.Name, err)
		}
		// Check the copy that will actually be installed, not the user's folder.
		data, err := os.ReadFile(dst)
		if err == nil {
			err = verifyFontFile(f.Name, data)
		}
		if err != nil {
			removeStaging(p, staging)
			return err
		}
		installed.Files[f.Name] = f.SHA256
		if before.Files[f.Name].SHA256 == f.SHA256 {
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
	log("完成：重启电脑后生效。重启前可以在「系统字体」页取消；之后可还原到「%s」", shortTime(before.Created))
	return nil
}

// Restore backs up the current system fonts, then schedules snapshot id
// ("" = the newest) for the next restart.
func Restore(sys OS, p Paths, id string, log Logf) error {
	st, err := prepare(sys, p, log)
	if err != nil {
		return err
	}
	target, err := findSnapshot(p, id)
	if err != nil {
		return err
	}
	if err := verifySnapshot(p, target); err != nil {
		return err
	}
	label := shortTime(target.Created) + " " + target.Reason
	log("还原到备份：%s", label)
	if st.Pending != nil {
		if err := cancelPending(sys, p, st, log); err != nil {
			return err
		}
	}
	// The current files are about to be replaced too: keep them.
	before, err := takeSnapshot(p, "还原到「"+shortTime(target.Created)+"」的备份之前", log)
	if err != nil {
		return err
	}

	installed := &Installed{Kind: "snapshot", Title: label, Before: before.ID, Files: map[string]string{}, Time: now()}
	staging, err := newStaging(p)
	if err != nil {
		return err
	}
	var files []stagedFile
	for _, name := range recipe.AllOutputs {
		rec := target.Files[name]
		installed.Files[name] = rec.SHA256
		if matches(before.Files[name].SHA256, rec.SHA256) {
			continue
		}
		if rec.SHA256 == "" {
			files = append(files, stagedFile{name: name})
			continue
		}
		dst := filepath.Join(staging, name)
		if err := copyVerified(p.blobPath(rec.SHA256), dst, rec.SHA256); err != nil {
			removeStaging(p, staging)
			return fmt.Errorf("暂存备份里的 %s 失败：%w", name, err)
		}
		files = append(files, stagedFile{name: name, staged: dst, sha: rec.SHA256})
	}
	if len(files) == 0 {
		removeStaging(p, staging)
		st.Installed = installed
		if err := writeJSON(p.statePath(), st); err != nil {
			return err
		}
		log("系统字体已经和这份备份一样，不需要还原")
		return nil
	}
	if err := schedule(sys, p, st, "restore", files, staging, installed, log); err != nil {
		return err
	}
	log("完成：重启电脑后换回这份备份")
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

// Forget deletes one backup snapshot, and the stored files no remaining
// snapshot needs. Only ever done on the user's explicit request.
func Forget(sys OS, p Paths, id string, log Logf) error {
	if !ValidSnapshotID(id) {
		return errors.New("备份编号无效")
	}
	if _, err := prepare(sys, p, log); err != nil {
		return err
	}
	s, err := findSnapshot(p, id)
	if err != nil {
		return err
	}
	if err := os.Remove(p.snapshotPath(s.ID)); err != nil {
		return err
	}
	freed, err := gcBlobs(p)
	if err != nil {
		return err
	}
	log("已删除备份 %s（%s），释放 %.1f MiB", shortTime(s.Created), s.Reason, float64(freed)/(1<<20))
	return nil
}
