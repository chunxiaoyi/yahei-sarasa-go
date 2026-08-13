package otfont

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/chunxiaoyi/yahei-sarasa-semibold-go/internal/config"
)

const (
	platformUnicode    = 0
	platformMacintosh  = 1
	platformWindows    = 3
	encodingUnicodeBMP = 1
	langEnglishUS      = 0x0409
)

type NameRecord struct {
	PlatformID uint16
	EncodingID uint16
	LanguageID uint16
	NameID     uint16
	Value      string
}

func encodeUTF16BE(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.BigEndian.PutUint16(b[i*2:], v)
	}
	return b
}

func decodeUTF16BE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.BigEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u))
}

func encodeNameString(r NameRecord) ([]byte, error) {
	switch r.PlatformID {
	case platformUnicode, platformWindows:
		return encodeUTF16BE(r.Value), nil
	case platformMacintosh:
		// The Windows 11 goldens only contribute ASCII Macintosh identity
		// records for SimSun/NSimSun/SimSun-ExtB. Do not invent a lossy
		// MacRoman conversion if this ever changes.
		out := make([]byte, 0, len(r.Value))
		for _, ch := range r.Value {
			if ch > 0x7f {
				return nil, fmt.Errorf("name: non-ASCII Macintosh golden string %q", r.Value)
			}
			out = append(out, byte(ch))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("name: unsupported platform %d", r.PlatformID)
	}
}

func decodeNameString(platform uint16, raw []byte) string {
	if platform == platformUnicode || platform == platformWindows {
		return decodeUTF16BE(raw)
	}
	return string(raw)
}

// BuildNameTable constructs the output name table from two intentionally
// separate sources:
//   - Windows 11 official-font identity records (IDs 1,2,3,4,6,16,17),
//     extracted verbatim into config/testdata/windows11-name-golden.json;
//   - the original yahei-sarasa project's own copyright/version behavior
//     (English Windows ID 0 and ID 5).
//
// This deliberately preserves odd Windows 11 spellings such as Yahei and
// UI-Bold. We never normalize case, hyphens, localized family names, or the
// Light family's legacy/typographic split.
func BuildNameTable(spec config.FontSpec) ([]byte, error) {
	records := make([]NameRecord, 0, len(spec.Identity)+2)
	for _, r := range spec.Identity {
		records = append(records, NameRecord{
			PlatformID: r.PlatformID,
			EncodingID: r.EncodingID,
			LanguageID: r.LanguageID,
			NameID:     r.NameID,
			Value:      r.Value,
		})
	}
	// These two fields are explicitly set by the original Python project and
	// are not Windows identity fields.
	records = append(records,
		NameRecord{PlatformID: platformWindows, EncodingID: encodingUnicodeBMP, LanguageID: langEnglishUS, NameID: 0, Value: config.Copyright},
		NameRecord{PlatformID: platformWindows, EncodingID: encodingUnicodeBMP, LanguageID: langEnglishUS, NameID: 5, Value: spec.Version},
	)

	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.PlatformID != b.PlatformID {
			return a.PlatformID < b.PlatformID
		}
		if a.EncodingID != b.EncodingID {
			return a.EncodingID < b.EncodingID
		}
		if a.LanguageID != b.LanguageID {
			return a.LanguageID < b.LanguageID
		}
		if a.NameID != b.NameID {
			return a.NameID < b.NameID
		}
		return a.Value < b.Value
	})

	storage := bytes.Buffer{}
	type encodedRecord struct {
		NameRecord
		raw []byte
		off uint16
	}
	enc := make([]encodedRecord, len(records))
	for i, r := range records {
		raw, err := encodeNameString(r)
		if err != nil {
			return nil, err
		}
		if storage.Len() > 0xffff || len(raw) > 0xffff || storage.Len()+len(raw) > 0xffff {
			return nil, errors.New("name: table string storage exceeds format-0 limit")
		}
		enc[i] = encodedRecord{NameRecord: r, raw: raw, off: uint16(storage.Len())}
		_, _ = storage.Write(raw)
	}

	count := len(enc)
	stringOffset := 6 + count*12
	if count > 0xffff || stringOffset > 0xffff {
		return nil, errors.New("name: record array too large")
	}
	out := make([]byte, stringOffset+storage.Len())
	be.PutUint16(out[0:2], 0)
	be.PutUint16(out[2:4], uint16(count))
	be.PutUint16(out[4:6], uint16(stringOffset))
	for i, r := range enc {
		o := 6 + i*12
		be.PutUint16(out[o:o+2], r.PlatformID)
		be.PutUint16(out[o+2:o+4], r.EncodingID)
		be.PutUint16(out[o+4:o+6], r.LanguageID)
		be.PutUint16(out[o+6:o+8], r.NameID)
		be.PutUint16(out[o+8:o+10], uint16(len(r.raw)))
		be.PutUint16(out[o+10:o+12], r.off)
	}
	copy(out[stringOffset:], storage.Bytes())
	return out, nil
}

func ParseNameRecords(table []byte) ([]NameRecord, error) {
	if len(table) < 6 {
		return nil, errors.New("name: truncated table")
	}
	format := be.Uint16(table[0:2])
	if format != 0 && format != 1 {
		return nil, fmt.Errorf("name: unsupported format %d", format)
	}
	count := int(be.Uint16(table[2:4]))
	storageOffset := int(be.Uint16(table[4:6]))
	if len(table) < 6+12*count || storageOffset > len(table) {
		return nil, errors.New("name: truncated records/storage")
	}
	out := make([]NameRecord, 0, count)
	for i := 0; i < count; i++ {
		o := 6 + i*12
		p := be.Uint16(table[o : o+2])
		e := be.Uint16(table[o+2 : o+4])
		l := be.Uint16(table[o+4 : o+6])
		n := be.Uint16(table[o+6 : o+8])
		ln := int(be.Uint16(table[o+8 : o+10]))
		so := int(be.Uint16(table[o+10 : o+12]))
		start := storageOffset + so
		if start < 0 || ln < 0 || start > len(table) || ln > len(table)-start {
			return nil, errors.New("name: invalid string range")
		}
		raw := table[start : start+ln]
		out = append(out, NameRecord{p, e, l, n, decodeNameString(p, raw)})
	}
	return out, nil
}

func VersionFromFont(f *Font) (string, error) {
	t, ok := f.Tables["name"]
	if !ok {
		return "", errors.New("name: source font has no name table")
	}
	recs, err := ParseNameRecords(t)
	if err != nil {
		return "", err
	}
	// Reproduce FontForge import + original get_version(font): FontForge strips
	// the leading "Version ", then Python takes the segment before ';'.
	normalize := func(v string) string {
		if strings.HasPrefix(v, "Version ") {
			v = strings.TrimPrefix(v, "Version ")
			if strings.HasSuffix(v, " ") {
				v = strings.TrimSuffix(v, " ")
			}
		}
		return strings.SplitN(v, ";", 2)[0]
	}
	for _, r := range recs {
		if r.PlatformID == platformWindows && r.LanguageID == langEnglishUS && r.NameID == 5 {
			return normalize(r.Value), nil
		}
	}
	for _, r := range recs {
		if r.NameID == 5 && r.Value != "" {
			return normalize(r.Value), nil
		}
	}
	return "", errors.New("name: source font has no version (name ID 5)")
}

func ApplySpec(src *Font, spec config.FontSpec) (*Font, error) {
	f := src.Clone()
	if err := f.PrepareCommon(spec.Weight, spec.FSSelection); err != nil {
		return nil, err
	}
	name, err := BuildNameTable(spec)
	if err != nil {
		return nil, err
	}
	f.Tables["name"] = name
	return f, nil
}
