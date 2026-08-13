package otfont

import (
	"bytes"
	"testing"
)

func TestPrepareCommonPreservesGasp(t *testing.T) {
	gasp := []byte{0, 1, 0, 1, 0, 16, 0, 15}
	head := make([]byte, 54)
	os2 := make([]byte, 64)
	f := &Font{Tables: map[string][]byte{
		"gasp": bytes.Clone(gasp),
		"head": head,
		"OS/2": os2,
	}}

	if err := f.PrepareCommon(400, 0x0040); err != nil {
		t.Fatal(err)
	}
	got, ok := f.Tables["gasp"]
	if !ok {
		t.Fatal("PrepareCommon removed gasp")
	}
	if !bytes.Equal(got, gasp) {
		t.Fatalf("PrepareCommon changed gasp: got %x want %x", got, gasp)
	}
}
