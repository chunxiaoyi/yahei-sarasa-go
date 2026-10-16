package sysinstall

import (
	"fmt"
	"strings"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/sysfonts"
)

// PlanFile is one file an operation will change.
type PlanFile struct {
	Name    string `json:"name"`
	Current string `json:"current"` // system file state
	After   string `json:"after"`   // what it becomes
}

// BackupEntry previews how one current system file will be backed up.
type BackupEntry struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Stored bool   `json:"stored"` // an identical copy is already in the backups
}

// Plan describes an operation before it runs. The elevated helper repeats
// every check itself; a plan is only for showing the user.
type Plan struct {
	Action    string         `json:"action"`
	Package   *Package       `json:"package,omitempty"`
	Files     []PlanFile     `json:"files"`
	Backup    []BackupEntry  `json:"backup,omitempty"`
	Snapshots []SnapshotInfo `json:"snapshots,omitempty"`
	Snapshot  string         `json:"snapshot,omitempty"` // restore: the chosen one
	Steps     []string       `json:"steps"`
	Warnings  []string       `json:"warnings,omitempty"`
	Blockers  []string       `json:"blockers,omitempty"`
}

func (p Paths) cacheSteps() []string {
	return []string{
		"删除字体缓存：" + p.CacheDir + " 里的全部文件，以及 " + p.FntCache,
		"重启后 Windows 会用新字体重建缓存，第一次开机可能稍慢",
	}
}

// previewBackup lists the current files and warns about non-originals.
func previewBackup(p Paths, plan *Plan) {
	var nonOriginal []string
	for _, name := range recipe.AllOutputs {
		st := sysfonts.StatusOf(p.FontsDir, name)
		e := BackupEntry{Name: name, State: st.State}
		if st.SHA256 != "" {
			e.Stored = fileExists(p.blobPath(st.SHA256))
			if !sysfonts.Genuine(st.State) {
				nonOriginal = append(nonOriginal, fmt.Sprintf("%s（%s）", name, stateLabel(st.State)))
			}
		}
		plan.Backup = append(plan.Backup, e)
	}
	if len(nonOriginal) > 0 {
		plan.Warnings = append(plan.Warnings, "这些文件不是微软原版，会照原样备份："+strings.Join(nonOriginal, "、")+
			"。非原版字体往往比原版更难再找到，这份备份值得留着；微软原版随时可以从 Windows 安装镜像里取得")
	}
}

func pendingWarning(sys OS, p Paths, plan *Plan) {
	st, _ := LoadState(p)
	if st != nil && st.Pending != nil {
		if ops, err := sys.PendingOps(); err == nil && countPresent(ops, st.Pending.Ops) > 0 {
			plan.Warnings = append(plan.Warnings, "已有一个待重启生效的操作（"+actionLabel(st.Pending.Action)+"），会先取消它")
		}
	}
}

// PlanInstall describes installing the package in pkgDir.
func PlanInstall(sys OS, p Paths, pkgDir string) Plan {
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
	previewBackup(p, &plan)
	pendingWarning(sys, p, &plan)
	plan.Steps = append(plan.Steps,
		"把当前系统里的 6 个字体文件照原样备份到 "+p.BackupsDir()+"（已备份过的不会重复保存）",
		"把字体包复制到受保护的暂存文件夹，再次校验哈希与字体身份",
		"让新文件沿用系统原文件的所有者与权限（TrustedInstaller）",
		"登记「下次启动时替换」：重启时 Windows 在加载字体之前换上新文件")
	plan.Steps = append(plan.Steps, p.cacheSteps()...)
	plan.Steps = append(plan.Steps, "重启前都可以取消；之后随时可以还原到安装前的备份")
	return plan
}

// PlanRestore describes restoring snapshot id ("" = the newest).
func PlanRestore(sys OS, p Paths, id string) Plan {
	plan := Plan{Action: "restore"}
	if !sys.Supported() {
		plan.Blockers = append(plan.Blockers, ErrUnsupported.Error())
		return plan
	}
	snaps, err := ListSnapshots(p)
	if err != nil {
		plan.Blockers = append(plan.Blockers, err.Error())
		return plan
	}
	for _, s := range snaps {
		plan.Snapshots = append(plan.Snapshots, s.Describe(p))
	}
	s, err := findSnapshot(p, id)
	if err != nil {
		plan.Blockers = append(plan.Blockers, err.Error())
		return plan
	}
	plan.Snapshot = s.ID
	info := s.Describe(p)
	if !info.Complete {
		plan.Blockers = append(plan.Blockers, "这份备份里有文件缺失或大小不对，无法还原")
	}
	for _, name := range recipe.AllOutputs {
		rec := s.Files[name]
		cur := sysfonts.StatusOf(p.FontsDir, name)
		if matches(cur.SHA256, rec.SHA256) {
			continue
		}
		after := "备份里的版本（" + stateLabel(rec.State) + "）"
		if rec.SHA256 == "" {
			after = "删除（备份时没有这个文件）"
		}
		plan.Files = append(plan.Files, PlanFile{Name: name, Current: cur.State, After: after})
	}
	if len(plan.Files) == 0 {
		plan.Warnings = append(plan.Warnings, "系统字体已经和这份备份一样，不需要还原")
	}
	pendingWarning(sys, p, &plan)
	plan.Steps = append(plan.Steps,
		"先把当前的 6 个文件也照原样备份（已备份过的不会重复保存），还原之后还能回来",
		"从备份取出所选版本并校验哈希",
		"登记「下次启动时替换」，重启时换回所选版本")
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
