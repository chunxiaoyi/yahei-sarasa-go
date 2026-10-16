package sysinstall

import (
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/sysfonts"
)

// FileReport is one system file compared with the last operation.
type FileReport struct {
	sysfonts.FileStatus
	Expected         *string `json:"expected,omitempty"` // sha256 the last operation set up ("" = absent)
	MatchesInstalled bool    `json:"matchesInstalled"`
	BackedUp         bool    `json:"backedUp"` // this exact file is in the backups
}

// Report is the state shown on the system-font page. It needs no privileges.
type Report struct {
	Supported     bool           `json:"supported"`
	Admin         bool           `json:"admin"`
	Paths         Paths          `json:"paths"`
	Files         []FileReport   `json:"files"`
	Snapshots     []SnapshotInfo `json:"snapshots,omitempty"`
	BackupBytes   int64          `json:"backupBytes"`
	State         *State         `json:"state,omitempty"`
	PendingActive bool           `json:"pendingActive"`
	PendingCount  int            `json:"pendingCount"`
	Phase         string         `json:"phase"` // none, pending, applied, restored, failed, modified
	Message       string         `json:"message"`
	Error         string         `json:"error,omitempty"`
}

// GetStatus compares the system files with the records.
func GetStatus(sys OS, p Paths) Report {
	r := Report{Supported: sys.Supported(), Paths: p, Phase: "none"}
	if !r.Supported {
		return r
	}
	r.Admin = sys.IsAdmin()
	snaps, err := ListSnapshots(p)
	if err != nil {
		r.Error = err.Error()
	}
	for _, s := range snaps {
		r.Snapshots = append(r.Snapshots, s.Describe(p))
	}
	r.BackupBytes = BackupBytes(p)
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

	allInstalled := st.Installed != nil
	for _, name := range recipe.AllOutputs {
		fr := FileReport{FileStatus: sysfonts.StatusOf(p.FontsDir, name)}
		if fr.SHA256 != "" {
			fr.BackedUp = fileExists(p.blobPath(fr.SHA256))
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
	case st.Installed != nil && allInstalled && st.Installed.Kind == "snapshot":
		r.Phase, r.Message = "restored", "已还原到备份："+st.Installed.Title
	case st.Installed != nil && allInstalled:
		r.Phase, r.Message = "applied", "已安装："+firstNonEmpty(st.Installed.Title, st.Installed.Package)
	case st.Installed != nil && st.Pending != nil:
		r.Phase = "failed"
		r.Message = "上次安排的" + actionLabel(st.Pending.Action) + "在重启后没有生效。可能是还没重启、重启时被安全软件拦截，或系统还原了文件"
	case st.Installed != nil:
		r.Phase = "modified"
		r.Message = "系统字体在上次操作之后被改动过（例如 Windows 更新或 sfc 修复换回了原版）"
	default:
		r.Message = "还没有用本工具安装过字体"
	}
	return r
}
