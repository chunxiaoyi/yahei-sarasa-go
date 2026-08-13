package otfont

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"math/bits"
	"sort"
)

var be = binary.BigEndian

type Font struct {
	ScalerType uint32
	Tables     map[string][]byte
}

func Parse(data []byte) (*Font, error) {
	if len(data) < 12 {
		return nil, errors.New("sfnt: file too short")
	}
	if string(data[:4]) == "ttcf" {
		return nil, errors.New("sfnt: TTC input is not supported as a source; expected TTF")
	}
	scaler := be.Uint32(data[0:4])
	n := int(be.Uint16(data[4:6]))
	if len(data) < 12+16*n {
		return nil, errors.New("sfnt: truncated table directory")
	}
	f := &Font{ScalerType: scaler, Tables: make(map[string][]byte, n)}
	for i := 0; i < n; i++ {
		off := 12 + i*16
		tag := string(data[off : off+4])
		to := int(be.Uint32(data[off+8 : off+12]))
		ln := int(be.Uint32(data[off+12 : off+16]))
		if to < 0 || ln < 0 || to > len(data) || ln > len(data)-to {
			return nil, fmt.Errorf("sfnt: invalid table %q range", tag)
		}
		f.Tables[tag] = bytes.Clone(data[to : to+ln])
	}
	return f, nil
}

func (f *Font) Clone() *Font {
	out := &Font{ScalerType: f.ScalerType, Tables: make(map[string][]byte, len(f.Tables))}
	for k, v := range f.Tables {
		out.Tables[k] = bytes.Clone(v)
	}
	return out
}

func (f *Font) DeleteTable(tag string) { delete(f.Tables, tag) }

func (f *Font) SetWeight(weight uint16) error {
	t, ok := f.Tables["OS/2"]
	if !ok || len(t) < 6 {
		return errors.New("sfnt: missing/truncated OS/2 table")
	}
	t = bytes.Clone(t)
	be.PutUint16(t[4:6], weight)
	f.Tables["OS/2"] = t
	return nil
}

func (f *Font) SetFSSelection(fsSelection uint16) error {
	t, ok := f.Tables["OS/2"]
	if !ok || len(t) < 64 {
		return errors.New("sfnt: missing/truncated OS/2 table for fsSelection")
	}
	t = bytes.Clone(t)
	be.PutUint16(t[62:64], fsSelection)
	f.Tables["OS/2"] = t
	return nil
}

func (f *Font) SetClearTypeOptimized() error {
	t, ok := f.Tables["head"]
	if !ok || len(t) < 18 {
		return errors.New("sfnt: missing/truncated head table")
	}
	t = bytes.Clone(t)
	flags := be.Uint16(t[16:18])
	flags |= 1 << 13
	be.PutUint16(t[16:18], flags)
	// Keep head in a collection-safe canonical state. Standalone serialization
	// computes checkSumAdjustment at the very end.
	if len(t) >= 12 {
		be.PutUint32(t[8:12], 0)
	}
	f.Tables["head"] = t
	return nil
}

func (f *Font) PrepareCommon(weight, fsSelection uint16) error {
	// D2 starts from C1 and still preserves Sarasa gasp. Existing faces keep
	// their C1 fsSelection values; the added 600 SemiBold faces use the D2 WWS
	// selection flags. Vertical metrics remain untouched.
	if err := f.SetClearTypeOptimized(); err != nil {
		return err
	}
	if err := f.SetWeight(weight); err != nil {
		return err
	}
	return f.SetFSSelection(fsSelection)
}

func sortedTags(m map[string][]byte) []string {
	tags := make([]string, 0, len(m))
	for tag := range m {
		if len(tag) == 4 {
			tags = append(tags, tag)
		}
	}
	sort.Strings(tags)
	return tags
}

func searchParams(numTables int) (searchRange, entrySelector, rangeShift uint16) {
	if numTables <= 0 {
		return 0, 0, 0
	}
	pow := 1 << (bits.Len(uint(numTables)) - 1)
	searchRange = uint16(pow * 16)
	entrySelector = uint16(bits.Len(uint(pow)) - 1)
	rangeShift = uint16(numTables*16) - searchRange
	return
}

func align4(n int) int { return (n + 3) &^ 3 }

func tableChecksum(data []byte) uint32 {
	var sum uint32
	for i := 0; i < len(data); i += 4 {
		var word [4]byte
		copy(word[:], data[i:min(i+4, len(data))])
		sum += be.Uint32(word[:])
	}
	return sum
}

func wholeChecksum(data []byte) uint32 { return tableChecksum(data) }

func (f *Font) Bytes() ([]byte, error) {
	if _, ok := f.Tables["head"]; !ok {
		return nil, errors.New("sfnt: cannot serialize without head table")
	}
	// Work on copies so Bytes() is side-effect free.
	tables := make(map[string][]byte, len(f.Tables))
	for k, v := range f.Tables {
		tables[k] = bytes.Clone(v)
	}
	head := tables["head"]
	if len(head) < 12 {
		return nil, errors.New("sfnt: truncated head table")
	}
	be.PutUint32(head[8:12], 0)
	tables["head"] = head

	tags := sortedTags(tables)
	n := len(tags)
	dirLen := 12 + 16*n
	cursor := align4(dirLen)
	total := cursor
	for _, tag := range tags {
		total += align4(len(tables[tag]))
	}
	out := make([]byte, total)
	be.PutUint32(out[0:4], f.ScalerType)
	be.PutUint16(out[4:6], uint16(n))
	sr, es, rs := searchParams(n)
	be.PutUint16(out[6:8], sr)
	be.PutUint16(out[8:10], es)
	be.PutUint16(out[10:12], rs)

	headOffset := -1
	for i, tag := range tags {
		data := tables[tag]
		d := 12 + i*16
		copy(out[d:d+4], []byte(tag))
		be.PutUint32(out[d+4:d+8], tableChecksum(data))
		be.PutUint32(out[d+8:d+12], uint32(cursor))
		be.PutUint32(out[d+12:d+16], uint32(len(data)))
		copy(out[cursor:cursor+len(data)], data)
		if tag == "head" {
			headOffset = cursor
		}
		cursor += align4(len(data))
	}
	if headOffset < 0 {
		return nil, errors.New("sfnt: head table not emitted")
	}
	adjustment := uint32(0xB1B0AFBA) - wholeChecksum(out)
	be.PutUint32(out[headOffset+8:headOffset+12], adjustment)
	return out, nil
}

// fingerprint is used only to deduplicate identical TTC tables.
func fingerprint(tag string, b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(tag))
	_, _ = h.Write(b)
	return h.Sum64()
}
