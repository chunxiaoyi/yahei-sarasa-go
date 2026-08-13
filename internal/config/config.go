package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

const (
	// These source selections intentionally preserve the original project:
	// Sarasa UI SC SemiBold is exposed to Windows as YaHei Regular.
	RegularSource = "SarasaUiSC-SemiBold"
	BoldSource    = "SarasaUiSC-Bold"
	LightSource   = "SarasaUiSC-Light"
	SimSunSource  = "SarasaUiSC-SemiBold"

	// The original Python project explicitly overwrites the font copyright.
	Copyright = "Made from sarasa by GPT"
)

// NameRecord is one raw OpenType name-table identity record extracted from the
// official Windows 11 fonts supplied as the project's compatibility goldens.
// Values are deliberately not normalized: casing, spaces and hyphens must stay
// byte-for-string equivalent to Windows 11's records.
type NameRecord struct {
	PlatformID uint16 `json:"platform_id"`
	EncodingID uint16 `json:"encoding_id"`
	LanguageID uint16 `json:"language_id"`
	NameID     uint16 `json:"name_id"`
	Value      string `json:"value"`
}

type goldenFace struct {
	Weight  uint16       `json:"weight"`
	Records []NameRecord `json:"records"`
}

// FontSpec combines Windows 11 identity records with the intentional rendering
// behavior inherited from yahei-sarasa.
type FontSpec struct {
	GoldenKey   string
	Weight      uint16
	FSSelection uint16
	Version     string
	Identity    []NameRecord
}

//go:embed testdata/windows11-name-golden.json
var goldenJSON []byte

var goldenFaces = mustLoadGolden()

// windows11FSSelection is copied from the official Windows 11 font files used
// as this project's gold standard. Experiment C1 changes only this OS/2 field
// relative to experiment B; vertical metrics remain Sarasa's original values.
var windows11FSSelection = map[string]uint16{
	"Regular":        0x0040,
	"RegularUI":      0x0040,
	"Bold":           0x0020,
	"BoldUI":         0x0020,
	"Light":          0x0100,
	"LightUI":        0x0100,
	"SimSun":         0x0040,
	"NSimSun":        0x0040,
	"SimSunExtB":     0x0040,
	"SegoeUIRegular": 0x0040,
}

func mustLoadGolden() map[string]goldenFace {
	var m map[string]goldenFace
	if err := json.Unmarshal(goldenJSON, &m); err != nil {
		panic(fmt.Sprintf("parse embedded Windows 11 name golden: %v", err))
	}
	return m
}

func spec(key, version string) FontSpec {
	g, ok := goldenFaces[key]
	if !ok {
		panic("missing embedded golden face: " + key)
	}
	fsSelection, ok := windows11FSSelection[key]
	if !ok {
		panic("missing Windows 11 fsSelection golden: " + key)
	}
	return FontSpec{
		GoldenKey:   key,
		Weight:      g.Weight,
		FSSelection: fsSelection,
		Version:     version,
		Identity:    append([]NameRecord(nil), g.Records...),
	}
}

func Regular(version string) FontSpec    { return spec("Regular", version) }
func RegularUI(version string) FontSpec  { return spec("RegularUI", version) }
func Bold(version string) FontSpec       { return spec("Bold", version) }
func BoldUI(version string) FontSpec     { return spec("BoldUI", version) }
func Light(version string) FontSpec      { return spec("Light", version) }
func LightUI(version string) FontSpec    { return spec("LightUI", version) }
func SimSun(version string) FontSpec     { return spec("SimSun", version) }
func NSimSun(version string) FontSpec    { return spec("NSimSun", version) }
func SimSunExtB(version string) FontSpec { return spec("SimSunExtB", version) }

// D4 starts from D2 and adds a Segoe UI Regular 400 output whose glyphs come
// from the same SarasaUiSC-SemiBold source as the project's YaHei Regular.
// Unlike D3, D4 filters cmap to the embedded Windows 11 Segoe UI coverage
// whitelist so CJK continues to fall back to D2's Microsoft YaHei UI faces.
func SegoeUIRegular(version string) FontSpec { return spec("SegoeUIRegular", version) }

// D2 adds two experimental 600-weight faces to the YaHei families. There is
// no Microsoft Win11 SemiBold face to copy, so these identity records are
// intentionally synthetic. Existing Win11-golden faces remain untouched.
//
// The naming extends the actual Win11 Bold conventions where useful (for
// example UI en-US uses a hyphen in the Unique ID while zh-CN does not), while
// adding typographic family/subfamily IDs so DirectWrite can expose the real
// 600 weight as SemiBold.
func semiBoldSpec(ui bool, version string) FontSpec {
	const (
		weight      = uint16(600)
		fsSelection = uint16(0x0100) // WWS; not REGULAR and not legacy BOLD
	)

	if !ui {
		return FontSpec{
			GoldenKey:   "D2SemiBold",
			Weight:      weight,
			FSSelection: fsSelection,
			Version:     version,
			Identity: []NameRecord{
				{3, 1, 0x0409, 1, "Microsoft YaHei"},
				{3, 1, 0x0409, 2, "SemiBold"},
				{3, 1, 0x0409, 3, "Microsoft YaHei-SemiBold"},
				{3, 1, 0x0409, 4, "Microsoft YaHei SemiBold"},
				{3, 1, 0x0409, 6, "MicrosoftYaHei-SemiBold"},
				{3, 1, 0x0409, 16, "Microsoft YaHei"},
				{3, 1, 0x0409, 17, "SemiBold"},
				{3, 1, 0x0804, 1, "微软雅黑"},
				{3, 1, 0x0804, 2, "SemiBold"},
				{3, 1, 0x0804, 3, "微软雅黑-SemiBold"},
				{3, 1, 0x0804, 4, "微软雅黑 SemiBold"},
				{3, 1, 0x0804, 6, "MicrosoftYaHei-SemiBold"},
				{3, 1, 0x0804, 16, "微软雅黑"},
				{3, 1, 0x0804, 17, "SemiBold"},
			},
		}
	}

	return FontSpec{
		GoldenKey:   "D2SemiBoldUI",
		Weight:      weight,
		FSSelection: fsSelection,
		Version:     version,
		Identity: []NameRecord{
			{3, 1, 0x0409, 1, "Microsoft YaHei UI"},
			{3, 1, 0x0409, 2, "SemiBold"},
			{3, 1, 0x0409, 3, "Microsoft YaHei UI-SemiBold"},
			{3, 1, 0x0409, 4, "Microsoft YaHei UI SemiBold"},
			{3, 1, 0x0409, 6, "MicrosoftYaHeiUI-SemiBold"},
			{3, 1, 0x0409, 16, "Microsoft YaHei UI"},
			{3, 1, 0x0409, 17, "SemiBold"},
			{3, 1, 0x0804, 1, "Microsoft YaHei UI"},
			{3, 1, 0x0804, 2, "SemiBold"},
			{3, 1, 0x0804, 3, "Microsoft YaHei UI SemiBold"},
			{3, 1, 0x0804, 4, "Microsoft Yahei UI SemiBold"},
			{3, 1, 0x0804, 6, "MicrosoftYaHeiUI-SemiBold"},
			{3, 1, 0x0804, 16, "Microsoft YaHei UI"},
			{3, 1, 0x0804, 17, "SemiBold"},
		},
	}
}

func SemiBold(version string) FontSpec   { return semiBoldSpec(false, version) }
func SemiBoldUI(version string) FontSpec { return semiBoldSpec(true, version) }

var RequiredSources = []string{
	RegularSource + ".ttf",
	BoldSource + ".ttf",
	LightSource + ".ttf",
}
