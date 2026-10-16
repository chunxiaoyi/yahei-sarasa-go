package otfont

import (
	"errors"
	"math"
)

// Axis is one fvar axis.
type Axis struct {
	Tag     string  `json:"tag"`
	Min     float64 `json:"min"`
	Default float64 `json:"default"`
	Max     float64 `json:"max"`
}

// Metrics are the vertical metrics Windows uses for line layout. GDI always
// uses usWin*; DirectWrite uses usWin* too unless fsSelection bit 7
// (USE_TYPO_METRICS) is set, which the golden fsSelection values clear.
type Metrics struct {
	UPM           uint16 `json:"upm"`
	HheaAscender  int16  `json:"hheaAscender"`
	HheaDescender int16  `json:"hheaDescender"`
	HheaLineGap   int16  `json:"hheaLineGap"`
	TypoAscender  int16  `json:"typoAscender"`
	TypoDescender int16  `json:"typoDescender"`
	TypoLineGap   int16  `json:"typoLineGap"`
	WinAscent     uint16 `json:"winAscent"`
	WinDescent    uint16 `json:"winDescent"`
	YMin          int16  `json:"yMin"`
	YMax          int16  `json:"yMax"`
}

// WinLineHeight is (usWinAscent+usWinDescent)/UPM.
func (m Metrics) WinLineHeight() float64 {
	if m.UPM == 0 {
		return 0
	}
	return float64(int(m.WinAscent)+int(m.WinDescent)) / float64(m.UPM)
}

// CmapSubtable is one cmap encoding record.
type CmapSubtable struct {
	Platform uint16 `json:"platform"`
	Encoding uint16 `json:"encoding"`
	Format   uint16 `json:"format"`
}

// Facts is a read-only summary of one face.
type Facts struct {
	Family        string `json:"family"`
	Subfamily     string `json:"subfamily"`
	TypoFamily    string `json:"typoFamily,omitempty"`
	TypoSubfamily string `json:"typoSubfamily,omitempty"`
	FullName      string `json:"fullName"`
	PostScript    string `json:"postScript"`
	LocalFamily   string `json:"localFamily,omitempty"` // zh-CN / zh-TW family if present
	VersionString string `json:"versionString"`
	Copyright     string `json:"copyright,omitempty"`
	License       string `json:"license,omitempty"`
	LicenseURL    string `json:"licenseURL,omitempty"`

	Outline   string `json:"outline"` // TrueType, CFF, CFF2, Bitmap, None
	Scaler    string `json:"scaler"`
	Variable  bool   `json:"variable"`
	Axes      []Axis `json:"axes,omitempty"`
	Instances int    `json:"instances,omitempty"`

	OS2Version  uint16  `json:"os2Version"`
	WeightClass uint16  `json:"weightClass"`
	WidthClass  uint16  `json:"widthClass"`
	FSType      uint16  `json:"fsType"`
	FSSelection uint16  `json:"fsSelection"`
	MacStyle    uint16  `json:"macStyle"`
	ItalicAngle float64 `json:"italicAngle"`
	FixedPitch  bool    `json:"fixedPitch"`
	CodePage1   uint32  `json:"codePage1"`

	NumGlyphs int     `json:"numGlyphs"`
	Metrics   Metrics `json:"metrics"`

	Hinted  bool           `json:"hinted"`
	HasGasp bool           `json:"hasGasp"`
	Cmap    []CmapSubtable `json:"cmap"`
	Tables  []string       `json:"tables"`
}

// Italic reports italic/oblique styling from any of the usual places.
func (f Facts) Italic() bool {
	return f.FSSelection&0x0001 != 0 || f.FSSelection&0x0200 != 0 || f.MacStyle&0x0002 != 0 || math.Abs(f.ItalicAngle) > 0.5
}

func fixed(v uint32) float64 { return float64(int32(v)) / 65536 }

// ReadFacts summarises f. Missing optional tables yield zero values; only a
// missing/unreadable name table is tolerated silently.
func ReadFacts(f *Font) Facts {
	var x Facts
	x.Tables = sortedTags(f.Tables)
	switch f.ScalerType {
	case ScalerTrueType:
		x.Scaler = "TrueType"
	case ScalerApple:
		x.Scaler = "Apple 'true'"
	case ScalerCFF:
		x.Scaler = "OpenType CFF"
	default:
		x.Scaler = "unknown"
	}
	switch {
	case f.Tables["glyf"] != nil && f.Tables["loca"] != nil:
		x.Outline = "TrueType"
	case f.Tables["CFF2"] != nil:
		x.Outline = "CFF2"
	case f.Tables["CFF "] != nil:
		x.Outline = "CFF"
	case f.Tables["EBDT"] != nil || f.Tables["CBDT"] != nil || f.Tables["sbix"] != nil:
		x.Outline = "Bitmap"
	default:
		x.Outline = "None"
	}

	if recs, err := ParseNameRecords(f.Tables["name"]); err == nil {
		x.Family = BestName(recs, 1)
		x.Subfamily = BestName(recs, 2)
		x.FullName = BestName(recs, 4)
		x.PostScript = BestName(recs, 6)
		x.VersionString = BestName(recs, 5)
		x.TypoFamily = BestName(recs, 16)
		x.TypoSubfamily = BestName(recs, 17)
		x.Copyright = BestName(recs, 0)
		x.License = BestName(recs, 13)
		x.LicenseURL = BestName(recs, 14)
		for _, lang := range []uint16{0x0804, 0x0404, 0x0C04, 0x1004, 0x0411, 0x0412} {
			if v := windowsName(recs, 16, lang); v != "" {
				x.LocalFamily = v
				break
			}
			if v := windowsName(recs, 1, lang); v != "" {
				x.LocalFamily = v
				break
			}
		}
	}

	if fv := f.Tables["fvar"]; len(fv) >= 16 {
		x.Variable = true
		axesOff := int(be.Uint16(fv[4:6]))
		axisCount := int(be.Uint16(fv[8:10]))
		axisSize := int(be.Uint16(fv[10:12]))
		x.Instances = int(be.Uint16(fv[12:14]))
		for i := 0; i < axisCount && axisSize >= 20; i++ {
			o := axesOff + i*axisSize
			if o+20 > len(fv) {
				break
			}
			x.Axes = append(x.Axes, Axis{
				Tag:     string(fv[o : o+4]),
				Min:     fixed(be.Uint32(fv[o+4 : o+8])),
				Default: fixed(be.Uint32(fv[o+8 : o+12])),
				Max:     fixed(be.Uint32(fv[o+12 : o+16])),
			})
		}
	}

	if t := f.Tables["OS/2"]; len(t) >= 68 {
		x.OS2Version = be.Uint16(t[0:2])
		x.WeightClass = be.Uint16(t[4:6])
		x.WidthClass = be.Uint16(t[6:8])
		x.FSType = be.Uint16(t[8:10])
		x.FSSelection = be.Uint16(t[62:64])
		if len(t) >= 78 {
			x.Metrics.TypoAscender = int16(be.Uint16(t[68:70]))
			x.Metrics.TypoDescender = int16(be.Uint16(t[70:72]))
			x.Metrics.TypoLineGap = int16(be.Uint16(t[72:74]))
			x.Metrics.WinAscent = be.Uint16(t[74:76])
			x.Metrics.WinDescent = be.Uint16(t[76:78])
		}
		if x.OS2Version >= 1 && len(t) >= 82 {
			x.CodePage1 = be.Uint32(t[78:82])
		}
	}
	if t := f.Tables["head"]; len(t) >= 54 {
		x.Metrics.UPM = be.Uint16(t[18:20])
		x.Metrics.YMin = int16(be.Uint16(t[38:40]))
		x.Metrics.YMax = int16(be.Uint16(t[42:44]))
		x.MacStyle = be.Uint16(t[44:46])
	}
	if t := f.Tables["hhea"]; len(t) >= 10 {
		x.Metrics.HheaAscender = int16(be.Uint16(t[4:6]))
		x.Metrics.HheaDescender = int16(be.Uint16(t[6:8]))
		x.Metrics.HheaLineGap = int16(be.Uint16(t[8:10]))
	}
	if t := f.Tables["maxp"]; len(t) >= 6 {
		x.NumGlyphs = int(be.Uint16(t[4:6]))
	}
	if t := f.Tables["post"]; len(t) >= 16 {
		x.ItalicAngle = fixed(be.Uint32(t[4:8]))
		x.FixedPitch = be.Uint32(t[12:16]) != 0
	}
	x.Hinted = len(f.Tables["fpgm"]) > 0 || len(f.Tables["prep"]) > 0 || len(f.Tables["cvt "]) > 0
	if x.Outline == "CFF" {
		x.Hinted = true // CFF carries its own hints; TrueType bytecode is not applicable
	}
	_, x.HasGasp = f.Tables["gasp"]
	x.Cmap = CmapSubtables(f.Tables["cmap"])
	return x
}

func windowsName(recs []NameRecord, id, lang uint16) string {
	for _, r := range recs {
		if r.PlatformID == platformWindows && r.NameID == id && r.LanguageID == lang && r.Value != "" {
			return r.Value
		}
	}
	return ""
}

// BestName returns name id, preferring Windows en-US, then any Windows
// language, then Unicode, then Macintosh Roman.
func BestName(recs []NameRecord, id uint16) string {
	if v := windowsName(recs, id, langEnglishUS); v != "" {
		return v
	}
	for _, r := range recs {
		if r.PlatformID == platformWindows && r.NameID == id && r.Value != "" {
			return r.Value
		}
	}
	for _, r := range recs {
		if r.PlatformID == platformUnicode && r.NameID == id && r.Value != "" {
			return r.Value
		}
	}
	for _, r := range recs {
		if r.PlatformID == platformMacintosh && r.EncodingID == 0 && r.NameID == id && r.Value != "" {
			return r.Value
		}
	}
	return ""
}

// CmapSubtables lists a cmap's encoding records.
func CmapSubtables(table []byte) []CmapSubtable {
	if len(table) < 4 {
		return nil
	}
	n := int(be.Uint16(table[2:4]))
	var out []CmapSubtable
	for i := 0; i < n && 4+8*i+8 <= len(table); i++ {
		o := 4 + i*8
		st := CmapSubtable{Platform: be.Uint16(table[o : o+2]), Encoding: be.Uint16(table[o+2 : o+4])}
		off := int(be.Uint32(table[o+4 : o+8]))
		if off >= 0 && off+2 <= len(table) {
			st.Format = be.Uint16(table[off : off+2])
		}
		out = append(out, st)
	}
	return out
}

// GlyphYExtents returns per-glyph vertical bounds for a TrueType font, or an
// error for other outline formats. Empty glyphs report ok=false.
type GlyphBounds struct {
	loca  []byte
	glyf  []byte
	long  bool
	count int
}

func NewGlyphBounds(f *Font) (*GlyphBounds, error) {
	head, loca, glyf, maxp := f.Tables["head"], f.Tables["loca"], f.Tables["glyf"], f.Tables["maxp"]
	if len(head) < 54 || loca == nil || glyf == nil || len(maxp) < 6 {
		return nil, errors.New("glyph bounds need TrueType glyf/loca")
	}
	return &GlyphBounds{loca: loca, glyf: glyf, long: be.Uint16(head[50:52]) == 1, count: int(be.Uint16(maxp[4:6]))}, nil
}

func (g *GlyphBounds) offset(i int) (int, bool) {
	if g.long {
		if 4*i+4 > len(g.loca) {
			return 0, false
		}
		return int(be.Uint32(g.loca[4*i:])), true
	}
	if 2*i+2 > len(g.loca) {
		return 0, false
	}
	return 2 * int(be.Uint16(g.loca[2*i:])), true
}

// Y returns (yMin, yMax, ok) for glyph id.
func (g *GlyphBounds) Y(gid uint16) (int16, int16, bool) {
	i := int(gid)
	if i >= g.count {
		return 0, 0, false
	}
	a, ok1 := g.offset(i)
	b, ok2 := g.offset(i + 1)
	if !ok1 || !ok2 || b <= a || a+10 > len(g.glyf) {
		return 0, 0, false
	}
	return int16(be.Uint16(g.glyf[a+4 : a+6])), int16(be.Uint16(g.glyf[a+8 : a+10])), true
}
