package charset

import "testing"

func TestSetSizes(t *testing.T) {
	for _, tc := range []struct {
		s    Set
		want int
	}{{GB2312L1, 3755}, {GB2312L2, 3008}, {Big5L1, 5401}, {ASCII, 95}, {Latin1, 96}} {
		if len(tc.s.Runes) != tc.want {
			t.Fatalf("%s: %d want %d", tc.s.ID, len(tc.s.Runes), tc.want)
		}
	}
	for _, r := range GB2312L1.Runes {
		if r < 0x4E00 || r > 0x9FFF {
			t.Fatalf("GB2312 L1 rune U+%04X outside URO", r)
		}
	}
	cov := Measure(GB2312L1, func(r rune) bool { return r != '的' })
	if cov.Have != 3754 || cov.Missing != "的" {
		t.Fatalf("%+v", cov)
	}
}
