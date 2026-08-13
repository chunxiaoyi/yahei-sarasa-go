package otfont

import "testing"

func TestTTCSupportsFourFacesForD2(t *testing.T) {
	base := func(seed byte) *Font {
		return &Font{ScalerType: 0x00010000, Tables: map[string][]byte{
			"head": make([]byte, 54),
			"test": {seed, seed + 1, seed + 2, seed + 3},
		}}
	}
	b, err := TTC(base(1), base(2), base(3), base(4))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 28 || string(b[:4]) != "ttcf" {
		t.Fatal("invalid TTC header")
	}
	if got := be.Uint32(b[8:12]); got != 4 {
		t.Fatalf("numFonts=%d want 4", got)
	}
}
