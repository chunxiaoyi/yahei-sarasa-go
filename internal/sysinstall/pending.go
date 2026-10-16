package sysinstall

import (
	"strings"
	"unicode/utf16"
)

// PendingOp is one entry of the Session Manager's PendingFileRenameOperations
// value, exactly as stored in the registry. Windows performs these at the
// next boot, before services (including the font cache) start:
//
//	Source "\??\C:\staged.ttc"  Target "!\??\C:\Windows\Fonts\msyh.ttc"  replace
//	Source "\??\C:\cache.dat"   Target ""                                delete
type PendingOp struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// DecodePending parses REG_MULTI_SZ data. The value is a flat list of
// strings read in pairs; an empty target means delete, so empty strings
// cannot simply be treated as the end of the list. Sources are never empty,
// which is what marks the end.
func DecodePending(data []uint16) []PendingOp {
	var strs []string
	start := 0
	for i, c := range data {
		if c == 0 {
			strs = append(strs, string(utf16Decode(data[start:i])))
			start = i + 1
		}
	}
	if start < len(data) { // unterminated tail
		strs = append(strs, string(utf16Decode(data[start:])))
	}
	var ops []PendingOp
	for i := 0; i < len(strs); i += 2 {
		if strs[i] == "" {
			break
		}
		op := PendingOp{Source: strs[i]}
		if i+1 < len(strs) {
			op.Target = strs[i+1]
		}
		ops = append(ops, op)
	}
	return ops
}

// EncodePending produces REG_MULTI_SZ data for ops.
func EncodePending(ops []PendingOp) []uint16 {
	var out []uint16
	for _, op := range ops {
		out = append(out, utf16Encode(op.Source)...)
		out = append(out, 0)
		out = append(out, utf16Encode(op.Target)...)
		out = append(out, 0)
	}
	return append(out, 0)
}

func sameOp(a, b PendingOp) bool {
	return strings.EqualFold(a.Source, b.Source) && strings.EqualFold(a.Target, b.Target)
}

// diffOps returns the entries of after that are not in before (multiset
// difference): what our MoveFileEx calls added.
func diffOps(before, after []PendingOp) []PendingOp {
	used := make([]bool, len(before))
	var out []PendingOp
	for _, a := range after {
		matched := false
		for i, b := range before {
			if !used[i] && sameOp(a, b) {
				used[i], matched = true, true
				break
			}
		}
		if !matched {
			out = append(out, a)
		}
	}
	return out
}

// removeOps removes one occurrence of each op in remove, keeping every
// other program's entries and their order.
func removeOps(all, remove []PendingOp) []PendingOp {
	used := make([]bool, len(remove))
	var out []PendingOp
	for _, a := range all {
		dropped := false
		for i, r := range remove {
			if !used[i] && sameOp(a, r) {
				used[i], dropped = true, true
				break
			}
		}
		if !dropped {
			out = append(out, a)
		}
	}
	return out
}

// countPresent reports how many of ours are still registered.
func countPresent(all, ours []PendingOp) int {
	return len(ours) - len(diffOps(all, ours))
}

func utf16Encode(s string) []uint16 { return utf16.Encode([]rune(s)) }
func utf16Decode(u []uint16) []rune { return utf16.Decode(u) }
