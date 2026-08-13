package otfont

import "testing"

func TestD4FilterCmapToWhitelist(t *testing.T) {
	srcMap := map[uint32]uint16{
		0x20:   3,
		'A':    4,
		'0':    5,
		0x00E9: 6,
		0x4E00: 7,
		0x7F51: 8,
	}
	cmap, err := buildSegoeStyleFormat4Cmap(srcMap)
	if err != nil {
		t.Fatal(err)
	}
	f := &Font{Tables: map[string][]byte{"cmap": cmap}}
	allow := func(cp uint32) bool { return cp < 0x0400 }
	kept, dropped, err := f.FilterCmapToWhitelist(allow)
	if err != nil {
		t.Fatal(err)
	}
	if kept != 4 || dropped != 2 {
		t.Fatalf("kept=%d dropped=%d, want 4/2", kept, dropped)
	}
	got, err := CmapMappings(f.Tables["cmap"])
	if err != nil {
		t.Fatal(err)
	}
	for _, cp := range []uint32{0x20, 'A', '0', 0x00E9} {
		if _, ok := got[cp]; !ok {
			t.Fatalf("U+%04X unexpectedly missing", cp)
		}
	}
	for _, cp := range []uint32{0x4E00, 0x7F51} {
		if _, ok := got[cp]; ok {
			t.Fatalf("U+%04X unexpectedly retained", cp)
		}
	}
}

func TestD4Format4RoundTripGlyphIDs(t *testing.T) {
	want := map[uint32]uint16{
		0x20:   1,
		0x41:   37,
		0x7E:   400,
		0x03A9: 1200,
		0xFFFD: 2000,
	}
	b, err := buildSegoeStyleFormat4Cmap(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := CmapMappings(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("mapping count=%d want %d", len(got), len(want))
	}
	for cp, gid := range want {
		if got[cp] != gid {
			t.Fatalf("U+%04X gid=%d want %d", cp, got[cp], gid)
		}
	}
}
