// Package display looks at the screens of the computer the tool runs on and
// recommends whether to use the hinted or unhinted Sarasa package.
//
// What matters for hinting is how many pixels a line of UI text gets, i.e.
// each monitor's effective DPI (the Windows scaling setting), not its
// resolution: 9 pt UI text is 12 px at 100 % and 18 px at 150 %. The
// replaced fonts are system-wide, so the lowest-scaled monitor decides.
package display

import (
	"fmt"
	"sort"
	"strings"
)

// Monitor is one attached display.
type Monitor struct {
	Name    string `json:"name"`
	Width   int    `json:"width"`  // physical pixels
	Height  int    `json:"height"` // physical pixels
	DPI     int    `json:"dpi"`    // effective DPI (96 = 100 %)
	Scale   int    `json:"scale"`  // percent
	Primary bool   `json:"primary"`
}

// Smoothing is the system font-smoothing setting.
type Smoothing string

const (
	SmoothingUnknown   Smoothing = ""
	SmoothingClearType Smoothing = "cleartype"
	SmoothingGrayscale Smoothing = "grayscale"
	SmoothingOff       Smoothing = "off"
)

// Info is what Detect found.
type Info struct {
	Supported bool      `json:"supported"`
	Monitors  []Monitor `json:"monitors"`
	Smoothing Smoothing `json:"smoothing"`
	Error     string    `json:"error,omitempty"`
}

// HighDPIScale is the scaling (percent) from which hinting stops paying off:
// 150 % puts 9 pt UI text at 18 px, where grid-fitting changes little.
const HighDPIScale = 150

// Recommendation is the automatic hinted/unhinted decision.
type Recommendation struct {
	Hinted bool   `json:"hinted"`
	Reason string `json:"reason"`
	// Basis is a short description of what was detected, for display.
	Basis string `json:"basis"`
}

// ScaleFromDPI converts an effective DPI to a scaling percentage.
func ScaleFromDPI(dpi int) int {
	if dpi <= 0 {
		return 0
	}
	return (dpi*100 + 48) / 96
}

// Recommend applies the rule:
//
//	font smoothing off          -> hinted (aliased text depends on hints)
//	nothing detected            -> hinted (the safe default)
//	every monitor >= 150 %      -> unhinted (smaller files, design shapes)
//	otherwise                   -> hinted
func Recommend(info Info) Recommendation {
	basis := Describe(info)
	if !info.Supported || len(info.Monitors) == 0 {
		why := "无法检测屏幕"
		if info.Error != "" {
			why += "（" + info.Error + "）"
		}
		return Recommendation{Hinted: true, Basis: basis, Reason: why + "，按默认使用 hinted"}
	}
	if info.Smoothing == SmoothingOff {
		return Recommendation{Hinted: true, Basis: basis, Reason: "系统关闭了字体平滑，没有抗锯齿时文字清晰度完全依赖 hinting"}
	}
	low := lowest(info.Monitors)
	if low.Scale >= HighDPIScale {
		what := fmt.Sprintf("屏幕缩放 %d%%", low.Scale)
		if len(info.Monitors) > 1 {
			what = fmt.Sprintf("所有屏幕缩放都在 %d%% 及以上（最低 %d%%）", HighDPIScale, low.Scale)
		}
		return Recommendation{Hinted: false, Basis: basis,
			Reason: fmt.Sprintf("%s，界面小字已有约 %d 像素，hinting 收益很小；unhinted 文件更小、字形更接近设计", what, 12*low.Scale/100)}
	}
	return Recommendation{Hinted: true, Basis: basis,
		Reason: fmt.Sprintf("%s缩放 %d%%，界面小字约 %d 像素，hinting 能让传统程序里的小字明显更清晰", monitorLabel(info.Monitors, low), low.Scale, 12*low.Scale/100)}
}

// HintingMatters reports whether unhinted custom fonts deserve a warning
// on this computer.
func HintingMatters(info Info) bool { return Recommend(info).Hinted }

func lowest(ms []Monitor) Monitor {
	sorted := append([]Monitor(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Scale < sorted[j].Scale })
	return sorted[0]
}

func monitorLabel(all []Monitor, m Monitor) string {
	if len(all) == 1 {
		return "屏幕"
	}
	for i, x := range all {
		if x == m {
			if x.Primary {
				return fmt.Sprintf("屏幕 %d（主屏）", i+1)
			}
			return fmt.Sprintf("屏幕 %d", i+1)
		}
	}
	return "屏幕"
}

// Describe summarises the detected setup in one line.
func Describe(info Info) string {
	if !info.Supported {
		return "这个系统上无法检测屏幕"
	}
	if len(info.Monitors) == 0 {
		return "没有检测到屏幕"
	}
	var parts []string
	for i, m := range info.Monitors {
		label := fmt.Sprintf("屏幕 %d", i+1)
		if m.Primary && len(info.Monitors) > 1 {
			label += "（主屏）"
		}
		parts = append(parts, fmt.Sprintf("%s %d×%d，缩放 %d%%", label, m.Width, m.Height, m.Scale))
	}
	s := strings.Join(parts, "；")
	switch info.Smoothing {
	case SmoothingClearType:
		s += "；ClearType 已开启"
	case SmoothingGrayscale:
		s += "；灰度抗锯齿"
	case SmoothingOff:
		s += "；字体平滑已关闭"
	}
	return s
}

// Mode is the user's hinted setting.
type Mode string

const (
	ModeAuto     Mode = "auto"
	ModeHinted   Mode = "hinted"
	ModeUnhinted Mode = "unhinted"
)

func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeAuto:
		return ModeAuto, nil
	case ModeHinted, "true":
		return ModeHinted, nil
	case ModeUnhinted, "false":
		return ModeUnhinted, nil
	}
	return "", fmt.Errorf("unknown hint mode %q (want auto, hinted or unhinted)", s)
}

// Resolve turns a mode into a concrete choice plus the sentence explaining it.
func Resolve(mode Mode, detect func() Info) (hinted bool, note string) {
	switch mode {
	case ModeHinted:
		return true, "hinted（手动指定）"
	case ModeUnhinted:
		return false, "unhinted（手动指定）"
	}
	r := Recommend(detect())
	choice := "unhinted"
	if r.Hinted {
		choice = "hinted"
	}
	return r.Hinted, fmt.Sprintf("%s（自动判定：%s）", choice, r.Reason)
}
