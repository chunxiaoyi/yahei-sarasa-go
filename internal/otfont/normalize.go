package otfont

import (
	"errors"
	"fmt"
	"sort"
)

// Fix is one change Normalize applied (shown to the user in the report).
type Fix struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
}

// variationTables are dropped when a TrueType variable font is flattened to
// its default instance: glyf already holds the default outlines, and these
// tables would otherwise reference name IDs the golden name table removes.
var variationTables = []string{"fvar", "gvar", "avar", "cvar", "HVAR", "VVAR", "MVAR", "STAT"}

// NormalizeOptions selects the coverage-dependent fixes.
type NormalizeOptions struct {
	// SetCodePageBits are OR-ed into OS/2.ulCodePageRange1 (for example the
	// GB2312 bit when the font demonstrably covers GB2312).
	SetCodePageBits uint32
}

// Normalize prepares an arbitrary user font for the golden build path. It is
// never applied to the Sarasa presets (whose output must stay byte-identical
// to the regression baselines); for custom fonts it removes the things that
// would make a renamed font misbehave on Windows.
func Normalize(src *Font, opt NormalizeOptions) (*Font, []Fix, error) {
	// Shallow copy: every change below installs a new slice, so unchanged
	// (large) tables stay shared with src.
	f := &Font{ScalerType: src.ScalerType, Tables: make(map[string][]byte, len(src.Tables))}
	for k, v := range src.Tables {
		f.Tables[k] = v
	}
	var fixes []Fix
	add := func(code, format string, args ...any) {
		fixes = append(fixes, Fix{Code: code, Msg: fmt.Sprintf(format, args...)})
	}

	if f.Tables["CFF2"] != nil {
		return nil, nil, errors.New("CFF2 (variable PostScript) outlines are not supported by GDI; instance the font to a static TTF/OTF first")
	}
	for _, tag := range []string{"head", "hhea", "maxp", "OS/2", "cmap", "hmtx"} {
		if f.Tables[tag] == nil {
			return nil, nil, fmt.Errorf("missing required table %q", tag)
		}
	}
	if f.Tables["glyf"] == nil && f.Tables["CFF "] == nil {
		return nil, nil, errors.New("no outline table (glyf or CFF); bitmap-only fonts cannot be system UI fonts")
	}
	if f.Tables["glyf"] != nil && f.Tables["loca"] == nil {
		return nil, nil, errors.New("glyf without loca")
	}
	if len(f.Tables["head"]) < 54 {
		return nil, nil, errors.New("truncated head table")
	}
	if len(f.Tables["OS/2"]) < 78 {
		return nil, nil, fmt.Errorf("OS/2 table too short (%d bytes)", len(f.Tables["OS/2"]))
	}

	if f.ScalerType == ScalerApple && f.Tables["glyf"] != nil {
		f.ScalerType = ScalerTrueType
		add("scaler", "sfnt 版本 'true'（Mac 专用）改为 Windows 标准 0x00010000")
	}

	if f.Tables["fvar"] != nil {
		var dropped []string
		for _, tag := range variationTables {
			if f.Tables[tag] != nil {
				delete(f.Tables, tag)
				dropped = append(dropped, tag)
			}
		}
		add("flatten-variable", "可变字体已固定为默认实例（移除 %v）", dropped)
	} else if f.Tables["STAT"] != nil {
		delete(f.Tables, "STAT")
		add("drop-stat", "移除 STAT 表（它引用的样式名在伪装后不复存在，会干扰 DirectWrite 的字族归类）")
	}
	if f.Tables["DSIG"] != nil {
		delete(f.Tables, "DSIG")
		add("drop-dsig", "移除 DSIG 数字签名（修改后签名必然失效）")
	}

	os2 := append([]byte(nil), f.Tables["OS/2"]...)
	ver := be.Uint16(os2[0:2])
	if (ver == 2 || ver == 3) && len(os2) >= 96 {
		// v2..v4 share one layout; v4 is required for fsSelection bits 7-9
		// (the golden Light/SemiBold values use WWS, bit 8).
		be.PutUint16(os2[0:2], 4)
		add("os2-version", "OS/2 版本 %d → 4（同一结构，golden fsSelection 的 WWS 位需要 v4）", ver)
	}
	if opt.SetCodePageBits != 0 && be.Uint16(os2[0:2]) >= 1 && len(os2) >= 86 {
		cp := be.Uint32(os2[78:82])
		if missing := opt.SetCodePageBits &^ cp; missing != 0 {
			be.PutUint32(os2[78:82], cp|missing)
			add("codepage", "补上 OS/2 代码页位 %s（字体确实覆盖这些字符集；GDI 按字符集挑字体时依赖它）", codePageNames(missing))
		}
	}
	f.Tables["OS/2"] = os2

	if changed, err := ensureWindowsCmap(f); err != nil {
		return nil, nil, err
	} else if changed != "" {
		add("cmap-windows", "%s", changed)
	}
	return f, fixes, nil
}

const (
	CodePageLatin1 = 1 << 0  // 1252
	CodePageGB2312 = 1 << 18 // 936
	CodePageBig5   = 1 << 20 // 950
)

func codePageNames(bits uint32) string {
	var names []string
	if bits&CodePageLatin1 != 0 {
		names = append(names, "1252 Latin-1")
	}
	if bits&CodePageGB2312 != 0 {
		names = append(names, "936 简体中文")
	}
	if bits&CodePageBig5 != 0 {
		names = append(names, "950 繁体中文")
	}
	if len(names) == 0 {
		return fmt.Sprintf("0x%08X", bits)
	}
	return fmt.Sprint(names)
}

// ensureWindowsCmap makes sure Windows finds a Unicode subtable: fonts built
// for macOS sometimes carry only platform-0 records. The existing subtables
// are aliased with (3,1)/(3,10) records; no subtable bytes are rewritten.
func ensureWindowsCmap(f *Font) (string, error) {
	t := f.Tables["cmap"]
	subs := CmapSubtables(t)
	if len(subs) == 0 {
		return "", errors.New("cmap has no subtables")
	}
	has := func(p, e uint16) bool {
		for _, s := range subs {
			if s.Platform == p && s.Encoding == e {
				return true
			}
		}
		return false
	}
	if has(3, 1) || has(3, 10) {
		return "", nil
	}
	if has(3, 0) {
		return "", errors.New("symbol font (cmap 3,0 only); it cannot stand in for a text UI font")
	}
	n := int(be.Uint16(t[2:4]))
	type rec struct {
		p, e uint16
		off  uint32
	}
	recs := make([]rec, 0, n+2)
	var bmp, full uint32
	bmpOK, fullOK := false, false
	for i := 0; i < n; i++ {
		o := 4 + i*8
		r := rec{be.Uint16(t[o:]), be.Uint16(t[o+2:]), be.Uint32(t[o+4:])}
		recs = append(recs, r)
		if r.p != 0 || int(r.off)+2 > len(t) {
			continue
		}
		switch be.Uint16(t[r.off:]) {
		case 4:
			if !bmpOK {
				bmp, bmpOK = r.off, true
			}
		case 12:
			if !fullOK {
				full, fullOK = r.off, true
			}
		}
	}
	if !bmpOK && !fullOK {
		return "", errors.New("no Unicode format 4/12 cmap subtable")
	}
	added := 0
	var msg string
	if bmpOK {
		recs = append(recs, rec{3, 1, bmp})
		added++
		msg = "补上 Windows Unicode cmap 记录 (3,1)"
	}
	if fullOK {
		recs = append(recs, rec{3, 10, full})
		added++
		if msg == "" {
			msg = "补上 Windows Unicode cmap 记录 (3,10)"
		} else {
			msg += " 与 (3,10)"
		}
	}
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].p != recs[j].p {
			return recs[i].p < recs[j].p
		}
		return recs[i].e < recs[j].e
	})
	shift := uint32(8 * added)
	hdr := 4 + 8*n
	out := make([]byte, 0, len(t)+int(shift))
	out = append(out, t[0:2]...)
	out = be.AppendUint16(out, uint16(len(recs)))
	for _, r := range recs {
		out = be.AppendUint16(out, r.p)
		out = be.AppendUint16(out, r.e)
		out = be.AppendUint32(out, r.off+shift)
	}
	out = append(out, t[hdr:]...)
	f.Tables["cmap"] = out
	return msg, nil
}
