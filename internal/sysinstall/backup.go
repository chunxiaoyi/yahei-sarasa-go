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
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/sysfonts"
)

// Snapshot is the state of the six system files at one moment, taken
// before something replaced them. File contents live once each in
// backups/files/<sha256>; snapshots only reference them.
type Snapshot struct {
	ID      string                `json:"id"`
	Created string                `json:"created"`
	Reason  string                `json:"reason"`
	Files   map[string]FileRecord `json:"files"`
}

// SnapshotInfo is a snapshot plus what the UI needs to describe it.
type SnapshotInfo struct {
	Snapshot
	Label       string   `json:"label"`
	NonOriginal []string `json:"nonOriginal,omitempty"` // present files that are not Microsoft originals
	Missing     []string `json:"missing,omitempty"`     // files that did not exist
	Size        int64    `json:"size"`
	Complete    bool     `json:"complete"` // every referenced file is in the store (size check)
}

var snapshotIDPattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4,16}$`)

// ValidSnapshotID guards snapshot ids coming from the GUI or command line.
func ValidSnapshotID(id string) bool { return snapshotIDPattern.MatchString(id) }

func (p Paths) blobPath(sha string) string    { return filepath.Join(p.blobsDir(), sha) }
func (p Paths) snapshotPath(id string) string { return filepath.Join(p.snapshotsDir(), id+".json") }

func shortTime(rfc string) string {
	if t, err := time.Parse(time.RFC3339, rfc); err == nil {
		return t.Format("2006-01-02 15:04")
	}
	return rfc
}

// Describe adds labels and summaries.
func (s Snapshot) Describe(p Paths) SnapshotInfo {
	info := SnapshotInfo{Snapshot: s, Label: shortTime(s.Created) + "　" + s.Reason, Complete: true}
	for _, name := range recipe.AllOutputs {
		rec, ok := s.Files[name]
		switch {
		case !ok || rec.SHA256 == "":
			info.Missing = append(info.Missing, name)
		default:
			info.Size += rec.Size
			if !sysfonts.Genuine(rec.State) {
				info.NonOriginal = append(info.NonOriginal, name)
			}
			if st, err := os.Stat(p.blobPath(rec.SHA256)); err != nil || st.Size() != rec.Size {
				info.Complete = false
			}
		}
	}
	return info
}

// ListSnapshots returns all snapshots, newest first.
func ListSnapshots(p Paths) ([]Snapshot, error) {
	entries, err := os.ReadDir(p.snapshotsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || !ValidSnapshotID(id) {
			continue
		}
		var s Snapshot
		if err := readJSON(filepath.Join(p.snapshotsDir(), e.Name()), &s); err != nil || s.ID != id {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// findSnapshot returns the snapshot with id, or the newest when id is "".
func findSnapshot(p Paths, id string) (*Snapshot, error) {
	snaps, err := ListSnapshots(p)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return nil, errors.New("还没有任何备份，没有可还原的内容")
	}
	if id == "" {
		return &snaps[0], nil
	}
	for i := range snaps {
		if snaps[i].ID == id {
			return &snaps[i], nil
		}
	}
	return nil, fmt.Errorf("找不到备份 %s", id)
}

// BackupBytes is the disk space the file store uses.
func BackupBytes(p Paths) int64 {
	var total int64
	entries, err := os.ReadDir(p.blobsDir())
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() {
			total += info.Size()
		}
	}
	return total
}

// verifySnapshot checks every referenced file by hash.
func verifySnapshot(p Paths, s *Snapshot) error {
	for _, name := range recipe.AllOutputs {
		rec := s.Files[name]
		if rec.SHA256 == "" {
			continue
		}
		got, _, err := hashFile(p.blobPath(rec.SHA256))
		if err != nil {
			return fmt.Errorf("备份里的 %s 读不到：%w", name, err)
		}
		if got != rec.SHA256 {
			return fmt.Errorf("备份里的 %s 已损坏（哈希不一致）", name)
		}
	}
	return nil
}

// storeBlob makes sure the store holds a verified copy of src.
func storeBlob(p Paths, src, sha string) (stored bool, err error) {
	dst := p.blobPath(sha)
	if got, _, err := hashFile(dst); err == nil && got == sha {
		return false, nil // already there and intact
	}
	_ = os.Remove(dst)
	tmp := dst + ".part"
	_ = os.Remove(tmp)
	if err := copyVerified(src, tmp, sha); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, dst)
}

func sameFiles(a, b map[string]FileRecord) bool {
	for _, name := range recipe.AllOutputs {
		if a[name].SHA256 != b[name].SHA256 {
			return false
		}
	}
	return true
}

// takeSnapshot backs up the six current system files exactly as they are.
// When they are identical to the newest snapshot, that one is reused.
func takeSnapshot(p Paths, reason string, log Logf) (*Snapshot, error) {
	for _, d := range []string{p.blobsDir(), p.snapshotsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	cur := map[string]FileRecord{}
	var nonOriginal []string
	for _, name := range recipe.AllOutputs {
		st := sysfonts.StatusOf(p.FontsDir, name)
		if st.State == sysfonts.StateMissing {
			cur[name] = FileRecord{State: st.State}
			continue
		}
		if st.SHA256 == "" {
			return nil, fmt.Errorf("读不到系统里的 %s，无法备份：%s", name, st.Error)
		}
		stored, err := storeBlob(p, st.Path, st.SHA256)
		if err != nil {
			return nil, fmt.Errorf("备份 %s 失败：%w", name, err)
		}
		cur[name] = FileRecord{SHA256: st.SHA256, Size: st.Size, State: st.State, Version: st.Version, Copyright: st.Copyright}
		if !sysfonts.Genuine(st.State) {
			nonOriginal = append(nonOriginal, name)
		}
		if stored {
			log("已备份 %s（%s）", name, stateLabel(st.State))
		}
	}
	snaps, err := ListSnapshots(p)
	if err != nil {
		return nil, err
	}
	if len(snaps) > 0 && sameFiles(snaps[0].Files, cur) {
		log("当前系统字体与最近的备份（%s）相同，不再重复保存", shortTime(snaps[0].Created))
		return &snaps[0], nil
	}
	s := &Snapshot{ID: time.Now().Format("20060102-150405") + "-" + randomHex(3), Created: now(), Reason: reason, Files: cur}
	if err := writeJSON(p.snapshotPath(s.ID), s); err != nil {
		return nil, err
	}
	log("已保存备份：%s（%s）", shortTime(s.Created), reason)
	if len(nonOriginal) > 0 {
		log("注意：%s 不是微软原版，已照原样备份", strings.Join(nonOriginal, "、"))
	}
	return s, nil
}

// gcBlobs deletes stored files no snapshot references any more.
func gcBlobs(p Paths) (freed int64, err error) {
	snaps, err := ListSnapshots(p)
	if err != nil {
		return 0, err
	}
	keep := map[string]bool{}
	for _, s := range snaps {
		for _, rec := range s.Files {
			if rec.SHA256 != "" {
				keep[rec.SHA256] = true
			}
		}
	}
	entries, err := os.ReadDir(p.blobsDir())
	if err != nil {
		return 0, nil
	}
	for _, e := range entries {
		if e.IsDir() || keep[e.Name()] {
			continue
		}
		if info, err := e.Info(); err == nil {
			freed += info.Size()
		}
		_ = os.Remove(filepath.Join(p.blobsDir(), e.Name()))
	}
	return freed, nil
}

// migrateLegacyOriginals converts the "originals" folder written by an
// earlier development build into an ordinary snapshot.
func migrateLegacyOriginals(p Paths, log Logf) {
	dir := p.legacyOriginals()
	var legacy struct {
		Created string                `json:"created"`
		Files   map[string]FileRecord `json:"files"`
	}
	if err := readJSON(filepath.Join(dir, "originals.json"), &legacy); err != nil {
		return
	}
	for _, d := range []string{p.blobsDir(), p.snapshotsDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return
		}
	}
	for name, rec := range legacy.Files {
		if rec.SHA256 == "" {
			continue
		}
		if _, err := storeBlob(p, filepath.Join(dir, name), rec.SHA256); err != nil {
			log("迁移旧的原版备份失败（%s：%v），保留原文件夹 %s", name, err, dir)
			return
		}
	}
	created := legacy.Created
	t, err := time.Parse(time.RFC3339, created)
	if err != nil {
		t, created = time.Now(), now()
	}
	s := Snapshot{ID: t.Format("20060102-150405") + "-" + randomHex(3), Created: created, Reason: "第一次安装之前（旧版本的原版备份）", Files: legacy.Files}
	if err := writeJSON(p.snapshotPath(s.ID), s); err != nil {
		log("迁移旧的原版备份失败：%v", err)
		return
	}
	_ = os.RemoveAll(dir)
	log("已把旧的原版备份转为普通备份（%s）", shortTime(created))
}
