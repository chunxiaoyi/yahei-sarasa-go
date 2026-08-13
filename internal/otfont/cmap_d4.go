package otfont

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"
)

// CmapMappings returns one best-effort Unicode->glyph mapping from a cmap
// table. It prefers Windows UCS-4, then Windows BMP, then Unicode subtables.
// D4 only emits a BMP format-4 cmap for Segoe UI because the Windows 11
// original segoeui.ttf supplied for the project is itself BMP-only.
func CmapMappings(table []byte) (map[uint32]uint16, error) {
	if len(table) < 4 {
		return nil, errors.New("cmap: truncated header")
	}
	n := int(be.Uint16(table[2:4]))
	if len(table) < 4+8*n {
		return nil, errors.New("cmap: truncated encoding records")
	}

	type candidate struct {
		priority int
		offset   int
		format   uint16
	}
	candidates := make([]candidate, 0, n)
	for i := 0; i < n; i++ {
		o := 4 + i*8
		platform := be.Uint16(table[o : o+2])
		encoding := be.Uint16(table[o+2 : o+4])
		subOff := int(be.Uint32(table[o+4 : o+8]))
		if subOff < 0 || subOff+2 > len(table) {
			continue
		}
		format := be.Uint16(table[subOff : subOff+2])
		priority := 100
		switch {
		case platform == 3 && encoding == 10 && format == 12:
			priority = 0
		case platform == 3 && encoding == 1 && format == 4:
			priority = 1
		case platform == 0 && format == 12:
			priority = 2
		case platform == 0 && format == 4:
			priority = 3
		default:
			continue
		}
		candidates = append(candidates, candidate{priority, subOff, format})
	}
	if len(candidates) == 0 {
		return nil, errors.New("cmap: no supported Unicode format 4/12 subtable")
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].priority < candidates[j].priority })
	c := candidates[0]
	if c.format == 12 {
		return parseCmapFormat12(table[c.offset:])
	}
	return parseCmapFormat4(table[c.offset:])
}

func parseCmapFormat12(sub []byte) (map[uint32]uint16, error) {
	if len(sub) < 16 || be.Uint16(sub[0:2]) != 12 {
		return nil, errors.New("cmap format12: truncated header")
	}
	length := int(be.Uint32(sub[4:8]))
	if length < 16 || length > len(sub) {
		return nil, errors.New("cmap format12: invalid length")
	}
	nGroups := int(be.Uint32(sub[12:16]))
	if nGroups < 0 || 16+12*nGroups > length {
		return nil, errors.New("cmap format12: truncated groups")
	}
	out := make(map[uint32]uint16)
	for i := 0; i < nGroups; i++ {
		o := 16 + 12*i
		start := be.Uint32(sub[o : o+4])
		end := be.Uint32(sub[o+4 : o+8])
		startGlyph := be.Uint32(sub[o+8 : o+12])
		if end < start {
			return nil, errors.New("cmap format12: reversed group")
		}
		for cp := start; cp <= end; cp++ {
			gid := startGlyph + (cp - start)
			if gid != 0 && gid <= 0xFFFF {
				out[cp] = uint16(gid)
			}
			if cp == 0xFFFFFFFF { // defensive overflow guard
				break
			}
		}
	}
	return out, nil
}

func parseCmapFormat4(sub []byte) (map[uint32]uint16, error) {
	if len(sub) < 16 || be.Uint16(sub[0:2]) != 4 {
		return nil, errors.New("cmap format4: truncated header")
	}
	length := int(be.Uint16(sub[2:4]))
	if length < 16 || length > len(sub) {
		return nil, errors.New("cmap format4: invalid length")
	}
	segCount := int(be.Uint16(sub[6:8]) / 2)
	if segCount <= 0 {
		return nil, errors.New("cmap format4: zero segments")
	}
	endOff := 14
	startOff := endOff + 2*segCount + 2
	deltaOff := startOff + 2*segCount
	rangeOff := deltaOff + 2*segCount
	if rangeOff+2*segCount > length {
		return nil, errors.New("cmap format4: truncated arrays")
	}

	out := make(map[uint32]uint16)
	for i := 0; i < segCount; i++ {
		end := be.Uint16(sub[endOff+2*i : endOff+2*i+2])
		start := be.Uint16(sub[startOff+2*i : startOff+2*i+2])
		delta := be.Uint16(sub[deltaOff+2*i : deltaOff+2*i+2])
		ro := be.Uint16(sub[rangeOff+2*i : rangeOff+2*i+2])
		if end < start {
			return nil, errors.New("cmap format4: reversed segment")
		}
		for cp := uint32(start); cp <= uint32(end); cp++ {
			if cp == 0xFFFF { // sentinel is not a real output mapping
				continue
			}
			var gid uint16
			if ro == 0 {
				gid = uint16(cp + uint32(delta))
			} else {
				// idRangeOffset is measured from the address of the current
				// idRangeOffset word to the glyphIdArray entry for startCode.
				wordPos := rangeOff + 2*i
				glyphPos := wordPos + int(ro) + 2*int(cp-uint32(start))
				if glyphPos < 0 || glyphPos+2 > length {
					return nil, errors.New("cmap format4: glyph array offset out of range")
				}
				gid = be.Uint16(sub[glyphPos : glyphPos+2])
				if gid != 0 {
					gid = uint16(uint32(gid) + uint32(delta))
				}
			}
			if gid != 0 {
				out[cp] = gid
			}
		}
	}
	return out, nil
}

// FilterCmapToWhitelist replaces f's cmap with a Windows-11-Segoe-shaped
// format-4 cmap containing only source mappings accepted by allow. The two
// encoding records intentionally mirror the supplied Windows 11 segoeui.ttf:
// Unicode platform 0/encoding 3 and Windows platform 3/encoding 1, both
// pointing to the same BMP format-4 subtable.
func (f *Font) FilterCmapToWhitelist(allow func(uint32) bool) (kept, dropped int, err error) {
	table, ok := f.Tables["cmap"]
	if !ok {
		return 0, 0, errors.New("cmap: source font has no cmap table")
	}
	mappings, err := CmapMappings(table)
	if err != nil {
		return 0, 0, err
	}
	filtered := make(map[uint32]uint16)
	for cp, gid := range mappings {
		if cp <= 0xFFFE && allow(cp) {
			filtered[cp] = gid
			kept++
		} else {
			dropped++
		}
	}
	if kept == 0 {
		return 0, dropped, errors.New("cmap: whitelist removed every source mapping")
	}
	built, err := buildSegoeStyleFormat4Cmap(filtered)
	if err != nil {
		return 0, dropped, err
	}
	f.Tables["cmap"] = built
	return kept, dropped, nil
}

func buildSegoeStyleFormat4Cmap(mappings map[uint32]uint16) ([]byte, error) {
	cps := make([]uint16, 0, len(mappings))
	for cp, gid := range mappings {
		if cp > 0xFFFE || gid == 0 {
			continue
		}
		cps = append(cps, uint16(cp))
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i] < cps[j] })
	if len(cps) == 0 {
		return nil, errors.New("cmap: no BMP mappings to build")
	}

	// One segment per code point plus the mandatory U+FFFF sentinel. This is
	// intentionally simple and deterministic; D4's ~2k retained mappings fit
	// comfortably inside format 4's uint16 length limit.
	segCount := len(cps) + 1
	length := 16 + 8*segCount
	if segCount > 0x7FFF || length > 0xFFFF {
		return nil, fmt.Errorf("cmap: format4 too large: segments=%d length=%d", segCount, length)
	}

	sub := make([]byte, length)
	be.PutUint16(sub[0:2], 4)
	be.PutUint16(sub[2:4], uint16(length))
	be.PutUint16(sub[4:6], 0) // language
	be.PutUint16(sub[6:8], uint16(segCount*2))
	pow := 1 << (bits.Len(uint(segCount)) - 1)
	be.PutUint16(sub[8:10], uint16(pow*2))
	be.PutUint16(sub[10:12], uint16(bits.Len(uint(pow))-1))
	be.PutUint16(sub[12:14], uint16(segCount*2-pow*2))

	endOff := 14
	startOff := endOff + 2*segCount + 2
	deltaOff := startOff + 2*segCount
	rangeOff := deltaOff + 2*segCount

	for i, cp := range cps {
		gid := mappings[uint32(cp)]
		be.PutUint16(sub[endOff+2*i:endOff+2*i+2], cp)
		be.PutUint16(sub[startOff+2*i:startOff+2*i+2], cp)
		be.PutUint16(sub[deltaOff+2*i:deltaOff+2*i+2], uint16(uint32(gid)-uint32(cp)))
		be.PutUint16(sub[rangeOff+2*i:rangeOff+2*i+2], 0)
	}
	last := segCount - 1
	be.PutUint16(sub[endOff+2*last:endOff+2*last+2], 0xFFFF)
	be.PutUint16(sub[endOff+2*segCount:endOff+2*segCount+2], 0) // reservedPad
	be.PutUint16(sub[startOff+2*last:startOff+2*last+2], 0xFFFF)
	be.PutUint16(sub[deltaOff+2*last:deltaOff+2*last+2], 1) // FFFF+1 -> glyph 0
	be.PutUint16(sub[rangeOff+2*last:rangeOff+2*last+2], 0)

	// cmap header + two encoding records + one shared format-4 subtable.
	const subOffset = 4 + 2*8
	out := make([]byte, subOffset+len(sub))
	be.PutUint16(out[0:2], 0)
	be.PutUint16(out[2:4], 2)
	// Match original Segoe UI's two cmap records.
	be.PutUint16(out[4:6], 0)
	be.PutUint16(out[6:8], 3)
	be.PutUint32(out[8:12], subOffset)
	be.PutUint16(out[12:14], 3)
	be.PutUint16(out[14:16], 1)
	be.PutUint32(out[16:20], subOffset)
	copy(out[subOffset:], sub)
	return out, nil
}

func VerifyCmapWhitelist(f *Font, allow func(uint32) bool) (int, error) {
	table, ok := f.Tables["cmap"]
	if !ok {
		return 0, errors.New("verify cmap: missing cmap")
	}
	m, err := CmapMappings(table)
	if err != nil {
		return 0, err
	}
	for cp := range m {
		if !allow(cp) {
			return 0, fmt.Errorf("verify cmap: unexpected code point U+%04X", cp)
		}
	}
	for cp := uint32(0x20); cp <= 0x7E; cp++ {
		if _, ok := m[cp]; !ok {
			return 0, fmt.Errorf("verify cmap: required ASCII U+%04X missing", cp)
		}
	}
	return len(m), nil
}
