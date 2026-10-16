package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// Copyright is the project-controlled name ID 0 used by both Sarasa presets.
// The original Python project explicitly overwrites the font copyright.
// Custom-font builds override it per source (see FontSpec.Copyright).
//
// Name ID 0 plays no part in Windows font matching; changing it only changes
// what font viewers show as the copyright notice.
const Copyright = "Made from sarasa by GPT and Claude"

// LegacyCopyright is the name ID 0 written by v1 (the original main and
// regular branches). The regression tests rebuild with it to prove that the
// presets are otherwise byte-for-byte identical to those branches, and the
// system-font page still recognises files made by v1.
const LegacyCopyright = "Made from sarasa by GPT"

// CustomMarkerPrefix starts the name ID 10 (description) record of every
// custom-font build; presets carry no such record (their name ID 0 marks them).
const CustomMarkerPrefix = "Made with yahei-sarasa-go from "

// IsProjectCopyright reports whether s is a name ID 0 this project writes.
func IsProjectCopyright(s string) bool { return s == Copyright || s == LegacyCopyright }

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

	// Copyright is written as Windows en-US name ID 0. Every constructor in
	// this package sets it to the project value above; only custom-font
	// builds replace it with the source font's own notice.
	Copyright string
	// ExtraNames are non-identity records (custom builds only: the source
	// font's license description / URL, name IDs 13 and 14). Presets never
	// set them, so preset name tables stay byte-identical to D4/D4-Regular.
	ExtraNames []NameRecord
	// MacStyle, when non-nil, overwrites head.macStyle (custom builds only)
	// so it agrees with the golden fsSelection. Presets leave it nil and
	// keep the Sarasa source value untouched.
	MacStyle *uint16
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
		Copyright:   Copyright,
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

// SegoeUIRegular is the Windows 11 Segoe UI Regular 400 identity. Its glyph
// source is chosen by the recipe (SemiBold in the "semibold" preset, Regular
// in the "regular" preset); in every case the cmap is filtered to the
// embedded Windows 11 Segoe UI coverage whitelist so CJK continues to fall
// back to D2's Microsoft YaHei UI faces.
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
			Copyright:   Copyright,
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
		Copyright:   Copyright,
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

// Windows11OriginalSHA256 are the hashes of the Windows 11 originals the
// golden records were extracted from (see GOLDEN.md). A system file with a
// different hash is either already replaced or from another Windows build.
var Windows11OriginalSHA256 = map[string]string{
	"msyh.ttc":    "d79c55e68b1131eea0cc1c47be4f572d964f28c682e143db2ad09c1e4cb07a3f",
	"msyhbd.ttc":  "4508821b3dffe01f0ef5e5326a3e60df705a44633858811f67b6982dce3f6ee6",
	"msyhl.ttc":   "7e9bdf90bb5d3fe1b5975fc8ae31944b8fa674122261f92c28d4ec0b9c482fa1",
	"simsun.ttc":  "120a51d2b14eb588700a21e15fc301f0ea55d7c1a4e1f3ae61db83b7c8d42cd6",
	"simsunb.ttf": "a67e70adce24390c43a0c5dfc0856dc81660e91489db930999aa0973780e8a67",
	"segoeui.ttf": "8134dbcd09e7b123c9a7f229d49cffbcb01352cc72ea5e1076b65d0dca9f73cd",
}
