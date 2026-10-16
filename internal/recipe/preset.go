package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
)

// Sarasa UI SC source files (single-family "Ui" TTF package).
const (
	SarasaLight    = "SarasaUiSC-Light"
	SarasaRegular  = "SarasaUiSC-Regular"
	SarasaSemiBold = "SarasaUiSC-SemiBold"
	SarasaBold     = "SarasaUiSC-Bold"
)

// SarasaFiles is everything either preset can need. The GUI caches all four
// so the second button never downloads again.
var SarasaFiles = []string{
	SarasaRegular + ".ttf",
	SarasaSemiBold + ".ttf",
	SarasaBold + ".ttf",
	SarasaLight + ".ttf",
}

// Preset is a validated Sarasa slot mapping.
type Preset struct {
	ID      string // CLI / API identifier
	Name    string // short display name
	Summary string
	Map     map[Slot]string // slot -> Sarasa basename (no extension)
}

// PresetSemiBold is D4 in GOLDEN.md: Sarasa UI SC SemiBold is
// exposed as Microsoft YaHei Regular 400 and Segoe UI Regular 400.
var PresetSemiBold = Preset{
	ID:      "semibold",
	Name:    "SemiBold 版",
	Summary: "用 Sarasa UI SC SemiBold 冒充微软雅黑 Regular，正文更粗、更醒目",
	Map: map[Slot]string{
		SlotLight:    SarasaLight,
		SlotRegular:  SarasaSemiBold,
		SlotSemiBold: SarasaSemiBold,
		SlotBold:     SarasaBold,
		SlotSimSun:   SarasaSemiBold,
		SlotSegoe:    SarasaSemiBold,
	},
}

// PresetRegular is D4-Regular in GOLDEN.md: the whole 400
// path (YaHei Regular and Segoe UI Regular) moves to true Sarasa Regular;
// 600, Bold, Light and SimSun are unchanged from D4.
var PresetRegular = Preset{
	ID:      "regular",
	Name:    "标准版",
	Summary: "用 Sarasa UI SC Regular 冒充微软雅黑 Regular，字重接近原生",
	Map: map[Slot]string{
		SlotLight:    SarasaLight,
		SlotRegular:  SarasaRegular,
		SlotSemiBold: SarasaSemiBold,
		SlotBold:     SarasaBold,
		SlotSimSun:   SarasaSemiBold,
		SlotSegoe:    SarasaRegular,
	},
}

var Presets = []Preset{PresetSemiBold, PresetRegular}

func PresetByID(id string) (Preset, error) {
	for _, p := range Presets {
		if p.ID == id {
			return p, nil
		}
	}
	return Preset{}, fmt.Errorf("unknown preset %q (want semibold or regular)", id)
}

// Files returns the distinct Sarasa files the preset reads, sorted.
func (p Preset) Files() []string {
	seen := map[string]bool{}
	var out []string
	for _, base := range p.Map {
		if !seen[base] {
			seen[base] = true
			out = append(out, base+".ttf")
		}
	}
	sort.Strings(out)
	return out
}

// LoadSources parses the preset's Sarasa files from dir. Each file is parsed
// once and shared by every slot that uses it.
func (p Preset) LoadSources(dir string) (map[Slot]*Source, error) {
	byBase := map[string]*Source{}
	for _, file := range p.Files() {
		path := filepath.Join(dir, file)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		f, err := otfont.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		v, err := otfont.VersionFromFont(f)
		if err != nil {
			return nil, fmt.Errorf("%s version: %w", file, err)
		}
		sum := sha256.Sum256(b)
		base := file[:len(file)-len(".ttf")]
		byBase[base] = &Source{Label: file, SHA256: hex.EncodeToString(sum[:]), Font: f, Version: v}
	}
	slots := map[Slot]*Source{}
	for slot, base := range p.Map {
		slots[slot] = byBase[base]
	}
	return slots, nil
}
