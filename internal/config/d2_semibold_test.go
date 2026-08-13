package config

import "testing"

func TestD2SemiBoldSpecs(t *testing.T) {
	cases := []struct {
		name                                         string
		spec                                         FontSpec
		famEN, famZH, uniqueEN, uniqueZH, fullZH, ps string
	}{
		{
			name: "YaHei", spec: SemiBold("1.0.40"),
			famEN: "Microsoft YaHei", famZH: "微软雅黑",
			uniqueEN: "Microsoft YaHei-SemiBold", uniqueZH: "微软雅黑-SemiBold",
			fullZH: "微软雅黑 SemiBold", ps: "MicrosoftYaHei-SemiBold",
		},
		{
			name: "YaHei UI", spec: SemiBoldUI("1.0.40"),
			famEN: "Microsoft YaHei UI", famZH: "Microsoft YaHei UI",
			uniqueEN: "Microsoft YaHei UI-SemiBold", uniqueZH: "Microsoft YaHei UI SemiBold",
			fullZH: "Microsoft Yahei UI SemiBold", ps: "MicrosoftYaHeiUI-SemiBold",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.spec.Weight != 600 {
				t.Fatalf("weight=%d want 600", tc.spec.Weight)
			}
			if tc.spec.FSSelection != 0x0100 {
				t.Fatalf("fsSelection=%#04x want 0x0100", tc.spec.FSSelection)
			}
			want := []struct {
				lang, id uint16
				value    string
			}{
				{0x0409, 1, tc.famEN},
				{0x0409, 2, "SemiBold"},
				{0x0409, 3, tc.uniqueEN},
				{0x0409, 6, tc.ps},
				{0x0409, 16, tc.famEN},
				{0x0409, 17, "SemiBold"},
				{0x0804, 1, tc.famZH},
				{0x0804, 2, "SemiBold"},
				{0x0804, 3, tc.uniqueZH},
				{0x0804, 4, tc.fullZH},
				{0x0804, 6, tc.ps},
				{0x0804, 16, tc.famZH},
				{0x0804, 17, "SemiBold"},
			}
			for _, w := range want {
				found := false
				for _, r := range tc.spec.Identity {
					if r.PlatformID == 3 && r.EncodingID == 1 && r.LanguageID == w.lang && r.NameID == w.id && r.Value == w.value {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("missing name lang=%#x id=%d value=%q", w.lang, w.id, w.value)
				}
			}
		})
	}
}
