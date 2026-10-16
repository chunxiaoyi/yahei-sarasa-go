package custom

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/config"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
)

// Style mirrors the two validated Sarasa presets.
type Style string

const (
	// StyleRegular: true Regular becomes YaHei/Segoe Regular 400 (like the
	// "regular" preset).
	StyleRegular Style = "regular"
	// StyleSemiBold: the face nearest 600 becomes YaHei/Segoe Regular 400
	// (like the "semibold" preset, formerly main).
	StyleSemiBold Style = "semibold"
)

func ParseStyle(s string) (Style, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "regular", "standard":
		return StyleRegular, nil
	case "semibold", "main":
		return StyleSemiBold, nil
	}
	return "", fmt.Errorf("unknown style %q (want regular or semibold)", s)
}

// Assignment maps each slot to a face ID.
type Assignment map[recipe.Slot]string

// Request is a custom build plan as the UI/CLI sends it.
type Request struct {
	Style   Style      `json:"style"`
	Assign  Assignment `json:"assign"`  // empty slots are auto-filled
	Outputs []string   `json:"outputs"` // nil = all six
}

// AutoAssign picks, for every slot, the usable face whose weight is closest
// to what that slot needs, following the preset rules:
//
//	regular style:  400 <- nearest 400, Segoe <- same, SimSun <- the 600 pick
//	semibold style: 400 <- nearest 600, Segoe <- same, SimSun <- the 600 pick
//	both:           300 <- nearest 300, 600 <- nearest 600, 700 <- nearest 700
func AutoAssign(faces []*Face, style Style) Assignment {
	a := Assignment{}
	pick := func(target uint16, heavier bool) string {
		best := ""
		bestScore := math.Inf(1)
		for _, f := range faces {
			if !f.Usable {
				continue
			}
			d := float64(int(f.Weight) - int(target))
			score := math.Abs(d)
			// Tie-break toward the direction the slot leans.
			if (heavier && d > 0) || (!heavier && d < 0) {
				score -= 0.5
			}
			if f.Facts.Italic() {
				score += 1000
			}
			if w := f.Facts.WidthClass; w != 0 && w != 5 {
				score += 30 * math.Abs(float64(int(w)-5))
			}
			if f.coverage("gb2312-1").Ratio < 0.5 {
				score += 2000 // a Latin-only face should never win a CJK slot
			}
			score += 120 * float64(f.LangRank())
			if f.Facts.FixedPitch {
				score += 400 // monospaced faces make poor UI fonts
			}
			if score < bestScore || (score == bestScore && f.ID < best) {
				best, bestScore = f.ID, score
			}
		}
		return best
	}
	set := func(s recipe.Slot, id string) {
		if id != "" {
			a[s] = id
		}
	}
	// 600 leans lighter on a tie (e.g. Medium 500 vs Bold 700): it keeps
	// SemiBold visibly distinct from Bold, the reason D2 added a 600 face.
	set(recipe.SlotLight, pick(300, false))
	set(recipe.SlotSemiBold, pick(600, false))
	set(recipe.SlotBold, pick(700, true))
	if style == StyleSemiBold {
		set(recipe.SlotRegular, pick(600, false))
	} else {
		set(recipe.SlotRegular, pick(400, false))
	}
	set(recipe.SlotSegoe, a[recipe.SlotRegular])
	set(recipe.SlotSimSun, a[recipe.SlotSemiBold])
	return a
}

// SlotReport explains one slot of a plan.
type SlotReport struct {
	Slot         recipe.Slot `json:"slot"`
	FaceID       string      `json:"faceId"`
	FaceLabel    string      `json:"faceLabel"`
	SourceWeight uint16      `json:"sourceWeight"`
	// IdealWeight is the source weight this slot wants (600 for the 400
	// identity in semibold style, mirroring the preset).
	IdealWeight uint16 `json:"idealWeight"`
	// IdentityWeight is the OS/2 weight Windows will see.
	IdentityWeight uint16  `json:"identityWeight"`
	Auto           bool    `json:"auto"`
	Issues         []Issue `json:"issues"`
}

// OutputReport explains one output file of a plan.
type OutputReport struct {
	Name     string        `json:"name"`
	Selected bool          `json:"selected"`
	Slots    []recipe.Slot `json:"slots"`
	Blocked  bool          `json:"blocked"`
	Issues   []Issue       `json:"issues"`
}

// Evaluation is the full pre-build report.
type Evaluation struct {
	Style    Style            `json:"style"`
	Assign   Assignment       `json:"assign"`
	Slots    []SlotReport     `json:"slots"`
	Outputs  []OutputReport   `json:"outputs"`
	Issues   []Issue          `json:"issues"`
	Faces    []FaceIssueGroup `json:"faces"`
	CanBuild bool             `json:"canBuild"`
}

// Evaluate resolves req against faces and reports every problem.
func Evaluate(faces []*Face, req Request) Evaluation {
	byID := map[string]*Face{}
	for _, f := range faces {
		byID[f.ID] = f
	}
	style := req.Style
	if style == "" {
		style = StyleRegular
	}
	auto := AutoAssign(faces, style)
	ev := Evaluation{Style: style, Assign: Assignment{}}
	for _, s := range recipe.AllSlots {
		id, manual := req.Assign[s]
		if !manual || byID[id] == nil {
			id = auto[s]
			manual = false
		}
		rep := SlotReport{Slot: s, IdealWeight: s.TargetWeight(), IdentityWeight: s.TargetWeight(), Auto: !manual}
		if style == StyleSemiBold && (s == recipe.SlotRegular || s == recipe.SlotSegoe) {
			// Mirrors the semibold preset: its 400 identity is meant to carry
			// a 600 source, so do not flag that as a weight gap.
			rep.IdealWeight = 600
		}
		if s == recipe.SlotSimSun {
			rep.IdealWeight = 600 // both presets build SimSun from SemiBold
		}
		face := byID[id]
		if face == nil {
			rep.Issues = append(rep.Issues, issue(SevError, "no-face", "没有可用的字体"))
			ev.Slots = append(ev.Slots, rep)
			continue
		}
		ev.Assign[s] = id
		rep.FaceID, rep.FaceLabel, rep.SourceWeight = face.ID, face.Label, face.Weight
		rep.Issues = slotIssues(s, face, rep.IdealWeight)
		ev.Slots = append(ev.Slots, rep)
	}

	// Cross-slot checks.
	w := func(s recipe.Slot) int {
		if f := byID[ev.Assign[s]]; f != nil {
			return int(f.Weight)
		}
		return -1
	}
	if w(recipe.SlotLight) > w(recipe.SlotRegular) && w(recipe.SlotRegular) > 0 && style == StyleRegular {
		ev.Issues = append(ev.Issues, issue(SevWarn, "weight-order", "Light 用的字体比 Regular 还粗，字重顺序颠倒"))
	}
	if w(recipe.SlotSemiBold) > w(recipe.SlotBold) && w(recipe.SlotBold) > 0 {
		ev.Issues = append(ev.Issues, issue(SevWarn, "weight-order", "SemiBold 用的字体比 Bold 还粗，字重顺序颠倒"))
	}
	if ev.Assign[recipe.SlotSemiBold] != "" && ev.Assign[recipe.SlotSemiBold] == ev.Assign[recipe.SlotBold] {
		ev.Issues = append(ev.Issues, issue(SevInfo, "same-600-700", "SemiBold 600 与 Bold 700 使用同一字体，两种粗细会显示得一样"))
	}
	if style == StyleRegular && ev.Assign[recipe.SlotRegular] != "" && ev.Assign[recipe.SlotRegular] == ev.Assign[recipe.SlotSemiBold] {
		ev.Issues = append(ev.Issues, issue(SevInfo, "same-400-600", "Regular 400 与 SemiBold 600 使用同一字体，WinUI 的 600 标题不会比正文更粗"))
	}

	selected := map[string]bool{}
	if req.Outputs == nil {
		for _, n := range recipe.AllOutputs {
			selected[n] = true
		}
	} else {
		for _, n := range req.Outputs {
			selected[n] = true
		}
	}
	slotBlocked := map[recipe.Slot]bool{}
	for _, r := range ev.Slots {
		for _, i := range r.Issues {
			if i.Severity == SevError {
				slotBlocked[r.Slot] = true
			}
		}
	}
	anySelected := false
	ev.CanBuild = true
	for _, name := range recipe.AllOutputs {
		o := OutputReport{Name: name, Selected: selected[name], Slots: recipe.SlotsFor(name)}
		outlines := map[string]bool{}
		for _, s := range o.Slots {
			if slotBlocked[s] {
				o.Blocked = true
			}
			if f := byID[ev.Assign[s]]; f != nil {
				outlines[f.Facts.Outline] = true
			}
		}
		if len(outlines) > 1 {
			o.Issues = append(o.Issues, issue(SevWarn, "mixed-outline", "同一个 TTC 里混用了 TrueType 与 CFF 轮廓，部分旧程序可能处理不好"))
		}
		if o.Selected {
			anySelected = true
			if o.Blocked {
				ev.CanBuild = false
			}
		}
		ev.Outputs = append(ev.Outputs, o)
	}
	if !anySelected {
		ev.CanBuild = false
		ev.Issues = append(ev.Issues, issue(SevError, "no-output", "至少选择一个要生成的文件"))
	}
	ev.Faces = FaceIssues(faces, ev)
	if selected[recipe.OutSimSun] || selected[recipe.OutSimSunB] {
		ev.Issues = append(ev.Issues, issue(SevInfo, "simsun", "替换宋体会让所有指定「宋体/新宋体」的文档和网页也变成这款字体；不需要可以取消勾选 simsun.ttc 与 simsunb.ttf"))
	}
	return ev
}

func slotIssues(s recipe.Slot, f *Face, target uint16) []Issue {
	var out []Issue
	for _, i := range f.Issues {
		if i.Severity == SevError {
			out = append(out, i)
		}
	}
	if !f.Usable {
		return out
	}
	ascii := f.coverage("ascii")
	if s == recipe.SlotSegoe {
		if ascii.Ratio < 1 {
			out = append(out, issue(SevError, "segoe-ascii", "缺少 ASCII 字符（%s），不能替换 Segoe UI", ascii.Missing))
		} else {
			out = append(out, issue(SevInfo, "segoe-coverage", "Segoe UI 将保留 %d / %d 个原版码位，其余字符照常回退到其他字体", f.SegoeKept, config.SegoeUIRegularOriginalCoverageCount))
		}
	}
	if s.NeedsCJK() {
		l1, l2, big5, punct := f.coverage("gb2312-1"), f.coverage("gb2312-2"), f.coverage("big5-1"), f.coverage("punct")
		switch {
		case l1.Ratio < 0.5:
			out = append(out, issue(SevError, "no-cjk", "这不是中文字体：GB2312 一级汉字只覆盖 %d / %d。用它冒充微软雅黑会让中文界面大面积缺字", l1.Have, l1.Total))
		case l1.Ratio < 0.999:
			out = append(out, issue(SevWarn, "gb2312", "GB2312 一级汉字缺 %d 个（如「%s」），缺的字会回退到其他字体显示", l1.Total-l1.Have, l1.Missing))
		}
		if l1.Ratio >= 0.5 && l2.Ratio < 0.95 {
			out = append(out, issue(SevWarn, "gb2312-2", "GB2312 二级汉字只覆盖 %.0f%%（如缺「%s」）", 100*l2.Ratio, l2.Missing))
		}
		if l1.Ratio >= 0.5 && punct.Ratio < 0.95 {
			out = append(out, issue(SevWarn, "punct", "中文标点不全（缺「%s」）", punct.Missing))
		}
		if l1.Ratio >= 0.5 && big5.Ratio < 0.9 {
			out = append(out, issue(SevInfo, "big5", "繁体常用字（Big5 一级）覆盖 %.0f%%，繁体内容会部分回退", 100*big5.Ratio))
		}
		if ascii.Ratio < 1 {
			out = append(out, issue(SevWarn, "ascii", "缺少部分 ASCII 字符（%s）", ascii.Missing))
		}
	}
	gap := int(f.Weight) - int(target)
	switch {
	case s == recipe.SlotBold && f.Weight < 600:
		out = append(out, issue(SevWarn, "weight-gap", "源字重 %d：粗体文字会显示得不够粗（目标 700）", f.Weight))
	case gap >= 150 || gap <= -150:
		out = append(out, issue(SevWarn, "weight-gap", "源字重 %d 与目标 %d 差距较大", f.Weight, target))
	}
	return out
}

// FaceIssues returns the face-level findings (variable, CFF, metrics...)
// of every face the evaluation uses, once per face, in slot order.
func FaceIssues(faces []*Face, ev Evaluation) []FaceIssueGroup {
	byID := map[string]*Face{}
	for _, f := range faces {
		byID[f.ID] = f
	}
	seen := map[string]bool{}
	var out []FaceIssueGroup
	for _, s := range ev.Slots {
		f := byID[s.FaceID]
		if f == nil || seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		var issues []Issue
		for _, i := range f.Issues {
			if i.Severity != SevError {
				issues = append(issues, i)
			}
		}
		if len(issues) > 0 || len(f.Fixes) > 0 {
			out = append(out, FaceIssueGroup{FaceID: f.ID, Label: f.Label, Issues: issues, Fixes: f.Fixes})
		}
	}
	return out
}

// FaceIssueGroup is one face's findings.
type FaceIssueGroup struct {
	FaceID string       `json:"faceId"`
	Label  string       `json:"label"`
	Issues []Issue      `json:"issues"`
	Fixes  []otfont.Fix `json:"fixes"`
}

// ToRecipe converts an evaluated plan into a recipe plan. It refuses to
// build when Evaluate reported a blocking problem.
func ToRecipe(faces []*Face, ev Evaluation) (recipe.Plan, error) {
	if !ev.CanBuild {
		var msgs []string
		for _, s := range ev.Slots {
			for _, i := range s.Issues {
				if i.Severity == SevError {
					msgs = append(msgs, fmt.Sprintf("%s：%s", SlotTitle(s.Slot), i.Msg))
				}
			}
		}
		for _, i := range ev.Issues {
			if i.Severity == SevError {
				msgs = append(msgs, i.Msg)
			}
		}
		return recipe.Plan{}, fmt.Errorf("无法生成：%s", strings.Join(msgs, "；"))
	}
	byID := map[string]*Face{}
	for _, f := range faces {
		byID[f.ID] = f
	}
	sources := map[string]*recipe.Source{}
	plan := recipe.Plan{Slots: map[recipe.Slot]*recipe.Source{}}
	need := map[recipe.Slot]bool{}
	for _, o := range ev.Outputs {
		if o.Selected {
			plan.Outputs = append(plan.Outputs, o.Name)
			for _, s := range o.Slots {
				need[s] = true
			}
		}
	}
	for s := range need {
		id := ev.Assign[s]
		f := byID[id]
		if f == nil || f.font == nil {
			return recipe.Plan{}, fmt.Errorf("%s 没有可用字体", SlotTitle(s))
		}
		src := sources[id]
		if src == nil {
			src = f.Source()
			sources[id] = src
		}
		plan.Slots[s] = src
	}
	return plan, nil
}

// Source returns the recipe source for this face (custom identity rules:
// keep the source font's own copyright/license, fix macStyle).
func (f *Face) Source() *recipe.Source {
	copyright := strings.TrimSpace(f.Facts.Copyright)
	if copyright == "" {
		name := f.Facts.FullName
		if name == "" {
			name = f.FileName
		}
		copyright = "Made from " + name + " by yahei-sarasa-go"
	}
	var extra []config.NameRecord
	if v := strings.TrimSpace(f.Facts.License); v != "" {
		extra = append(extra, config.NameRecord{PlatformID: 3, EncodingID: 1, LanguageID: 0x0409, NameID: 13, Value: v})
	}
	if v := strings.TrimSpace(f.Facts.LicenseURL); v != "" {
		extra = append(extra, config.NameRecord{PlatformID: 3, EncodingID: 1, LanguageID: 0x0409, NameID: 14, Value: v})
	}
	return &recipe.Source{
		Label:       f.Label,
		SHA256:      f.fileSum,
		Font:        f.font,
		Version:     f.version,
		Copyright:   copyright,
		ExtraNames:  extra,
		FixMacStyle: true,
	}
}

// SlotTitle is the Chinese display name of a slot.
func SlotTitle(s recipe.Slot) string {
	switch s {
	case recipe.SlotLight:
		return "微软雅黑 Light 300"
	case recipe.SlotRegular:
		return "微软雅黑 Regular 400"
	case recipe.SlotSemiBold:
		return "微软雅黑 SemiBold 600"
	case recipe.SlotBold:
		return "微软雅黑 Bold 700"
	case recipe.SlotSimSun:
		return "宋体 / 新宋体"
	case recipe.SlotSegoe:
		return "Segoe UI Regular"
	}
	return string(s)
}

// SortFaces orders faces by weight then label (stable UI lists).
func SortFaces(faces []*Face) {
	sort.SliceStable(faces, func(i, j int) bool {
		if faces[i].Weight != faces[j].Weight {
			return faces[i].Weight < faces[j].Weight
		}
		return faces[i].Label < faces[j].Label
	})
}
