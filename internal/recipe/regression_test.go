package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type regressionCase struct {
	Sarasa  string            `json:"sarasa"`
	Package string            `json:"package"`
	Preset  string            `json:"preset"`
	Branch  string            `json:"branch"`
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

func runRegression(t *testing.T, srcDir string, c regressionCase) {
	t.Helper()
	p, err := PresetByID(c.Preset)
	if err != nil {
		t.Fatal(err)
	}
	if p.Branch != c.Branch {
		t.Fatalf("preset %s maps to branch %s, fixture says %s", p.ID, p.Branch, c.Branch)
	}
	slots, err := p.LoadSources(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := Build(Plan{Slots: slots}, out, nil); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDir(out, AllOutputs); err != nil {
		t.Fatal(err)
	}
	got := hashDir(t, out)
	if !reflect.DeepEqual(got, c.Outputs) {
		for name, want := range c.Outputs {
			if got[name] != want {
				t.Errorf("%s/%s %s: got %s want %s (original %s branch)", c.Sarasa, c.Preset, name, got[name], want, c.Branch)
			}
		}
		t.FailNow()
	}
}

// TestPresetsMatchOriginalBranchesSynthetic always runs: the synthetic
// Sarasa-shaped fonts in testdata/synthetic were fed to the original main and
// regular programs, and both presets must reproduce those files exactly.
func TestPresetsMatchOriginalBranchesSynthetic(t *testing.T) {
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

// TestPresetsMatchOriginalBranchesRealSarasa runs against real Sarasa files
// when YSG_SARASA_DIR points at a directory holding the four SarasaUiSC TTFs
// (or sub-directories of such). A case only runs when the source hashes in
// the directory match the fixture, so any Sarasa release can sit there.
func TestPresetsMatchOriginalBranchesRealSarasa(t *testing.T) {
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
	if _, err := PresetByID("main"); err != nil {
		t.Fatal("branch alias main should resolve")
	}
}
