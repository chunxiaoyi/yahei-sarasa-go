// Package custom turns user-supplied fonts into recipe sources: it inspects
// every face, normalises what would break on Windows, assigns faces to the
// six slots the same way the two Sarasa presets do, and explains every risk
// before anything is built.
package custom

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/charset"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/config"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
)

type Severity string

const (
	SevError Severity = "error" // blocks the outputs that need this slot
	SevWarn  Severity = "warn"  // builds, but the user should know
	SevInfo  Severity = "info"
)

// Issue is one finding shown to the user.
type Issue struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Msg      string   `json:"msg"`
}

func issue(sev Severity, code, format string, args ...any) Issue {
	return Issue{Severity: sev, Code: code, Msg: fmt.Sprintf(format, args...)}
}

// SarasaWinLineHeight is (usWinAscent+usWinDescent)/UPM of Sarasa UI SC
// 1.0.42 (968+241)/1000, the validated reference for line spacing.
const SarasaWinLineHeight = 1.209

// MaxFileSize bounds what Inspect will read (largest real CJK TTCs are
// ~100-250 MiB).
const MaxFileSize = 512 << 20

// File is one inspected font file.
type File struct {
	ID        string           `json:"id"`
	Path      string           `json:"path"`
	Name      string           `json:"name"`
	Size      int64            `json:"size"`
	SHA256    string           `json:"sha256"`
	Container otfont.Container `json:"container"`
	Faces     []*Face          `json:"faces"`
	Error     string           `json:"error,omitempty"`
}

// Face is one inspected face, already normalised when usable.
type Face struct {
	ID        string             `json:"id"` // "<file id>#<index>"
	FileID    string             `json:"fileId"`
	FileName  string             `json:"fileName"`
	Index     int                `json:"index"`
	Label     string             `json:"label"`
	Facts     otfont.Facts       `json:"facts"`
	Weight    uint16             `json:"weight"` // effective weight used for assignment
	Coverage  []charset.Coverage `json:"coverage"`
	SegoeKept int                `json:"segoeKept"`
	Clipped   int                `json:"clipped"` // common glyphs outside usWin metrics
	Issues    []Issue            `json:"issues"`
	Fixes     []otfont.Fix       `json:"fixes"`
	Usable    bool               `json:"usable"`

	path    string
	fileSum string
	font    *otfont.Font // normalised
	version string
}

func (f *Face) coverage(id string) charset.Coverage {
	for _, c := range f.Coverage {
		if c.ID == id {
			return c
		}
	}
	return charset.Coverage{}
}

// LangRank orders faces of pan-CJK families for a Simplified Chinese
// system font: 0 = Simplified Chinese, 1 = unspecified, 2 = Traditional
// Chinese, 3 = Japanese/Korean. CJK collections such as Noto Sans CJK ship
// one face per region with identical weights; this picks the SC face.
func (f *Face) LangRank() int {
	name := " " + strings.ToUpper(strings.Join([]string{f.Facts.Family, f.Facts.TypoFamily, f.Facts.PostScript, f.Facts.FullName}, " ")) + " "
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)
	has := func(tokens ...string) bool {
		for _, t := range tokens {
			if strings.Contains(name, " "+t+" ") {
				return true
			}
		}
		return false
	}
	switch {
	case has("SC", "CN", "GB", "HANS", "SIMPLIFIED", "CHS"):
		return 0
	case has("TC", "HK", "TW", "HANT", "TRADITIONAL", "CHT", "MO"):
		return 2
	case has("JP", "J", "KR", "K", "JA", "KO", "JAPANESE", "KOREAN"):
		return 3
	}
	return 1
}

// Path returns the source file path.
func (f *Face) Path() string { return f.path }

// FileID derives a stable short id from the file hash.
func fileID(sum string) string { return sum[:12] }

// Inspect parses every face of the font at path. A file that cannot be
// parsed at all returns a File with Error set (not a Go error), so the UI
// can show it next to the others.
func Inspect(path string) (*File, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s 是文件夹，请选择字体文件", path)
	}
	if st.Size() > MaxFileSize {
		return nil, fmt.Errorf("%s 太大（%.0f MiB）", filepath.Base(path), float64(st.Size())/(1<<20))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return InspectBytes(path, data), nil
}

// InspectBytes is Inspect for data already in memory.
func InspectBytes(path string, data []byte) *File {
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	file := &File{ID: fileID(hexSum), Path: path, Name: filepath.Base(path), Size: int64(len(data)), SHA256: hexSum}
	faces, container, err := otfont.ParseFaces(data)
	file.Container = container
	if err != nil {
		switch {
		case errors.Is(err, otfont.ErrWOFF2):
			file.Error = "不支持 WOFF2。请先解压为 TTF/OTF，例如：pip install fonttools brotli，然后 fonttools ttLib.woff2 decompress 文件.woff2"
		case errors.Is(err, otfont.ErrUnknownFont):
			file.Error = "不是 TrueType/OpenType 字体文件"
		default:
			file.Error = "无法解析字体：" + err.Error()
		}
		return file
	}
	for i, raw := range faces {
		file.Faces = append(file.Faces, inspectFace(file, i, len(faces), raw))
	}
	return file
}

func inspectFace(file *File, index, count int, raw *otfont.Font) *Face {
	facts := otfont.ReadFacts(raw)
	face := &Face{
		ID:       fmt.Sprintf("%s#%d", file.ID, index),
		FileID:   file.ID,
		FileName: file.Name,
		Index:    index,
		Facts:    facts,
		path:     file.Path,
		fileSum:  file.SHA256,
	}
	face.Label = file.Name
	if count > 1 {
		face.Label = fmt.Sprintf("%s #%d", file.Name, index)
	}
	add := func(i Issue) { face.Issues = append(face.Issues, i) }

	face.Weight = facts.WeightClass
	if face.Weight == 0 {
		face.Weight = 400
	}
	if face.Weight < 100 {
		// Some old fonts store 1..9 instead of 100..900.
		face.Weight *= 100
	}

	mappings, cmapErr := otfont.CmapMappings(raw.Tables["cmap"])
	has := func(r rune) bool { _, ok := mappings[uint32(r)]; return ok }
	if cmapErr == nil {
		for _, set := range charset.All {
			face.Coverage = append(face.Coverage, charset.Measure(set, has))
		}
		for cp := range mappings {
			if cp <= 0xFFFE && config.SegoeUIRegularAllows(cp) {
				face.SegoeKept++
			}
		}
	}

	// Coverage-justified code page bits (GDI picks fonts per charset).
	var cpBits uint32
	if face.coverage("ascii").Ratio == 1 && face.coverage("latin1").Ratio >= 0.95 {
		cpBits |= otfont.CodePageLatin1
	}
	if face.coverage("gb2312-1").Ratio >= 0.99 && face.coverage("gb2312-2").Ratio >= 0.95 {
		cpBits |= otfont.CodePageGB2312
	}

	norm, fixes, err := otfont.Normalize(raw, otfont.NormalizeOptions{SetCodePageBits: cpBits})
	switch {
	case err != nil:
		add(issue(SevError, "unusable", "无法使用：%s", explainNormalizeError(err)))
	case cmapErr != nil:
		add(issue(SevError, "cmap", "cmap 无法读取：%v", cmapErr))
	default:
		face.Usable = true
		face.font = norm
		face.Fixes = fixes
	}

	if facts.Variable {
		def := ""
		for _, a := range facts.Axes {
			if a.Tag == "wght" {
				def = fmt.Sprintf("wght 默认 %g（范围 %g–%g）", a.Default, a.Min, a.Max)
				if w := uint16(math.Round(a.Default)); w >= 100 && w <= 1000 {
					face.Weight = w
				}
			}
		}
		if def == "" {
			def = "无 wght 轴"
		}
		add(issue(SevWarn, "variable", "可变字体：只能使用默认实例（%s），其余 %d 个命名实例不会出现。想要真正的多字重，请先实例化成静态字体：pip install fonttools，然后 fonttools varLib.instancer 字体.ttf wght=600 --static -o 字体-600.ttf", def, facts.Instances))
	}
	if facts.Outline == "CFF" {
		add(issue(SevInfo, "cff", "PostScript/CFF 轮廓（.otf）。Windows 10/11 可正常使用，GDI 小字号下的观感与 TrueType hinting 字体不同"))
	}
	if facts.Outline == "TrueType" && !facts.Hinted {
		add(issue(SevInfo, "unhinted", "没有 TrueType hinting：GDI 程序里的小字号可能偏虚。Sarasa 预设默认用 hinted 版本"))
	}
	if facts.FSType&0x0002 != 0 || facts.FSType&0x0200 != 0 {
		add(issue(SevWarn, "fstype", "字体限制嵌入（fsType=0x%04X）：Office 嵌入字体、打印成 PDF 时可能失败或被替换。工具不会改动授权标志", facts.FSType))
	}
	if facts.Italic() {
		add(issue(SevWarn, "italic", "这是斜体/倾斜字形，伪装后会被当作正体使用"))
	}
	if facts.OS2Version < 2 {
		add(issue(SevInfo, "os2-old", "OS/2 表版本 %d 较旧，部分字段缺失", facts.OS2Version))
	}

	lh := facts.Metrics.WinLineHeight()
	switch {
	case lh == 0:
	case lh < 1.0 || lh > 1.6:
		add(issue(SevWarn, "line-height", "行高 %.2f em（usWinAscent+usWinDescent），Sarasa 为 %.2f em。伪装后系统界面行距会明显%s", lh, SarasaWinLineHeight, map[bool]string{true: "变挤", false: "变松"}[lh < 1.0]))
	case math.Abs(lh-SarasaWinLineHeight) > 0.12:
		add(issue(SevInfo, "line-height", "行高 %.2f em，Sarasa 为 %.2f em，界面行距会略有不同", lh, SarasaWinLineHeight))
	}

	if cmapErr == nil && facts.Outline == "TrueType" {
		if gb, err := otfont.NewGlyphBounds(raw); err == nil {
			tol := int(facts.Metrics.UPM) / 50
			asc, desc := int(facts.Metrics.WinAscent)+tol, int(facts.Metrics.WinDescent)+tol
			var sample []rune
			for _, set := range []charset.Set{charset.ASCII, charset.Punctuation, charset.GB2312L1} {
				sample = append(sample, set.Runes...)
			}
			for _, r := range sample {
				gid, ok := mappings[uint32(r)]
				if !ok {
					continue
				}
				if yMin, yMax, ok := gb.Y(gid); ok && (int(yMax) > asc || -int(yMin) > desc) {
					face.Clipped++
				}
			}
			if face.Clipped > 0 {
				add(issue(SevWarn, "clip", "%d 个常用字的字形超出 usWinAscent/usWinDescent，GDI 程序里可能被切掉顶部或底部", face.Clipped))
			}
		}
	}

	face.version = sourceVersion(raw)
	return face
}

func explainNormalizeError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "CFF2"):
		return "可变 CFF2 字体（OTF 可变）GDI 无法渲染。请先实例化为静态字体：fonttools varLib.instancer 字体.otf wght=400 --static -o 字体-400.otf"
	case strings.Contains(msg, "symbol font"):
		return "这是符号字体（只有 Symbol cmap），不能冒充文字字体"
	case strings.Contains(msg, "bitmap-only"):
		return "只有位图字形，没有矢量轮廓"
	case strings.Contains(msg, "missing required table"):
		return "缺少必需的表：" + strings.TrimPrefix(msg, "missing required table ")
	}
	return msg
}

// sourceVersion reproduces the preset rule (name ID 5 without "Version ",
// up to ';') and falls back to head.fontRevision.
func sourceVersion(f *otfont.Font) string {
	if v, err := otfont.VersionFromFont(f); err == nil && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if h := f.Tables["head"]; len(h) >= 8 {
		rev := float64(int32(uint32(h[4])<<24|uint32(h[5])<<16|uint32(h[6])<<8|uint32(h[7]))) / 65536
		return fmt.Sprintf("%.3f", rev)
	}
	return "1.000"
}
