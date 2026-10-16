package custom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/config"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
)

func load(t *testing.T, names ...string) []*Face {
	t.Helper()
	var faces []*Face
	for _, n := range names {
		f, err := Inspect(filepath.Join("testdata", n))
		if err != nil {
			t.Fatal(err)
		}
		if f.Error != "" {
			t.Fatalf("%s: %s", n, f.Error)
		}
		faces = append(faces, f.Faces...)
	}
	return faces
}

func hasCode(issues []Issue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}

func hasFix(fixes []otfont.Fix, code string) bool {
	for _, f := range fixes {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestInspectFindings(t *testing.T) {
	cases := []struct {
		file   string
		issues []string
		fixes  []string
		usable bool
	}{
		{"fixture-regular.ttf", []string{"unhinted"}, []string{"codepage"}, true},
		{"fixture-vf.ttf", []string{"variable"}, []string{"flatten-variable"}, true},
		{"fixture-maccmap.ttf", nil, []string{"drop-stat", "drop-dsig", "cmap-windows"}, true},
		{"fixture-os2v3.ttf", nil, []string{"os2-version"}, true},
		{"fixture-italic.ttf", []string{"italic"}, nil, true},
		{"fixture-tall.ttf", []string{"clip"}, nil, true},
		{"fixture-cff.otf", []string{"cff"}, nil, true},
		{"fixture-symbol.ttf", []string{"unusable"}, nil, false},
		{"fixture-regular.woff", nil, nil, true},
	}
	for _, tc := range cases {
		faces := load(t, tc.file)
		f := faces[0]
		if f.Usable != tc.usable {
			t.Fatalf("%s usable=%v", tc.file, f.Usable)
		}
		for _, c := range tc.issues {
			if !hasCode(f.Issues, c) {
				t.Errorf("%s: expected issue %s, got %+v", tc.file, c, f.Issues)
			}
		}
		for _, c := range tc.fixes {
			if !hasFix(f.Fixes, c) {
				t.Errorf("%s: expected fix %s, got %+v", tc.file, c, f.Fixes)
			}
		}
	}
}

func TestWOFF2Rejected(t *testing.T) {
	f := InspectBytes("x.woff2", append([]byte("wOF2"), make([]byte, 60)...))
	if f.Error == "" || !strings.Contains(f.Error, "WOFF2") {
		t.Fatalf("got %q", f.Error)
	}
}

func TestAutoAssignMirrorsPresets(t *testing.T) {
	faces := load(t, "fixture-light.ttf", "fixture-regular.ttf", "fixture-medium.ttf", "fixture-bold.ttf", "fixture-latin.ttf")
	w := func(a Assignment, s recipe.Slot) uint16 {
		for _, f := range faces {
			if f.ID == a[s] {
				return f.Weight
			}
		}
		return 0
	}
	reg := AutoAssign(faces, StyleRegular)
	want := map[recipe.Slot]uint16{
		recipe.SlotLight: 300, recipe.SlotRegular: 400, recipe.SlotSemiBold: 500,
		recipe.SlotBold: 700, recipe.SlotSimSun: 500, recipe.SlotSegoe: 400,
	}
	for s, ww := range want {
		if got := w(reg, s); got != ww {
			t.Errorf("regular style %s: weight %d want %d", s, got, ww)
		}
	}
	sb := AutoAssign(faces, StyleSemiBold)
	if w(sb, recipe.SlotRegular) != 500 || w(sb, recipe.SlotSegoe) != 500 {
		t.Errorf("semibold style should put the ~600 face on 400 and Segoe: %v", sb)
	}
	for _, s := range recipe.AllSlots {
		for _, f := range faces {
			if f.ID == reg[s] && f.Facts.Family == "Fixture Latin" {
				t.Errorf("Latin-only face won slot %s", s)
			}
		}
	}
}

func TestAutoAssignPrefersSimplifiedChineseFace(t *testing.T) {
	faces := load(t, "fixture-pair.ttc") // #0 "JP" Bold, #1 "SC" Regular
	if faces[0].LangRank() != 3 || faces[1].LangRank() != 0 {
		t.Fatalf("lang ranks %d %d", faces[0].LangRank(), faces[1].LangRank())
	}
	a := AutoAssign(faces, StyleRegular)
	if a[recipe.SlotRegular] != faces[1].ID {
		t.Fatalf("regular should be the SC face")
	}
}

func TestEvaluateBlocksNonCJKAndSymbol(t *testing.T) {
	faces := load(t, "fixture-latin.ttf")
	ev := Evaluate(faces, Request{Style: StyleRegular})
	if ev.CanBuild {
		t.Fatal("Latin-only font must not build YaHei outputs")
	}
	// Segoe UI alone is fine from a Latin font.
	ev = Evaluate(faces, Request{Style: StyleRegular, Outputs: []string{recipe.OutSegoeUI}})
	if !ev.CanBuild {
		t.Fatalf("segoe-only from Latin font should build: %+v", ev.Slots)
	}
	if _, err := ToRecipe(faces, ev); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateNeedsAnOutput(t *testing.T) {
	faces := load(t, "fixture-regular.ttf")
	ev := Evaluate(faces, Request{Outputs: []string{}})
	if ev.CanBuild || !hasCode(ev.Issues, "no-output") {
		t.Fatalf("%+v", ev.Issues)
	}
}

func TestManualAssignmentWins(t *testing.T) {
	faces := load(t, "fixture-regular.ttf", "fixture-bold.ttf")
	ev := Evaluate(faces, Request{Assign: Assignment{recipe.SlotLight: faces[1].ID}})
	for _, s := range ev.Slots {
		if s.Slot == recipe.SlotLight {
			if s.FaceID != faces[1].ID || s.Auto {
				t.Fatalf("manual assignment ignored: %+v", s)
			}
			if !hasCode(s.Issues, "weight-gap") {
				t.Fatalf("700 in the 300 slot should warn: %+v", s.Issues)
			}
		}
	}
}

// TestCustomBuildEndToEnd builds every output from awkward sources and
// checks the custom-only identity rules on the written files.
func TestCustomBuildEndToEnd(t *testing.T) {
	faces := load(t, "fixture-light.ttf", "fixture-maccmap.ttf", "fixture-medium.ttf", "fixture-bold.ttf", "fixture-cff.otf", "fixture-vf.ttf")
	// Force the awkward ones into slots: mac-cmap as Regular, CFF as SimSun, VF as Segoe.
	id := func(family, sub string) string {
		for _, f := range faces {
			if strings.Contains(f.FileName, family) && (sub == "" || f.Facts.Subfamily == sub) {
				return f.ID
			}
		}
		t.Fatalf("no face %s", family)
		return ""
	}
	req := Request{Style: StyleRegular, Assign: Assignment{
		recipe.SlotRegular: id("maccmap", ""),
		recipe.SlotSimSun:  id("cff", ""),
		recipe.SlotSegoe:   id("vf", ""),
	}}
	ev := Evaluate(faces, req)
	if !ev.CanBuild {
		t.Fatalf("cannot build: %+v", ev)
	}
	plan, err := ToRecipe(faces, ev)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	outs, err := recipe.Build(plan, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := recipe.VerifyDir(dir, recipe.AllOutputs); err != nil {
		t.Fatal(err)
	}
	if len(outs) != 6 {
		t.Fatalf("outputs %d", len(outs))
	}

	read := func(name string) []*otfont.Font {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		fs, _, err := otfont.ParseFaces(b)
		if err != nil {
			t.Fatal(err)
		}
		return fs
	}
	msyh := read(recipe.OutMSYH)
	if len(msyh) != 2 {
		t.Fatalf("msyh faces %d", len(msyh))
	}
	recs, _ := otfont.ParseNameRecords(msyh[0].Tables["name"])
	if otfont.BestName(recs, 0) != "Copyright Fixture Foundry" {
		t.Fatalf("custom build must keep the source copyright, got %q", otfont.BestName(recs, 0))
	}
	if otfont.BestName(recs, 13) != "Fixture license" || otfont.BestName(recs, 14) == "" {
		t.Fatal("license records not carried over")
	}
	if !strings.HasPrefix(otfont.BestName(recs, 10), config.CustomMarkerPrefix) {
		t.Fatalf("custom marker missing: %q", otfont.BestName(recs, 10))
	}
	if otfont.BestName(recs, 5) != "2.500" {
		t.Fatalf("version %q", otfont.BestName(recs, 5))
	}
	if err := otfont.VerifySpec(msyh[1], specWithExtras(config.RegularUI("2.500"), recs)); err != nil {
		t.Fatal(err)
	}
	// Mac-only cmap gained Windows records; STAT/DSIG are gone.
	if _, ok := msyh[0].Tables["STAT"]; ok {
		t.Fatal("STAT survived")
	}
	winCmap := false
	for _, st := range otfont.CmapSubtables(msyh[0].Tables["cmap"]) {
		if st.Platform == 3 && st.Encoding == 1 {
			winCmap = true
		}
	}
	if !winCmap {
		t.Fatal("no (3,1) cmap in output")
	}

	bd := read(recipe.OutMSYHBD)
	for i, wantBold := range []bool{true, true, false, false} {
		ms := otfont.ReadFacts(bd[i]).MacStyle
		if (ms&1 != 0) != wantBold || ms&2 != 0 {
			t.Fatalf("msyhbd face %d macStyle %#x", i, ms)
		}
	}
	segoe := read(recipe.OutSegoeUI)[0]
	if _, ok := segoe.Tables["fvar"]; ok {
		t.Fatal("variable tables survived in segoeui.ttf")
	}
	if _, err := otfont.VerifyCmapWhitelist(segoe, config.SegoeUIRegularAllows); err != nil {
		t.Fatal(err)
	}
	ss := read(recipe.OutSimSun)
	if ss[0].ScalerType != otfont.ScalerCFF {
		t.Fatal("CFF SimSun lost its OTTO scaler type")
	}
}

func specWithExtras(spec config.FontSpec, recs []otfont.NameRecord) config.FontSpec {
	spec.Copyright = otfont.BestName(recs, 0)
	for _, id := range []uint16{10, 13, 14} {
		if v := otfont.BestName(recs, id); v != "" {
			spec.ExtraNames = append(spec.ExtraNames, config.NameRecord{PlatformID: 3, EncodingID: 1, LanguageID: 0x0409, NameID: id, Value: v})
		}
	}
	return spec
}

func TestNormalizeLeavesSourceUntouched(t *testing.T) {
	b, err := os.ReadFile("testdata/fixture-maccmap.ttf")
	if err != nil {
		t.Fatal(err)
	}
	orig := append([]byte(nil), b...)
	faces, _, err := otfont.ParseFaces(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := otfont.Normalize(faces[0], otfont.NormalizeOptions{SetCodePageBits: otfont.CodePageGB2312}); err != nil {
		t.Fatal(err)
	}
	if string(b) != string(orig) {
		t.Fatal("Normalize wrote into the source buffer")
	}
	if _, ok := faces[0].Tables["STAT"]; !ok {
		t.Fatal("Normalize mutated the source font map")
	}
}
