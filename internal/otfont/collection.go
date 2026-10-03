package otfont

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
)

// Container identifies the on-disk wrapper of a font file.
type Container string

const (
	ContainerSFNT       Container = "sfnt"       // .ttf / .otf
	ContainerCollection Container = "collection" // .ttc / .otc
	ContainerWOFF       Container = "woff"
)

var (
	ErrWOFF2       = errors.New("WOFF2 is not supported; decompress it to TTF/OTF first")
	ErrUnknownFont = errors.New("not an OpenType/TrueType font")
)

const (
	ScalerTrueType = 0x00010000
	ScalerApple    = 0x74727565 // 'true'
	ScalerCFF      = 0x4F54544F // 'OTTO'
	scalerTyp1     = 0x74797031 // 'typ1'
)

// ParseFaces parses a TTF/OTF, a TTC/OTC (every face) or a WOFF file.
// Faces of a collection are returned as independent Fonts; their shared
// tables are copied, so they can be modified separately.
func ParseFaces(data []byte) ([]*Font, Container, error) {
	if len(data) < 12 {
		return nil, "", errors.New("sfnt: file too short")
	}
	switch string(data[:4]) {
	case "ttcf":
		faces, err := parseCollection(data)
		return faces, ContainerCollection, err
	case "wOFF":
		sfnt, err := DecodeWOFF(data)
		if err != nil {
			return nil, ContainerWOFF, err
		}
		f, err := parseAt(sfnt, 0)
		if err != nil {
			return nil, ContainerWOFF, err
		}
		return []*Font{f}, ContainerWOFF, nil
	case "wOF2":
		return nil, "", ErrWOFF2
	}
	switch be.Uint32(data[0:4]) {
	case ScalerTrueType, ScalerApple, ScalerCFF:
	case scalerTyp1:
		return nil, "", errors.New("sfnt: Mac 'typ1' PostScript fonts are not supported")
	default:
		return nil, "", ErrUnknownFont
	}
	f, err := parseAt(data, 0)
	if err != nil {
		return nil, "", err
	}
	return []*Font{f}, ContainerSFNT, nil
}

func parseCollection(data []byte) ([]*Font, error) {
	if len(data) < 12 {
		return nil, errors.New("ttc: truncated header")
	}
	n := int(be.Uint32(data[8:12]))
	if n <= 0 || n > 1024 || len(data) < 12+4*n {
		return nil, fmt.Errorf("ttc: invalid face count %d", n)
	}
	faces := make([]*Font, 0, n)
	for i := 0; i < n; i++ {
		off := int(be.Uint32(data[12+4*i : 16+4*i]))
		f, err := parseAt(data, off)
		if err != nil {
			return nil, fmt.Errorf("ttc face %d: %w", i, err)
		}
		faces = append(faces, f)
	}
	return faces, nil
}

// parseAt reads one table directory at off; table offsets are absolute in
// the file (that is how both standalone fonts and collections store them).
// Tables alias data instead of copying it: faces of a 10-face CJK collection
// share their large outline tables, and every mutation path in this package
// replaces a table with a fresh slice rather than writing into it.
func parseAt(data []byte, off int) (*Font, error) {
	if off < 0 || off+12 > len(data) {
		return nil, errors.New("sfnt: table directory out of range")
	}
	scaler := be.Uint32(data[off : off+4])
	n := int(be.Uint16(data[off+4 : off+6]))
	if off+12+16*n > len(data) {
		return nil, errors.New("sfnt: truncated table directory")
	}
	f := &Font{ScalerType: scaler, Tables: make(map[string][]byte, n)}
	for i := 0; i < n; i++ {
		d := off + 12 + i*16
		tag := string(data[d : d+4])
		to := int(be.Uint32(data[d+8 : d+12]))
		ln := int(be.Uint32(data[d+12 : d+16]))
		if to < 0 || ln < 0 || to > len(data) || ln > len(data)-to {
			return nil, fmt.Errorf("sfnt: invalid table %q range", tag)
		}
		f.Tables[tag] = data[to : to+ln : to+ln]
	}
	return f, nil
}

// DecodeWOFF converts a WOFF 1.0 file back to a plain sfnt.
func DecodeWOFF(data []byte) ([]byte, error) {
	if len(data) < 44 || string(data[:4]) != "wOFF" {
		return nil, errors.New("woff: bad header")
	}
	flavor := be.Uint32(data[4:8])
	n := int(be.Uint16(data[12:14]))
	if 44+20*n > len(data) {
		return nil, errors.New("woff: truncated table directory")
	}
	f := &Font{ScalerType: flavor, Tables: make(map[string][]byte, n)}
	for i := 0; i < n; i++ {
		d := 44 + 20*i
		tag := string(data[d : d+4])
		off := int(be.Uint32(data[d+4 : d+8]))
		compLen := int(be.Uint32(data[d+8 : d+12]))
		origLen := int(be.Uint32(data[d+12 : d+16]))
		if off < 0 || compLen < 0 || off > len(data) || compLen > len(data)-off || origLen < 0 || origLen > 512<<20 {
			return nil, fmt.Errorf("woff: invalid table %q", tag)
		}
		raw := data[off : off+compLen]
		if compLen < origLen {
			zr, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				return nil, fmt.Errorf("woff: table %q: %w", tag, err)
			}
			out, err := io.ReadAll(io.LimitReader(zr, int64(origLen)+1))
			zr.Close()
			if err != nil {
				return nil, fmt.Errorf("woff: table %q: %w", tag, err)
			}
			if len(out) != origLen {
				return nil, fmt.Errorf("woff: table %q: length %d want %d", tag, len(out), origLen)
			}
			raw = out
		} else if compLen != origLen {
			return nil, fmt.Errorf("woff: table %q: compressed larger than original", tag)
		}
		f.Tables[tag] = bytes.Clone(raw)
	}
	if _, ok := f.Tables["head"]; !ok {
		return nil, errors.New("woff: no head table")
	}
	return f.Bytes()
}
