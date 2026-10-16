// Package recipe describes how Windows 11 font identities are filled with
// glyph sources, and builds the six replacement files from that description.
//
// The two validated Sarasa variants (formerly the "main" and "regular" git
// branches) are expressed as presets over the same build path. That build path
// performs exactly the operations the D4 / D4-Regular programs performed, in
// the same order, so preset output is byte-for-byte identical to them; the
// regression tests in this package pin that.
package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/config"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/progress"
)

// Slot is one weight/role a glyph source can fill.
type Slot string

const (
	SlotLight    Slot = "light"    // Microsoft YaHei / UI Light 300    -> msyhl.ttc
	SlotRegular  Slot = "regular"  // Microsoft YaHei / UI Regular 400  -> msyh.ttc
	SlotSemiBold Slot = "semibold" // Microsoft YaHei / UI SemiBold 600 -> msyhbd.ttc faces 2/3
	SlotBold     Slot = "bold"     // Microsoft YaHei / UI Bold 700     -> msyhbd.ttc faces 0/1
	SlotSimSun   Slot = "simsun"   // SimSun / NSimSun / SimSun-ExtB 400 -> simsun.ttc, simsunb.ttf
	SlotSegoe    Slot = "segoe"    // Segoe UI Regular 400              -> segoeui.ttf (cmap whitelisted)
)

// AllSlots lists slots in display order.
var AllSlots = []Slot{SlotLight, SlotRegular, SlotSemiBold, SlotBold, SlotSimSun, SlotSegoe}

// TargetWeight is the OS/2 weight class Windows will see for a slot.
func (s Slot) TargetWeight() uint16 {
	switch s {
	case SlotLight:
		return 300
	case SlotSemiBold:
		return 600
	case SlotBold:
		return 700
	default:
		return 400
	}
}

// NeedsCJK reports whether the slot backs a Chinese family name.
func (s Slot) NeedsCJK() bool { return s != SlotSegoe }

func (s Slot) Valid() bool {
	for _, k := range AllSlots {
		if k == s {
			return true
		}
	}
	return false
}

// Output file names, in the order the original program generated them.
const (
	OutSegoeUI = "segoeui.ttf"
	OutMSYH    = "msyh.ttc"
	OutMSYHBD  = "msyhbd.ttc"
	OutMSYHL   = "msyhl.ttc"
	OutSimSun  = "simsun.ttc"
	OutSimSunB = "simsunb.ttf"
)

var AllOutputs = []string{OutSegoeUI, OutMSYH, OutMSYHBD, OutMSYHL, OutSimSun, OutSimSunB}

type faceDef struct {
	slot Slot
	spec func(version string) config.FontSpec
}

type outputDef struct {
	name       string
	collection bool
	segoeCmap  bool
	faces      []faceDef
}

// outputDefs is the D4 layout. Face order inside each collection is part of
// the validated contract (e.g. msyhbd.ttc: Bold, Bold UI, SemiBold, SemiBold UI).
var outputDefs = []outputDef{
	{name: OutSegoeUI, segoeCmap: true, faces: []faceDef{{SlotSegoe, config.SegoeUIRegular}}},
	{name: OutMSYH, collection: true, faces: []faceDef{{SlotRegular, config.Regular}, {SlotRegular, config.RegularUI}}},
	{name: OutMSYHBD, collection: true, faces: []faceDef{
		{SlotBold, config.Bold}, {SlotBold, config.BoldUI},
		{SlotSemiBold, config.SemiBold}, {SlotSemiBold, config.SemiBoldUI},
	}},
	{name: OutMSYHL, collection: true, faces: []faceDef{{SlotLight, config.Light}, {SlotLight, config.LightUI}}},
	{name: OutSimSun, collection: true, faces: []faceDef{{SlotSimSun, config.SimSun}, {SlotSimSun, config.NSimSun}}},
	{name: OutSimSunB, faces: []faceDef{{SlotSimSun, config.SimSunExtB}}},
}

// SlotsFor returns the slots an output file draws from.
func SlotsFor(output string) []Slot {
	for _, d := range outputDefs {
		if d.name != output {
			continue
		}
		var out []Slot
		seen := map[Slot]bool{}
		for _, f := range d.faces {
			if !seen[f.slot] {
				seen[f.slot] = true
				out = append(out, f.slot)
			}
		}
		return out
	}
	return nil
}

// Source is a parsed glyph source plus the identity details that are not
// taken from the Windows 11 golden records.
type Source struct {
	Label   string // shown in logs/manifests, e.g. "SarasaUiSC-SemiBold.ttf"
	SHA256  string // of the file the face came from (informational)
	Font    *otfont.Font
	Version string // name ID 5

	// Custom-font only. Zero values reproduce the preset behaviour exactly.
	Copyright   string
	ExtraNames  []config.NameRecord
	FixMacStyle bool
}

// Plan is a complete build request.
type Plan struct {
	Slots   map[Slot]*Source
	Outputs []string // subset of AllOutputs; nil means all six
}

// FaceInfo documents one face of a generated file.
type FaceInfo struct {
	Index       int    `json:"index"`
	GoldenKey   string `json:"golden"`
	Family      string `json:"family"`
	Subfamily   string `json:"subfamily"`
	Weight      uint16 `json:"weight"`
	FSSelection string `json:"fsSelection"`
	Slot        Slot   `json:"slot"`
	Source      string `json:"source"`
	Version     string `json:"version"`
}

// OutputInfo documents one generated file.
type OutputInfo struct {
	Name   string     `json:"name"`
	Size   int64      `json:"size"`
	SHA256 string     `json:"sha256"`
	Faces  []FaceInfo `json:"faces"`
	Note   string     `json:"note,omitempty"`
}

func (p Plan) outputs() ([]outputDef, error) {
	if p.Outputs == nil {
		return outputDefs, nil
	}
	want := map[string]bool{}
	for _, n := range p.Outputs {
		found := false
		for _, d := range outputDefs {
			if d.name == n {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("recipe: unknown output %q", n)
		}
		want[n] = true
	}
	var defs []outputDef
	for _, d := range outputDefs {
		if want[d.name] {
			defs = append(defs, d)
		}
	}
	if len(defs) == 0 {
		return nil, errors.New("recipe: no outputs selected")
	}
	return defs, nil
}

// OutputNames returns the canonical, ordered list of files the plan builds.
func (p Plan) OutputNames() ([]string, error) {
	defs, err := p.outputs()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.name
	}
	return names, nil
}

func (p Plan) faceSpec(fd faceDef, src *Source) config.FontSpec {
	spec := fd.spec(src.Version)
	if src.Copyright != "" {
		spec.Copyright = src.Copyright
	}
	if len(src.ExtraNames) > 0 {
		spec.ExtraNames = append([]config.NameRecord(nil), src.ExtraNames...)
	}
	if src.FixMacStyle {
		// Agree with the golden fsSelection: bold bit only for the 700 faces,
		// never italic.
		var ms uint16
		if spec.FSSelection&0x0020 != 0 {
			ms = 0x0001
		}
		spec.MacStyle = &ms
	}
	return spec
}

// Build generates every selected output into dir (which must exist) and
// returns a description of each file. Files are written as soon as they are
// complete so peak memory stays close to the original single-pass program.
func Build(p Plan, dir string, sink progress.Sink) ([]OutputInfo, error) {
	defs, err := p.outputs()
	if err != nil {
		return nil, err
	}
	for _, d := range defs {
		for _, fd := range d.faces {
			if src := p.Slots[fd.slot]; src == nil || src.Font == nil {
				return nil, fmt.Errorf("%s: slot %q has no glyph source", d.name, fd.slot)
			}
		}
	}

	var infos []OutputInfo
	sink.Step(0, len(defs))
	for _, d := range defs {
		data, info, err := p.buildOne(d)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, d.name), data, 0o644); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		info.Size = int64(len(data))
		info.SHA256 = hex.EncodeToString(sum[:])
		infos = append(infos, info)
		sink.Step(len(infos), len(defs))
		if info.Note != "" {
			sink.OKf("已生成 %s（%s）", d.name, info.Note)
		} else {
			sink.OKf("已生成 %s", d.name)
		}
	}
	return infos, nil
}

func (p Plan) buildOne(d outputDef) ([]byte, OutputInfo, error) {
	info := OutputInfo{Name: d.name}
	fonts := make([]*otfont.Font, 0, len(d.faces))
	for i, fd := range d.faces {
		src := p.Slots[fd.slot]
		spec := p.faceSpec(fd, src)
		f, err := otfont.ApplySpec(src.Font, spec)
		if err != nil {
			if d.collection {
				return nil, info, fmt.Errorf("%s face %d apply: %w", d.name, i, err)
			}
			return nil, info, err
		}
		if d.segoeCmap {
			kept, dropped, err := f.FilterCmapToWhitelist(config.SegoeUIRegularAllows)
			if err != nil {
				return nil, info, fmt.Errorf("%s cmap filter: %w", d.name, err)
			}
			if err := otfont.VerifySpec(f, spec); err != nil {
				return nil, info, fmt.Errorf("%s: %w", d.name, err)
			}
			verified, err := otfont.VerifyCmapWhitelist(f, config.SegoeUIRegularAllows)
			if err != nil {
				return nil, info, fmt.Errorf("%s: %w", d.name, err)
			}
			if verified != kept {
				return nil, info, fmt.Errorf("%s: cmap count changed after build: kept=%d verify=%d", d.name, kept, verified)
			}
			info.Note = fmt.Sprintf("字形来自 %s，按 Win11 Segoe 白名单保留 %d 个码位、去掉 %d 个", src.Label, kept, dropped)
		} else if err := otfont.VerifySpec(f, spec); err != nil {
			if d.collection {
				return nil, info, fmt.Errorf("%s face %d: %w", d.name, i, err)
			}
			return nil, info, fmt.Errorf("%s: %w", d.name, err)
		}
		fonts = append(fonts, f)
		info.Faces = append(info.Faces, describeFace(i, fd.slot, spec, src))
	}

	if d.collection {
		data, err := otfont.TTC(fonts...)
		return data, info, err
	}
	data, err := fonts[0].Bytes()
	return data, info, err
}

func describeFace(index int, slot Slot, spec config.FontSpec, src *Source) FaceInfo {
	fi := FaceInfo{
		Index:       index,
		GoldenKey:   spec.GoldenKey,
		Weight:      spec.Weight,
		FSSelection: fmt.Sprintf("0x%04X", spec.FSSelection),
		Slot:        slot,
		Source:      src.Label,
		Version:     spec.Version,
	}
	for _, r := range spec.Identity {
		if r.PlatformID == 3 && r.LanguageID == 0x0409 {
			switch r.NameID {
			case 1:
				fi.Family = r.Value
			case 2:
				fi.Subfamily = r.Value
			}
		}
	}
	// Prefer the typographic subfamily (Light / SemiBold) when present.
	for _, r := range spec.Identity {
		if r.PlatformID == 3 && r.LanguageID == 0x0409 && r.NameID == 17 {
			fi.Subfamily = r.Value
		}
		if r.PlatformID == 3 && r.LanguageID == 0x0409 && r.NameID == 16 {
			fi.Family = r.Value
		}
	}
	return fi
}

// VerifyDir checks that dir contains exactly the named files and nothing
// else (the original program's "exactly 6 files" contract, generalised to
// custom output subsets).
func VerifyDir(dir string, names []string) error {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != len(want) {
		return fmt.Errorf("result: expected exactly %d files, got %d", len(want), len(entries))
	}
	for _, e := range entries {
		if e.IsDir() || !want[e.Name()] {
			return fmt.Errorf("result: unexpected entry %q", e.Name())
		}
	}
	return nil
}

// DistinctSources returns the plan's sources in slot order without duplicates.
func (p Plan) DistinctSources() []*Source {
	seen := map[*Source]bool{}
	var out []*Source
	for _, s := range AllSlots {
		if src := p.Slots[s]; src != nil && !seen[src] {
			seen[src] = true
			out = append(out, src)
		}
	}
	return out
}

// SortedSlots returns map keys in display order (helper for manifests).
func SortedSlots(m map[Slot]*Source) []Slot {
	out := make([]Slot, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	order := map[Slot]int{}
	for i, s := range AllSlots {
		order[s] = i
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out
}
