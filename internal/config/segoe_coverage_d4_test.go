package config

import "testing"

func TestD4SegoeCoverageSnapshot(t *testing.T) {
	count := 0
	var prev uint16
	for i, r := range SegoeUIRegularCoverage {
		if r.Last < r.First {
			t.Fatalf("range %d reversed: %#x..%#x", i, r.First, r.Last)
		}
		if i > 0 && r.First <= prev {
			t.Fatalf("range %d overlaps or is unsorted", i)
		}
		count += int(r.Last-r.First) + 1
		prev = r.Last
	}
	if count != SegoeUIRegularOriginalCoverageCount {
		t.Fatalf("coverage count=%d want %d", count, SegoeUIRegularOriginalCoverageCount)
	}

	for _, cp := range []uint32{'A', 'z', '0', 0x20AC, 0x0416} {
		if !SegoeUIRegularAllows(cp) {
			t.Fatalf("expected U+%04X in original Segoe whitelist", cp)
		}
	}
	for _, cp := range []uint32{0x4E00, 0x7F51, 0x7EDC, 0x9AD8, 0x1F600} {
		if SegoeUIRegularAllows(cp) {
			t.Fatalf("CJK/non-BMP U+%04X must not be in D4 Segoe whitelist", cp)
		}
	}
}
