package otfont

import (
	"bytes"
	"testing"
)

func TestC1ChangesOnlyWeightAndFSSelectionInsideOS2(t *testing.T) {
	os2 := make([]byte, 96)
	for i := range os2 {
		os2[i] = byte((i*37 + 11) & 0xff)
	}
	// Give the source realistic, known values that differ from the C1 target.
	be.PutUint16(os2[4:6], 600)
	be.PutUint16(os2[62:64], 0x00c0) // REGULAR | USE_TYPO_METRICS
	before := bytes.Clone(os2)

	gasp := []byte{0, 1, 0, 1, 0, 16, 0, 15}
	f := &Font{Tables: map[string][]byte{
		"OS/2": os2,
		"head": make([]byte, 54),
		"gasp": bytes.Clone(gasp),
	}}
	if err := f.PrepareCommon(400, 0x0040); err != nil {
		t.Fatal(err)
	}

	got := f.Tables["OS/2"]
	if be.Uint16(got[4:6]) != 400 {
		t.Fatalf("weight=%d want 400", be.Uint16(got[4:6]))
	}
	if be.Uint16(got[62:64]) != 0x0040 {
		t.Fatalf("fsSelection=%#04x want 0x0040", be.Uint16(got[62:64]))
	}
	for i := range before {
		if (i >= 4 && i < 6) || (i >= 62 && i < 64) {
			continue
		}
		if got[i] != before[i] {
			t.Fatalf("OS/2 byte %d changed: got %#02x want %#02x", i, got[i], before[i])
		}
	}
	if !bytes.Equal(f.Tables["gasp"], gasp) {
		t.Fatal("C1 changed gasp; it must remain identical to experiment B/source Sarasa")
	}
}
