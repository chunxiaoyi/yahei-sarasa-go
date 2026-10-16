package config

import "testing"

func TestWindows11GoldenCriticalStrings(t *testing.T) {
	cases := []struct {
		key                 string
		platform, enc, lang uint16
		nameID              uint16
		want                string
	}{
		{"RegularUI", 3, 1, 0x0409, 3, "Microsoft YaHei UI Regular"},
		{"RegularUI", 3, 1, 0x0804, 3, "Microsoft Yahei UI Regular"},
		{"RegularUI", 3, 1, 0x0804, 4, "Microsoft Yahei UI"},
		{"BoldUI", 3, 1, 0x0409, 3, "Microsoft YaHei UI-Bold"},
		{"BoldUI", 3, 1, 0x0804, 3, "Microsoft YaHei UI Bold"},
		{"BoldUI", 3, 1, 0x0804, 4, "Microsoft Yahei UI Bold"},
		{"Light", 3, 1, 0x0409, 1, "Microsoft YaHei Light"},
		{"Light", 3, 1, 0x0409, 2, "Regular"},
		{"Light", 3, 1, 0x0409, 16, "Microsoft YaHei"},
		{"Light", 3, 1, 0x0409, 17, "Light"},
		{"LightUI", 3, 1, 0x0804, 1, "Microsoft YaHei UI Light"},
		{"SimSun", 3, 1, 0x0804, 1, "宋体"},
		{"NSimSun", 3, 1, 0x0804, 1, "新宋体"},
		{"SimSunExtB", 3, 1, 0x0409, 1, "SimSun-ExtB"},
		{"SegoeUIRegular", 3, 1, 0x0409, 1, "Segoe UI"},
		{"SegoeUIRegular", 3, 1, 0x0409, 2, "Regular"},
		{"SegoeUIRegular", 3, 1, 0x0409, 3, "Segoe UI Regular"},
		{"SegoeUIRegular", 3, 1, 0x0409, 4, "Segoe UI"},
		{"SegoeUIRegular", 3, 1, 0x0409, 6, "SegoeUI"},
	}
	for _, tc := range cases {
		g := goldenFaces[tc.key]
		got := ""
		for _, r := range g.Records {
			if r.PlatformID == tc.platform && r.EncodingID == tc.enc && r.LanguageID == tc.lang && r.NameID == tc.nameID {
				got = r.Value
				break
			}
		}
		if got != tc.want {
			t.Fatalf("%s (%d,%d,%#x,id=%d): got %q want %q", tc.key, tc.platform, tc.enc, tc.lang, tc.nameID, got, tc.want)
		}
	}
}

func TestRenderingWeightsFollowOriginalProject(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want uint16
	}{
		{"Regular", 400}, {"RegularUI", 400},
		{"Bold", 700}, {"BoldUI", 700},
		{"Light", 300}, {"LightUI", 300},
		{"SimSun", 400}, {"NSimSun", 400}, {"SimSunExtB", 400},
		{"SegoeUIRegular", 400},
	} {
		if got := goldenFaces[tc.key].Weight; got != tc.want {
			t.Fatalf("%s weight=%d want %d", tc.key, got, tc.want)
		}
	}
}

func TestC1FSSelectionMatchesWindows11OriginalFaces(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want uint16
	}{
		{"Regular", 0x0040}, {"RegularUI", 0x0040},
		{"Bold", 0x0020}, {"BoldUI", 0x0020},
		{"Light", 0x0100}, {"LightUI", 0x0100},
		{"SimSun", 0x0040}, {"NSimSun", 0x0040}, {"SimSunExtB", 0x0040},
		{"SegoeUIRegular", 0x0040},
	} {
		got, ok := windows11FSSelection[tc.key]
		if !ok {
			t.Fatalf("%s missing fsSelection golden", tc.key)
		}
		if got != tc.want {
			t.Fatalf("%s fsSelection=%#04x want %#04x", tc.key, got, tc.want)
		}
	}
}

func TestProjectCopyright(t *testing.T) {
	if Copyright != "Made from sarasa by GPT and Claude" || BaselineCopyright != "Made from sarasa by GPT" {
		t.Fatalf("copyright strings changed: %q / %q", Copyright, BaselineCopyright)
	}
	for _, s := range []string{Copyright, BaselineCopyright} {
		if !IsProjectCopyright(s) {
			t.Fatalf("%q not recognised", s)
		}
	}
	if IsProjectCopyright("© Microsoft Corporation") || IsProjectCopyright("") {
		t.Fatal("foreign copyright recognised as ours")
	}
	if Regular("1").Copyright != Copyright || SemiBold("1").Copyright != Copyright {
		t.Fatal("presets must carry the current copyright")
	}
}
