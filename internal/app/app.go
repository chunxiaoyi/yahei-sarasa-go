// Package app runs complete jobs (acquire sources, build, verify, package)
// for both front ends: the console CLI and the GUI.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/custom"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/fetch"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/pack"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/progress"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/sarasa"
)

// Version is the tool version (set with -ldflags "-X .../internal/app.Version=v2.0.0").
var Version = "v2.0.0-dev"

// Result is what a finished job produced.
type Result struct {
	Kind       string              `json:"kind"`
	Title      string              `json:"title"`
	PackageDir string              `json:"packageDir,omitempty"`
	FontsDir   string              `json:"fontsDir"`
	ZipPath    string              `json:"zipPath,omitempty"`
	Outputs    []recipe.OutputInfo `json:"outputs"`
	Warnings   []string            `json:"warnings,omitempty"`
	Seconds    float64             `json:"seconds"`
}

// PresetOptions configures RunPreset.
type PresetOptions struct {
	Preset   string
	Hinted   bool
	HintNote string // why hinted/unhinted was chosen (logged and kept in the manifest)
	Version  string // Sarasa version, "" = latest
	Local    string // Sarasa folder or ZIP; disables network
	CacheDir string // "" = no cache
	TempDir  string
	Proxy    string
	Mirror   string
	KeepTemp bool

	// Exactly one of these: ResultDir reproduces the original CLI (a folder
	// with exactly the six fonts); OutRoot creates a package folder in it.
	ResultDir string
	OutRoot   string
	Zip       bool
}

// RunPreset builds one of the two validated Sarasa variants.
func RunPreset(ctx context.Context, opt PresetOptions, sink progress.Sink) (*Result, error) {
	start := time.Now()
	p, err := recipe.PresetByID(opt.Preset)
	if err != nil {
		return nil, err
	}
	client, proxyDesc, err := fetch.NewClient(opt.Proxy)
	if err != nil {
		return nil, err
	}
	if opt.Local == "" && proxyDesc != "" {
		sink.Infof("网络：%s", proxyDesc)
	}
	if opt.Local == "" && opt.HintNote != "" {
		sink.Infof("Sarasa 包：%s", opt.HintNote)
	}
	files := p.Files()
	src, err := sarasa.Acquire(ctx, sarasa.Options{
		Local: opt.Local, Version: opt.Version, Hinted: opt.Hinted,
		CacheDir: opt.CacheDir, TempDir: opt.TempDir, Client: client, Mirror: opt.Mirror, Files: files,
	}, sink)
	if err != nil {
		return nil, err
	}
	if src.TempDir && !opt.KeepTemp {
		defer os.RemoveAll(src.Dir)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	slots, err := p.LoadSources(src.Dir)
	if err != nil {
		return nil, err
	}
	hint := "hinted"
	if !opt.Hinted {
		hint = "unhinted"
	}
	if opt.Local != "" {
		hint = "local"
	}
	title := fmt.Sprintf("微软雅黑 ← Sarasa UI SC %s %s（%s 预设，原 %s 分支）", p.Name, src.Version, p.ID, p.Branch)
	name := pack.SafeName(fmt.Sprintf("YaHei-Sarasa-%s-%s-%s", map[string]string{"semibold": "SemiBold", "regular": "Regular"}[p.ID], src.Version, hint))

	res := &Result{Kind: "preset", Title: title}
	pkgDir, fontsDir, err := outputDirs(opt.ResultDir, opt.OutRoot, name)
	if err != nil {
		return nil, err
	}
	res.PackageDir, res.FontsDir = pkgDir, fontsDir

	sink.Stage("build", "正在生成 Windows 11 身份的字体文件")
	plan := recipe.Plan{Slots: slots}
	outs, err := recipe.Build(plan, fontsDir, sink)
	if err != nil {
		return nil, err
	}
	if err := recipe.VerifyDir(fontsDir, recipe.AllOutputs); err != nil {
		return nil, err
	}
	res.Outputs = outs
	if pkgDir != "" {
		sink.Stage("pack", "正在打包")
		m := pack.Manifest{
			Kind: "preset", Preset: p.ID, Branch: p.Branch, Title: title,
			Sarasa: map[string]any{"version": src.Version, "package": src.Package, "origin": src.Origin, "hinted": opt.Hinted, "hintChoice": opt.HintNote, "files": src.Files},
			Slots:  slotLabels(slots), Sources: sourceInfos(plan), Outputs: outs,
		}
		zipPath, err := pack.Finish(pkgDir, m, opt.Zip)
		if err != nil {
			return nil, err
		}
		res.ZipPath = zipPath
	}
	res.Seconds = time.Since(start).Seconds()
	sink.OKf("完成：%s", firstNonEmpty(res.ZipPath, res.PackageDir, res.FontsDir))
	return res, nil
}

// CustomOptions configures RunCustom.
type CustomOptions struct {
	Faces     []*custom.Face
	Request   custom.Request
	ResultDir string
	OutRoot   string
	Zip       bool
	Name      string // optional package name

	// Display context for the "no hinting" finding (see custom.ApplyDisplayContext).
	DisplayDetected bool
	HintingMatters  bool
	DisplayBasis    string
}

// RunCustom builds from user fonts after re-evaluating the plan.
func RunCustom(ctx context.Context, opt CustomOptions, sink progress.Sink) (*Result, error) {
	start := time.Now()
	ev := custom.Evaluate(opt.Faces, opt.Request)
	custom.ApplyDisplayContext(&ev, opt.DisplayDetected, opt.HintingMatters, opt.DisplayBasis)
	plan, err := custom.ToRecipe(opt.Faces, ev)
	if err != nil {
		return nil, err
	}
	var warnings []string
	for _, s := range ev.Slots {
		if plan.Slots[s.Slot] == nil {
			continue
		}
		for _, i := range s.Issues {
			if i.Severity == custom.SevWarn {
				warnings = append(warnings, custom.SlotTitle(s.Slot)+"："+i.Msg)
			}
		}
	}
	for _, i := range ev.Issues {
		if i.Severity == custom.SevWarn {
			warnings = append(warnings, i.Msg)
		}
	}
	for _, g := range ev.Faces {
		for _, i := range g.Issues {
			if i.Severity == custom.SevWarn {
				warnings = append(warnings, g.Label+"："+i.Msg)
			}
		}
	}
	for _, w := range dedupe(warnings) {
		sink.Warnf("%s", w)
	}

	regular := plan.Slots[recipe.SlotRegular]
	if regular == nil {
		for _, s := range plan.DistinctSources() {
			regular = s
			break
		}
	}
	family := strings.TrimSuffix(strings.TrimSuffix(regular.Label, filepath.Ext(regular.Label)), " ")
	styleName := map[custom.Style]string{custom.StyleRegular: "标准风格", custom.StyleSemiBold: "SemiBold 风格"}[ev.Style]
	title := fmt.Sprintf("微软雅黑 ← 自定义字体 %s（%s）", family, styleName)
	name := opt.Name
	if name == "" {
		name = "YaHei-Custom-" + pack.SafeName(family) + "-" + string(ev.Style)
	}

	res := &Result{Kind: "custom", Title: title}
	pkgDir, fontsDir, err := outputDirs(opt.ResultDir, opt.OutRoot, pack.SafeName(name))
	if err != nil {
		return nil, err
	}
	res.PackageDir, res.FontsDir = pkgDir, fontsDir
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, s := range plan.DistinctSources() {
		var slots []string
		for _, k := range recipe.AllSlots {
			if plan.Slots[k] == s {
				slots = append(slots, custom.SlotTitle(k))
			}
		}
		sink.Infof("%s → %s", s.Label, strings.Join(slots, "、"))
	}
	for _, g := range ev.Faces {
		for _, fix := range g.Fixes {
			sink.Infof("%s：%s", g.Label, fix.Msg)
		}
	}

	sink.Stage("build", "正在生成 Windows 11 身份的字体文件")
	outs, err := recipe.Build(plan, fontsDir, sink)
	if err != nil {
		return nil, err
	}
	names, _ := plan.OutputNames()
	if err := recipe.VerifyDir(fontsDir, names); err != nil {
		return nil, err
	}
	res.Outputs = outs
	res.Warnings = dedupe(warnings)
	if pkgDir != "" {
		sink.Stage("pack", "正在打包")
		m := pack.Manifest{
			Kind: "custom", Style: string(ev.Style), Title: title,
			Slots: slotLabels(plan.Slots), Sources: sourceInfos(plan), Outputs: outs, Warnings: res.Warnings,
			BuildNote: "自定义字体：name ID 0/13/14 保留源字体的版权与授权信息，其余身份字段与两个 Sarasa 预设一致，取自 Windows 11 原版",
		}
		zipPath, err := pack.Finish(pkgDir, m, opt.Zip)
		if err != nil {
			return nil, err
		}
		res.ZipPath = zipPath
	}
	res.Seconds = time.Since(start).Seconds()
	sink.OKf("完成：%s", firstNonEmpty(res.ZipPath, res.PackageDir, res.FontsDir))
	runtime.GC()
	return res, nil
}

func outputDirs(resultDir, outRoot, name string) (pkgDir, fontsDir string, err error) {
	if resultDir != "" {
		if err := os.RemoveAll(resultDir); err != nil {
			return "", "", err
		}
		if err := os.MkdirAll(resultDir, 0o755); err != nil {
			return "", "", err
		}
		return "", resultDir, nil
	}
	if outRoot == "" {
		return "", "", fmt.Errorf("no output directory")
	}
	return pack.Prepare(outRoot, name)
}

func slotLabels(m map[recipe.Slot]*recipe.Source) map[recipe.Slot]string {
	out := map[recipe.Slot]string{}
	for k, v := range m {
		if v != nil {
			out[k] = v.Label
		}
	}
	return out
}

func sourceInfos(p recipe.Plan) []pack.SourceInfo {
	var out []pack.SourceInfo
	for _, s := range p.DistinctSources() {
		out = append(out, pack.SourceInfo{Label: s.Label, SHA256: s.SHA256, Version: s.Version})
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
