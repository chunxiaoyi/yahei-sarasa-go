package otfont

import (
	"testing"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/config"
)

func TestBuildNameTablePreservesWindows11Oddities(t *testing.T) {
	for _, tc := range []struct {
		spec        config.FontSpec
		p, e, l, id uint16
		want        string
	}{
		{config.RegularUI("1.0.40"), 3, 1, 0x0804, 3, "Microsoft Yahei UI Regular"},
		{config.RegularUI("1.0.40"), 3, 1, 0x0804, 4, "Microsoft Yahei UI"},
		{config.BoldUI("1.0.40"), 3, 1, 0x0409, 3, "Microsoft YaHei UI-Bold"},
		{config.BoldUI("1.0.40"), 3, 1, 0x0804, 3, "Microsoft YaHei UI Bold"},
		{config.Light("1.0.40"), 3, 1, 0x0409, 1, "Microsoft YaHei Light"},
		{config.Light("1.0.40"), 3, 1, 0x0409, 2, "Regular"},
		{config.Light("1.0.40"), 3, 1, 0x0409, 16, "Microsoft YaHei"},
		{config.Light("1.0.40"), 3, 1, 0x0409, 17, "Light"},
	} {
		b, err := BuildNameTable(tc.spec)
		if err != nil {
			t.Fatal(err)
		}
		recs, err := ParseNameRecords(b)
		if err != nil {
			t.Fatal(err)
		}
		if !hasName(recs, tc.p, tc.e, tc.l, tc.id, tc.want) {
			t.Fatalf("missing exact golden string %q", tc.want)
		}
	}
}

func TestSimSunMacIdentityRoundTrip(t *testing.T) {
	b, err := BuildNameTable(config.SimSun("1.0.40"))
	if err != nil {
		t.Fatal(err)
	}
	recs, err := ParseNameRecords(b)
	if err != nil {
		t.Fatal(err)
	}
	if !hasName(recs, 1, 0, 0, 1, "SimSun") {
		t.Fatal("missing Macintosh SimSun family golden")
	}
}

func TestVersionFromFontMatchesFontForgeImportThenPythonSplit(t *testing.T) {
	// Build a tiny source-style name table manually using the project builder;
	// only ID 5 matters to VersionFromFont.
	spec := config.Regular("Version 1.0.40; Sarasa build metadata")
	name, err := BuildNameTable(spec)
	if err != nil {
		t.Fatal(err)
	}
	f := &Font{Tables: map[string][]byte{"name": name}}
	got, err := VersionFromFont(f)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.0.40" {
		t.Fatalf("got %q, want %q", got, "1.0.40")
	}
}

func TestSearchParams(t *testing.T) {
	sr, es, rs := searchParams(10)
	if sr != 128 || es != 3 || rs != 32 {
		t.Fatalf("got %d %d %d", sr, es, rs)
	}
}
