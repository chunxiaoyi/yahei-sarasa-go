package otfont

import (
	"fmt"

	"github.com/chunxiaoyi/yahei-sarasa-semibold-go/internal/config"
)

// VerifySpec enforces the compatibility contract: Windows 11 identity records
// exactly as embedded from the official fonts, plus the original project's
// rendering mutations and metadata overrides. D2 starts from C1: existing faces preserve the C1 contract, while the two
// added SemiBold faces use an experimental 600-weight identity defined in config.
func VerifySpec(f *Font, spec config.FontSpec) error {
	head := f.Tables["head"]
	if len(head) < 18 || (be.Uint16(head[16:18])&(1<<13)) == 0 {
		return fmt.Errorf("verify: head ClearType bit 13 is not set")
	}
	os2 := f.Tables["OS/2"]
	if len(os2) < 64 || be.Uint16(os2[4:6]) != spec.Weight {
		return fmt.Errorf("verify: OS/2.usWeightClass != %d", spec.Weight)
	}
	if got := be.Uint16(os2[62:64]); got != spec.FSSelection {
		return fmt.Errorf("verify: OS/2.fsSelection=%#04x, want %#04x", got, spec.FSSelection)
	}

	recs, err := ParseNameRecords(f.Tables["name"])
	if err != nil {
		return err
	}
	wantCount := len(spec.Identity) + 2
	if len(recs) != wantCount {
		return fmt.Errorf("verify: name record count=%d, want %d", len(recs), wantCount)
	}
	for _, want := range spec.Identity {
		found := false
		for _, got := range recs {
			if got.PlatformID == want.PlatformID && got.EncodingID == want.EncodingID &&
				got.LanguageID == want.LanguageID && got.NameID == want.NameID && got.Value == want.Value {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("verify: missing Windows 11 golden name (%d,%d,%#x,id=%d,%q)",
				want.PlatformID, want.EncodingID, want.LanguageID, want.NameID, want.Value)
		}
	}
	if !hasName(recs, platformWindows, encodingUnicodeBMP, langEnglishUS, 0, config.Copyright) {
		return fmt.Errorf("verify: project copyright record missing")
	}
	if !hasName(recs, platformWindows, encodingUnicodeBMP, langEnglishUS, 5, spec.Version) {
		return fmt.Errorf("verify: source version record missing")
	}
	return nil
}

func hasName(rs []NameRecord, p, e, l, id uint16, value string) bool {
	for _, r := range rs {
		if r.PlatformID == p && r.EncodingID == e && r.LanguageID == l && r.NameID == id && r.Value == value {
			return true
		}
	}
	return false
}
