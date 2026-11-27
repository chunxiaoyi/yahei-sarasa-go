package sysinstall

import (
	"reflect"
	"testing"
	"unicode/utf16"
)

func multiSZ(strs ...string) []uint16 {
	var out []uint16
	for _, s := range strs {
		out = append(out, utf16.Encode([]rune(s))...)
		out = append(out, 0)
	}
	return append(out, 0)
}

func TestDecodePendingKeepsDeletesAndOrder(t *testing.T) {
	// A replace, a delete (empty target), and a path outside the BMP: the
	// empty target must not be read as the end of the list.
	data := multiSZ(
		`\??\C:\ProgramData\s\msyh.ttc`, `!\??\C:\Windows\Fonts\msyh.ttc`,
		`\??\C:\Windows\System32\FNTCACHE.DAT`, ``,
		`\??\C:\其他\𠀀.tmp`, `!\??\C:\x`,
	)
	got := DecodePending(data)
	want := []PendingOp{
		{`\??\C:\ProgramData\s\msyh.ttc`, `!\??\C:\Windows\Fonts\msyh.ttc`},
		{`\??\C:\Windows\System32\FNTCACHE.DAT`, ``},
		{`\??\C:\其他\𠀀.tmp`, `!\??\C:\x`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestDecodePendingEdgeCases(t *testing.T) {
	cases := map[string]struct {
		data []uint16
		want []PendingOp
	}{
		"empty value":           {nil, nil},
		"only terminator":       {[]uint16{0}, nil},
		"double terminator":     {[]uint16{0, 0}, nil},
		"delete at the end":     {multiSZ(`\??\C:\a`, ``), []PendingOp{{`\??\C:\a`, ``}}},
		"unterminated tail":     {utf16.Encode([]rune("\\??\\C:\\a\x00!\\??\\C:\\b")), []PendingOp{{`\??\C:\a`, `!\??\C:\b`}}},
		"source without target": {append(utf16.Encode([]rune(`\??\C:\a`)), 0), []PendingOp{{`\??\C:\a`, ``}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := DecodePending(c.data); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestEncodePendingRoundTrip(t *testing.T) {
	ops := []PendingOp{
		{`\??\C:\s\msyh.ttc`, `!\??\C:\Windows\Fonts\msyh.ttc`},
		{`\??\C:\cache\a.dat`, ``},
		{`\??\C:\cache\b.dat`, ``},
	}
	enc := EncodePending(ops)
	if enc[len(enc)-1] != 0 || enc[len(enc)-2] != 0 {
		t.Fatalf("not double-null terminated: %v", enc[len(enc)-4:])
	}
	if got := DecodePending(enc); !reflect.DeepEqual(got, ops) {
		t.Fatalf("round trip: %#v", got)
	}
	if got := EncodePending(nil); !reflect.DeepEqual(got, []uint16{0}) {
		t.Fatalf("empty list encodes as %v", got)
	}
}

func TestDiffOpsIsMultisetAndCaseInsensitive(t *testing.T) {
	other := PendingOp{`\??\C:\other.tmp`, ``}
	cache := PendingOp{`\??\C:\Windows\System32\FNTCACHE.DAT`, ``}
	before := []PendingOp{other, cache}
	// Another program had already registered the same FNTCACHE delete; ours
	// adds a second copy, and Windows may report a different case.
	after := []PendingOp{other, cache, {`\??\C:\WINDOWS\System32\fntcache.dat`, ``}, {`\??\C:\s\msyh.ttc`, `!\??\C:\Windows\Fonts\msyh.ttc`}}
	got := diffOps(before, after)
	want := after[2:]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if d := diffOps(after, before); d != nil {
		t.Fatalf("nothing was added, got %#v", d)
	}
}

func TestRemoveOpsKeepsOthersInOrder(t *testing.T) {
	a := PendingOp{`\??\C:\a`, ``}
	b := PendingOp{`\??\C:\b`, `!\??\C:\B`}
	mine := PendingOp{`\??\C:\mine`, ``}
	all := []PendingOp{a, mine, b, mine, a}
	// Removing one of ours drops exactly one occurrence.
	got := removeOps(all, []PendingOp{{`\??\c:\MINE`, ``}})
	if want := []PendingOp{a, b, mine, a}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if got := removeOps(all, nil); !reflect.DeepEqual(got, all) {
		t.Fatalf("removing nothing changed the list: %#v", got)
	}
	// An op that is not registered changes nothing.
	if got := removeOps([]PendingOp{a, b}, []PendingOp{mine}); !reflect.DeepEqual(got, []PendingOp{a, b}) {
		t.Fatalf("got %#v", got)
	}
}

func TestCountPresent(t *testing.T) {
	x := PendingOp{`\??\C:\x`, ``}
	y := PendingOp{`\??\C:\y`, `!\??\C:\Y`}
	ours := []PendingOp{x, y}
	for _, c := range []struct {
		all  []PendingOp
		want int
	}{
		{nil, 0},
		{[]PendingOp{y}, 1},
		{[]PendingOp{{`\??\C:\other`, ``}, x, y}, 2},
		{[]PendingOp{x, x}, 1}, // a duplicate of one of ours counts once
	} {
		if got := countPresent(c.all, ours); got != c.want {
			t.Errorf("countPresent(%v) = %d, want %d", c.all, got, c.want)
		}
	}
}
