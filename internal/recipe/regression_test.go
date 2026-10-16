package recipe

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/config"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
)

type regressionCase struct {
	Sarasa  string            `json:"sarasa"`
	Package string            `json:"package"`
	Preset  string            `json:"preset"`
	Sources map[string]string `json:"sources"`
	Outputs map[string]string `json:"outputs"`
}

func loadRegression(t *testing.T) []regressionCase {
	t.Helper()
	b, err := os.ReadFile("testdata/regression-sarasa.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []regressionCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Cases
}

func hashDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".ttf") && !strings.HasSuffix(strings.ToLower(e.Name()), ".ttc") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		out[e.Name()] = hex.EncodeToString(sum[:])
	}
	return out
}

// runRegression proves two things for one preset and source set:
//
//  1. Built with config.BaselineCopyright, all six outputs match the pinned
//     hashes byte for byte.
//  2. Built normally, each output differs from (1) only in the name ID 0
//     record; every other table of every face is byte-identical.
func runRegression(t *testing.T, srcDir string, c regressionCase) {
	t.Helper()
	p, err := PresetByID(c.Preset)
	if err != nil {
		t.Fatal(err)
	}
	slots, err := p.LoadSources(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	setCopyright := func(v string) {
		for _, src := range slots {
			src.Copyright = v
		}
	}

	legacy := t.TempDir()
	setCopyright(config.BaselineCopyright)
	if _, err := Build(Plan{Slots: slots}, legacy, nil); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDir(legacy, AllOutputs); err != nil {
		t.Fatal(err)
	}
	got := hashDir(t, legacy)
	if !reflect.DeepEqual(got, c.Outputs) {
		for name, want := range c.Outputs {
			if got[name] != want {
				t.Errorf("%s/%s %s: got %s want %s", c.Sarasa, c.Preset, name, got[name], want)
			}
		}
		t.FailNow()
	}

	current := t.TempDir()
	setCopyright("") // presets' own default: config.Copyright
	if _, err := Build(Plan{Slots: slots}, current, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range AllOutputs {
		assertOnlyCopyrightDiffers(t, filepath.Join(legacy, name), filepath.Join(current, name))
	}
}

func assertOnlyCopyrightDiffers(t *testing.T, legacyPath, currentPath string) {
	t.Helper()
	read := func(p string) []*otfont.Font {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		faces, _, err := otfont.ParseFaces(b)
		if err != nil {
			t.Fatal(err)
		}
		return faces
	}
	name := filepath.Base(currentPath)
	a, b := read(legacyPath), read(currentPath)
	if len(a) != len(b) {
		t.Fatalf("%s: face count %d vs %d", name, len(a), len(b))
	}
	for i := range a {
		if a[i].ScalerType != b[i].ScalerType || len(a[i].Tables) != len(b[i].Tables) {
			t.Fatalf("%s face %d: table directory changed", name, i)
		}
		for tag, at := range a[i].Tables {
			bt, ok := b[i].Tables[tag]
			if !ok {
				t.Fatalf("%s face %d: table %q missing", name, i, tag)
			}
			switch tag {
			case "name":
				compareNamesExceptCopyright(t, fmt.Sprintf("%s face %d", name, i), at, bt)
			case "head":
				// checkSumAdjustment (bytes 8..12) covers the whole file, so it
				// necessarily changes with the name table; nothing else may.
				if len(at) != len(bt) || !bytes.Equal(at[:8], bt[:8]) || !bytes.Equal(at[12:], bt[12:]) {
					t.Fatalf("%s face %d: head changed beyond checkSumAdjustment", name, i)
				}
			default:
				if !bytes.Equal(at, bt) {
					t.Fatalf("%s face %d: table %q changed", name, i, tag)
				}
			}
		}
	}
}

func compareNamesExceptCopyright(t *testing.T, where string, legacyTable, currentTable []byte) {
	t.Helper()
	la, err := otfont.ParseNameRecords(legacyTable)
	if err != nil {
		t.Fatal(err)
	}
	lb, err := otfont.ParseNameRecords(currentTable)
	if err != nil {
		t.Fatal(err)
	}
	if len(la) != len(lb) {
		t.Fatalf("%s: name record count %d vs %d", where, len(la), len(lb))
	}
	for i := range la {
		ra, rb := la[i], lb[i]
		if ra.PlatformID != rb.PlatformID || ra.EncodingID != rb.EncodingID || ra.LanguageID != rb.LanguageID || ra.NameID != rb.NameID {
			t.Fatalf("%s: name record %d key changed", where, i)
		}
		if ra.NameID == 0 && ra.PlatformID == 3 && ra.LanguageID == 0x0409 {
			if ra.Value != config.BaselineCopyright || rb.Value != config.Copyright {
				t.Fatalf("%s: copyright %q -> %q", where, ra.Value, rb.Value)
			}
			continue
		}
		if ra.Value != rb.Value {
			t.Fatalf("%s: name id %d changed: %q -> %q", where, ra.NameID, ra.Value, rb.Value)
		}
	}
}

// TestPresetsMatchBaselineSynthetic always runs: both presets built from the
// synthetic Sarasa-shaped fonts in testdata/synthetic must reproduce the
// pinned files exactly (with config.BaselineCopyright) and differ only in
// name ID 0 otherwise.
func TestPresetsMatchBaselineSynthetic(t *testing.T) {
	n := 0
	for _, c := range loadRegression(t) {
		if c.Sarasa != "synthetic" {
			continue
		}
		n++
		t.Run(c.Preset, func(t *testing.T) { runRegression(t, "testdata/synthetic", c) })
	}
	if n != 2 {
		t.Fatalf("expected 2 synthetic cases, got %d", n)
	}
}

// TestPresetsMatchBaselineRealSarasa runs against real Sarasa files
// when YSG_SARASA_DIR points at a directory holding the four SarasaUiSC TTFs
// (or sub-directories of such). A case only runs when the source hashes in
// the directory match the fixture, so any Sarasa release can sit there.
func TestPresetsMatchBaselineRealSarasa(t *testing.T) {
	root := os.Getenv("YSG_SARASA_DIR")
	if root == "" {
		t.Skip("set YSG_SARASA_DIR to run the real-Sarasa byte-for-byte regression")
	}
	dirs := []string{root}
	if entries, err := os.ReadDir(root); err == nil {
		for _, e := range entries {
			sub := filepath.Join(root, e.Name())
			if st, err := os.Stat(sub); err == nil && st.IsDir() {
				dirs = append(dirs, sub)
			}
		}
	}
	ran := 0
	for _, dir := range dirs {
		have := hashDir(t, dir)
		for _, c := range loadRegression(t) {
			if c.Sarasa == "synthetic" {
				continue
			}
			match := true
			for name, want := range c.Sources {
				if have[name] != want {
					match = false
				}
			}
			if !match {
				continue
			}
			ran++
			t.Run(c.Package+"/"+c.Preset, func(t *testing.T) { runRegression(t, dir, c) })
		}
	}
	if ran == 0 {
		t.Skip("no directory under YSG_SARASA_DIR matches a fixture's source hashes")
	}
}

func TestPresetMappings(t *testing.T) {
	cases := map[string]map[Slot]string{
		"semibold": {
			SlotLight: SarasaLight, SlotRegular: SarasaSemiBold, SlotSemiBold: SarasaSemiBold,
			SlotBold: SarasaBold, SlotSimSun: SarasaSemiBold, SlotSegoe: SarasaSemiBold,
		},
		"regular": {
			SlotLight: SarasaLight, SlotRegular: SarasaRegular, SlotSemiBold: SarasaSemiBold,
			SlotBold: SarasaBold, SlotSimSun: SarasaSemiBold, SlotSegoe: SarasaRegular,
		},
	}
	for id, want := range cases {
		p, err := PresetByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(p.Map, want) {
			t.Fatalf("%s mapping changed: %v", id, p.Map)
		}
	}
	if got := PresetSemiBold.Files(); !reflect.DeepEqual(got, []string{"SarasaUiSC-Bold.ttf", "SarasaUiSC-Light.ttf", "SarasaUiSC-SemiBold.ttf"}) {
		t.Fatalf("semibold files: %v", got)
	}
	if got := PresetRegular.Files(); len(got) != 4 {
		t.Fatalf("regular files: %v", got)
	}
}
