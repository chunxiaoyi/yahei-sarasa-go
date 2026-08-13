package otfont

import (
	"bytes"
	"errors"
	"fmt"
)

type sharedTable struct {
	tag      string
	data     []byte
	checksum uint32
	offset   uint32
}

type tableRef struct {
	tag      string
	data     []byte
	checksum uint32
	shared   *sharedTable
}

func TTC(fonts ...*Font) ([]byte, error) {
	if len(fonts) == 0 {
		return nil, errors.New("ttc: no fonts")
	}

	// TTC version 1.0 header: 'ttcf', 0x00010000, numFonts, offsets[].
	headerLen := 12 + 4*len(fonts)
	dirOffsets := make([]int, len(fonts))
	cursor := align4(headerLen)
	fontTags := make([][]string, len(fonts))
	for i, f := range fonts {
		if f == nil {
			return nil, fmt.Errorf("ttc: font %d is nil", i)
		}
		if h, ok := f.Tables["head"]; !ok || len(h) < 12 {
			return nil, fmt.Errorf("ttc: font %d missing/truncated head", i)
		}
		fontTags[i] = sortedTags(f.Tables)
		dirOffsets[i] = cursor
		cursor += align4(12 + 16*len(fontTags[i]))
	}

	refs := make([][]tableRef, len(fonts))
	buckets := make(map[uint64][]*sharedTable)
	shared := make([]*sharedTable, 0)
	for i, f := range fonts {
		refs[i] = make([]tableRef, 0, len(fontTags[i]))
		for _, tag := range fontTags[i] {
			data := bytes.Clone(f.Tables[tag])
			if tag == "head" {
				// checkSumAdjustment is unused in a TTC and may be zero.
				be.PutUint32(data[8:12], 0)
			}
			ck := tableChecksum(data)
			fp := fingerprint(tag, data)
			var s *sharedTable
			for _, candidate := range buckets[fp] {
				if candidate.tag == tag && bytes.Equal(candidate.data, data) {
					s = candidate
					break
				}
			}
			if s == nil {
				s = &sharedTable{tag: tag, data: data, checksum: ck}
				buckets[fp] = append(buckets[fp], s)
				shared = append(shared, s)
			}
			refs[i] = append(refs[i], tableRef{tag: tag, data: data, checksum: ck, shared: s})
		}
	}

	for _, s := range shared {
		cursor = align4(cursor)
		s.offset = uint32(cursor)
		cursor += align4(len(s.data))
	}
	out := make([]byte, cursor)
	copy(out[0:4], []byte("ttcf"))
	be.PutUint32(out[4:8], 0x00010000)
	be.PutUint32(out[8:12], uint32(len(fonts)))
	for i, off := range dirOffsets {
		be.PutUint32(out[12+i*4:16+i*4], uint32(off))
	}

	for i, f := range fonts {
		d := dirOffsets[i]
		be.PutUint32(out[d:d+4], f.ScalerType)
		n := len(refs[i])
		be.PutUint16(out[d+4:d+6], uint16(n))
		sr, es, rs := searchParams(n)
		be.PutUint16(out[d+6:d+8], sr)
		be.PutUint16(out[d+8:d+10], es)
		be.PutUint16(out[d+10:d+12], rs)
		for j, r := range refs[i] {
			o := d + 12 + j*16
			copy(out[o:o+4], []byte(r.tag))
			be.PutUint32(out[o+4:o+8], r.checksum)
			be.PutUint32(out[o+8:o+12], r.shared.offset)
			be.PutUint32(out[o+12:o+16], uint32(len(r.data)))
		}
	}
	for _, s := range shared {
		copy(out[int(s.offset):int(s.offset)+len(s.data)], s.data)
	}
	return out, nil
}
