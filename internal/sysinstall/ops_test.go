package sysinstall

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
)

func nolog(string, ...any) {}

// otherProgram is an entry some other installer registered; ours must never
// touch it.
var otherProgram = PendingOp{`\??\C:\Other\update.tmp`, `!\??\C:\Other\app.dll`}

func pkgSHA(t *testing.T, name string) string {
	t.Helper()
	sha, _, err := hashFile(filepath.Join(testPackage(t), "fonts", name))
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func stagingEntries(t *testing.T, p Paths) []os.DirEntry {
	t.Helper()
	e, err := os.ReadDir(p.stagingRoot())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return e
}

func TestInstallBacksUpSchedulesAndApplies(t *testing.T) {
	p := testPaths(t)
	old := writeSystemFonts(t, p, recipe.OutMSYHL) // msyhl.ttc did not exist
	writeCache(t, p)
	sys := newFakeOS(t)
	sys.pending = []PendingOp{otherProgram}
	targetSD := "O:S-1-5-80-1D:P(A;;FA;;;S-1-5-80-1)(A;;0x1200a9;;;SY)"
	sys.sd[filepath.Join(p.FontsDir, recipe.OutMSYH)] = targetSD

	if err := Install(sys, p, testPackage(t), nolog); err != nil {
		t.Fatal(err)
	}

	// The current fonts were backed up byte for byte before anything else.
	snaps, err := ListSnapshots(p)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots: %v %v", snaps, err)
	}
	for name, b := range old {
		rec := snaps[0].Files[name]
		got, err := os.ReadFile(p.blobPath(rec.SHA256))
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("backup of %s: %v", name, err)
		}
	}
	if rec := snaps[0].Files[recipe.OutMSYHL]; rec.SHA256 != "" {
		t.Fatalf("missing file recorded as present: %+v", rec)
	}

	// Registered: the other program's entry untouched and first, then one
	// replace per file, then every cache file.
	if sys.pending[0] != otherProgram {
		t.Fatalf("other program's entry moved: %v", sys.pending)
	}
	replaces, deletes := 0, 0
	for _, op := range sys.pending[1:] {
		if op.Target == "" {
			deletes++
		} else {
			replaces++
		}
	}
	if replaces != len(recipe.AllOutputs) || deletes != 3 {
		t.Fatalf("registered %d replaces and %d deletes: %v", replaces, deletes, sys.pending)
	}

	st, err := LoadState(p)
	if err != nil || st.Pending == nil || st.Pending.Action != "install" {
		t.Fatalf("state: %+v %v", st, err)
	}
	if !reflect.DeepEqual(st.Pending.Ops, sys.pending[1:]) {
		t.Fatalf("state records %v, registry has %v", st.Pending.Ops, sys.pending[1:])
	}

	// Staged files carry the replaced file's permissions made explicit; the
	// target lets SYSTEM delete it, and its old permissions are kept.
	staged := filepath.Join(st.Pending.Staging, recipe.OutMSYH)
	if got := sys.sd[staged]; got != explicitSDDL(targetSD) {
		t.Fatalf("staged SD %q", got)
	}
	target := filepath.Join(p.FontsDir, recipe.OutMSYH)
	if got := sys.sd[target]; !strings.Contains(got, "(A;;FA;;;SY)") {
		t.Fatalf("target SD %q has no SYSTEM full control", got)
	}
	if st.Pending.SavedSD[target] != targetSD {
		t.Fatalf("saved SD %q", st.Pending.SavedSD[target])
	}
	if r := GetStatus(sys, p); r.Phase != "pending" || r.PendingCount != len(st.Pending.Ops) {
		t.Fatalf("status before restart: %s %d", r.Phase, r.PendingCount)
	}

	sys.reboot()
	for _, name := range recipe.AllOutputs {
		got, _, err := hashFile(filepath.Join(p.FontsDir, name))
		if err != nil || got != pkgSHA(t, name) {
			t.Fatalf("%s after restart: %v", name, err)
		}
	}
	if fileExists(p.FntCache) {
		t.Fatal("FNTCACHE.DAT survived the restart")
	}
	if r := GetStatus(sys, p); r.Phase != "applied" {
		t.Fatalf("status after restart: %s %s", r.Phase, r.Message)
	}

	// The next privileged run notices the install happened and cleans up.
	if err := ClearCache(sys, p, nolog); err != nil {
		t.Fatal(err)
	}
	if e := stagingEntries(t, p); len(e) != 0 {
		t.Fatalf("staging left after the install took effect: %v", e)
	}
	if st, _ := LoadState(p); st.Pending == nil || st.Pending.Action != "clear-cache" {
		t.Fatalf("clear-cache not recorded: %+v", st.Pending)
	}
}

func TestCancelPutsEverythingBack(t *testing.T) {
	p := testPaths(t)
	writeSystemFonts(t, p)
	writeCache(t, p)
	sys := newFakeOS(t)
	sys.pending = []PendingOp{otherProgram}
	before := map[string]string{}
	for _, name := range recipe.AllOutputs {
		t := filepath.Join(p.FontsDir, name)
		sys.sd[t] = "O:BAD:P(A;;FA;;;BA)(A;;0x1200a9;;;BU)#" + name
		before[t] = sys.sd[t]
	}
	if err := Install(sys, p, testPackage(t), nolog); err != nil {
		t.Fatal(err)
	}
	if err := Cancel(sys, p, nolog); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sys.pending, []PendingOp{otherProgram}) {
		t.Fatalf("registry after cancel: %v", sys.pending)
	}
	for target, sd := range before {
		if sys.sd[target] != sd {
			t.Fatalf("%s permissions not restored: %q", target, sys.sd[target])
		}
	}
	if e := stagingEntries(t, p); len(e) != 0 {
		t.Fatalf("staging left after cancel: %v", e)
	}
	st, _ := LoadState(p)
	if st.Pending != nil || st.Installed != nil {
		t.Fatalf("state after cancel: %+v", st)
	}
	// The backup stays: nothing is deleted unless the user asks.
	if snaps, _ := ListSnapshots(p); len(snaps) != 1 {
		t.Fatalf("snapshots after cancel: %d", len(snaps))
	}
}

func TestInstallRollsBackWhenRegistrationFails(t *testing.T) {
	p := testPaths(t)
	writeSystemFonts(t, p)
	writeCache(t, p)
	sys := newFakeOS(t)
	sys.pending = []PendingOp{otherProgram}
	sys.failReplaceN = 3 // two files are already registered when it fails
	targets := map[string]string{}
	for _, name := range recipe.AllOutputs {
		t := filepath.Join(p.FontsDir, name)
		sys.sd[t] = defaultSD
		targets[t] = defaultSD
	}

	err := Install(sys, p, testPackage(t), nolog)
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(sys.pending, []PendingOp{otherProgram}) {
		t.Fatalf("registry not rolled back: %v", sys.pending)
	}
	for target, sd := range targets {
		if sys.sd[target] != sd {
			t.Fatalf("%s permissions not rolled back: %q", target, sys.sd[target])
		}
	}
	if e := stagingEntries(t, p); len(e) != 0 {
		t.Fatalf("staging left after rollback: %v", e)
	}
	if st, _ := LoadState(p); st.Pending != nil || st.Installed != nil {
		t.Fatalf("state changed by a failed install: %+v", st)
	}
}

func TestRestoreBringsBackTheBackup(t *testing.T) {
	p := testPaths(t)
	old := writeSystemFonts(t, p, recipe.OutMSYHL)
	sys := newFakeOS(t)
	if err := Install(sys, p, testPackage(t), nolog); err != nil {
		t.Fatal(err)
	}
	sys.reboot()

	if err := Restore(sys, p, "", nolog); err != nil {
		t.Fatal(err)
	}
	// The installed package was backed up before being replaced.
	snaps, _ := ListSnapshots(p)
	if len(snaps) != 2 {
		t.Fatalf("snapshots: %d", len(snaps))
	}
	// Snapshot ids have one-second resolution, so pick by content.
	original := snapshotWhere(t, snaps, recipe.OutMSYH, func(sha string) bool { return sha != pkgSHA(t, recipe.OutMSYH) }).ID
	sys.reboot()
	for name, b := range old {
		got, err := os.ReadFile(filepath.Join(p.FontsDir, name))
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("%s not restored: %v", name, err)
		}
	}
	if fileExists(filepath.Join(p.FontsDir, recipe.OutMSYHL)) {
		t.Fatal("a file that did not exist in the backup was kept")
	}
	if r := GetStatus(sys, p); r.Phase != "restored" {
		t.Fatalf("status: %s %s", r.Phase, r.Message)
	}

	// Restoring to what is already there schedules nothing.
	if err := Restore(sys, p, original, nolog); err != nil {
		t.Fatal(err)
	}
	if len(sys.pending) != 0 {
		t.Fatalf("no-op restore registered %v", sys.pending)
	}
}

func TestRestoreRefusesDamagedBackup(t *testing.T) {
	p := testPaths(t)
	writeSystemFonts(t, p)
	sys := newFakeOS(t)
	if err := Install(sys, p, testPackage(t), nolog); err != nil {
		t.Fatal(err)
	}
	sys.reboot()
	snaps, _ := ListSnapshots(p)
	blob := p.blobPath(snaps[0].Files[recipe.OutMSYH].SHA256)
	os.Chmod(blob, 0o644)
	if err := os.WriteFile(blob, []byte("bit rot"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Restore(sys, p, snaps[0].ID, nolog); err == nil {
		t.Fatal("restored from a damaged backup")
	}
	if len(sys.pending) != 0 {
		t.Fatalf("registered %v", sys.pending)
	}
}

func TestPrivilegedOpsRefuseWithoutRights(t *testing.T) {
	p := testPaths(t)
	writeSystemFonts(t, p)
	cases := map[string]struct {
		sys  *fakeOS
		want error
	}{
		"not admin":     {&fakeOS{notAdmin: true, sd: map[string]string{}}, ErrNotAdmin},
		"no privileges": {&fakeOS{privErr: errors.New("denied"), sd: map[string]string{}}, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			c.sys.t = t
			err := Install(c.sys, p, testPackage(t), nolog)
			if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
				t.Fatalf("err = %v", err)
			}
			if _, err := os.Stat(p.DataDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("data folder created without rights")
			}
			if len(c.sys.pending) != 0 {
				t.Fatalf("registered %v", c.sys.pending)
			}
		})
	}
	if err := Install(&fakeOS{t: t, noSupport: true, sd: map[string]string{}}, p, testPackage(t), nolog); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported OS: %v", err)
	}
}

func TestPrepareKeepsStagingWhenPendingListUnreadable(t *testing.T) {
	p := testPaths(t)
	writeSystemFonts(t, p)
	sys := newFakeOS(t)
	if err := Install(sys, p, testPackage(t), nolog); err != nil {
		t.Fatal(err)
	}
	st, _ := LoadState(p)
	sys.pendingReadErr = errors.New("registry unavailable")
	if err := ClearCache(sys, p, nolog); err == nil {
		t.Fatal("went on without knowing whether the install is still registered")
	}
	if !isDir(st.Pending.Staging) {
		t.Fatal("staging of a still-registered install was deleted")
	}
}

func TestInstallReplacesAnEarlierPendingInstall(t *testing.T) {
	p := testPaths(t)
	writeSystemFonts(t, p)
	sys := newFakeOS(t)
	sys.pending = []PendingOp{otherProgram}
	if err := Install(sys, p, testPackage(t), nolog); err != nil {
		t.Fatal(err)
	}
	first, _ := LoadState(p)
	if err := Install(sys, p, testPackage(t), nolog); err != nil {
		t.Fatal(err)
	}
	second, _ := LoadState(p)
	if second.Pending.Staging == first.Pending.Staging || isDir(first.Pending.Staging) {
		t.Fatal("the first install's staging was not replaced")
	}
	// Only the other program's entry and the second install's remain.
	if got, want := len(sys.pending), 1+len(second.Pending.Ops); got != want {
		t.Fatalf("registry has %d entries, want %d: %v", got, want, sys.pending)
	}
	for _, op := range first.Pending.Ops {
		if op.Target != "" && contains(sys.pending, op) {
			t.Fatalf("first install still registered: %v", op)
		}
	}
}

// snapshotWhere returns the one snapshot whose record of name satisfies ok.
func snapshotWhere(t *testing.T, snaps []Snapshot, name string, ok func(sha string) bool) Snapshot {
	t.Helper()
	var found []Snapshot
	for _, s := range snaps {
		if ok(s.Files[name].SHA256) {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d snapshots match on %s", len(found), name)
	}
	return found[0]
}

func contains(ops []PendingOp, op PendingOp) bool {
	for _, o := range ops {
		if sameOp(o, op) {
			return true
		}
	}
	return false
}

func TestForgetDropsOnlyUnreferencedFiles(t *testing.T) {
	p := testPaths(t)
	writeSystemFonts(t, p)
	sys := newFakeOS(t)
	if err := Install(sys, p, testPackage(t), nolog); err != nil {
		t.Fatal(err)
	}
	sys.reboot()
	// Change one system file, then back up again: two snapshots that share
	// five files.
	os.WriteFile(filepath.Join(p.FontsDir, recipe.OutSimSunB), []byte("changed by hand"), 0o644)
	if err := Install(sys, p, testPackage(t), nolog); err != nil {
		t.Fatal(err)
	}
	snaps, _ := ListSnapshots(p)
	if len(snaps) != 2 {
		t.Fatalf("snapshots: %d", len(snaps))
	}
	changed, _, _ := hashFile(filepath.Join(p.FontsDir, recipe.OutSimSunB))
	newest := snapshotWhere(t, snaps, recipe.OutSimSunB, func(sha string) bool { return sha == changed })
	oldest := snapshotWhere(t, snaps, recipe.OutSimSunB, func(sha string) bool { return sha != changed })
	if err := Forget(sys, p, oldest.ID, nolog); err != nil {
		t.Fatal(err)
	}
	for name, rec := range newest.Files {
		if rec.SHA256 != "" && !fileExists(p.blobPath(rec.SHA256)) {
			t.Fatalf("%s of the remaining backup was deleted", name)
		}
	}
	for name, rec := range oldest.Files {
		if newest.Files[name].SHA256 != rec.SHA256 && fileExists(p.blobPath(rec.SHA256)) {
			t.Fatalf("%s only the forgotten backup used was kept", name)
		}
	}
	if err := Forget(sys, p, "../../etc", nolog); err == nil {
		t.Fatal("accepted a malformed backup id")
	}
}

func TestRemoveStagingStaysInsideStagingRoot(t *testing.T) {
	p := testPaths(t)
	os.MkdirAll(p.stagingRoot(), 0o755)
	outside := filepath.Join(p.DataDir, "backups")
	os.MkdirAll(outside, 0o755)
	for _, dir := range []string{outside, p.stagingRoot(), filepath.Join(p.stagingRoot(), ".."), p.FontsDir} {
		removeStaging(p, dir)
	}
	for _, dir := range []string{outside, p.stagingRoot(), p.FontsDir} {
		if !isDir(dir) {
			t.Fatalf("%s was deleted", dir)
		}
	}
	inside := filepath.Join(p.stagingRoot(), "20260101-000000-abcd")
	os.MkdirAll(inside, 0o755)
	removeStaging(p, inside)
	if isDir(inside) {
		t.Fatal("staging folder not removed")
	}
}

func TestSecurityDescriptorRewrites(t *testing.T) {
	in := "O:S-1-5-80-1G:SYD:AI(A;ID;FA;;;S-1-5-80-1)(A;OICIID;0x1200a9;;;BU)"
	if got, want := explicitSDDL(in), "O:S-1-5-80-1G:SYD:P(A;;FA;;;S-1-5-80-1)(A;OICI;0x1200a9;;;BU)"; got != want {
		t.Fatalf("explicitSDDL = %q, want %q", got, want)
	}
	if got, want := withSystemFull(in), "O:S-1-5-80-1G:SYD:P(A;;FA;;;SY)(A;;FA;;;S-1-5-80-1)(A;OICI;0x1200a9;;;BU)"; got != want {
		t.Fatalf("withSystemFull = %q, want %q", got, want)
	}
	if got := explicitSDDL("O:BA"); got != "O:BA" {
		t.Fatalf("no DACL: %q", got)
	}
	if got := explicitSDDL("O:BAD:"); got != "O:BAD:P" {
		t.Fatalf("empty DACL: %q", got)
	}
}

func TestValidIDs(t *testing.T) {
	for id, ok := range map[string]bool{
		"0123456789abcdef":    true,
		"0123456789ABCDEF":    false,
		"0123":                false,
		"../0123456789abcdef": false,
	} {
		if ValidResultID(id) != ok {
			t.Errorf("ValidResultID(%q) != %v", id, ok)
		}
	}
	for id, ok := range map[string]bool{
		"20260101-120000-abc123": true,
		"20260101-120000":        false,
		"20260101-120000-abc/..": false,
	} {
		if ValidSnapshotID(id) != ok {
			t.Errorf("ValidSnapshotID(%q) != %v", id, ok)
		}
	}
}
