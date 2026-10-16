// yahei-sarasa-go builds replacement Windows 11 system fonts that carry the
// exact Microsoft YaHei / SimSun / Segoe UI identities while drawing Sarasa
// Gothic (or user-supplied) glyphs.
//
// Run without arguments for the GUI. Command line:
//
//	yahei-sarasa-go [build] [-preset semibold|regular] [-source-dir DIR] [-result DIR] ...
//	yahei-sarasa-go custom -font A.ttf -font B.ttf [-style regular|semibold] ...
//	yahei-sarasa-go inspect FONT... [-json]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/app"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/custom"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/display"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/fetch"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/gui"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/platform"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/progress"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/sysinstall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fetch.SystemProxy = platform.SystemProxy
	args := os.Args[1:]
	if len(args) > 0 {
		platform.AttachParentConsole()
	}
	var err error
	switch {
	case len(args) == 0:
		err = gui.Run(ctx, gui.Options{})
	case args[0] == "gui" || args[0] == "-gui" || args[0] == "--gui":
		err = runGUI(ctx, args[1:])
	case args[0] == "build":
		err = runBuild(ctx, args[1:])
	case args[0] == "custom":
		err = runCustom(ctx, args[1:])
	case args[0] == "inspect":
		err = runInspect(args[1:])
	case args[0] == "display":
		err = runDisplay(args[1:])
	case args[0] == "system":
		err = runSystem(args[1:])
	case args[0] == "version" || args[0] == "-version" || args[0] == "--version":
		fmt.Println("yahei-sarasa-go", app.Version)
	case args[0] == "help" || args[0] == "-h" || args[0] == "--help" || args[0] == "-help":
		usage()
	case strings.HasPrefix(args[0], "-"):
		// Original CLI: flags only, e.g. `go run . -source-dir D:\Sarasa`.
		err = runBuild(ctx, args)
	default:
		usage()
		err = fmt.Errorf("unknown command %q", args[0])
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "已取消")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`yahei-sarasa-go ` + app.Version + `

  (无参数)                     打开图形界面
  build   [选项]               生成 Sarasa 预设（不写 build 直接给选项也可以，兼容旧用法）
  custom  -font 文件 [选项]    用自己的字体生成
  inspect 文件... [-json]      体检字体文件（也可用来提取 Windows 原版字体信息）
  display [-json]              检测这台电脑的屏幕缩放，给出 hinted / unhinted 建议
  system status                查看系统字体、原版备份和待重启操作
  system install 字体包文件夹   安装到系统（重启后生效，同时清空字体缓存；需管理员）
         [-import 备份文件夹]   系统字体已被换过、又没有原版备份时，从这里导入原版
  system restore               还原原版（重启后生效；需管理员）
  system clear-cache           只清空字体缓存（重启后生效；需管理员）
  system cancel                取消还没重启生效的操作（需管理员）
  version

build 选项：
  -preset semibold|regular     semibold = 原 main 分支（默认），regular = 原 regular 分支
  -source-dir 目录或zip        使用本地 SarasaUiSC-*.ttf，不联网
  -version 1.0.42              固定 Sarasa 版本（默认最新）
  -hint auto|hinted|unhinted   auto = 按这台电脑的屏幕缩放自动判定（任一屏幕低于 150% 用 hinted）
  -hinted=true                 旧参数，等同 -hint hinted / unhinted；不给 -hint 时沿用它（默认 hinted）
  -result ./result             输出目录（只放 6 个字体，与旧版完全一致）
  -out 目录                    改为生成完整的包（fonts/ + manifest + 校验 + 说明）
  -zip                         与 -out 一起：额外打成 zip
  -cache 目录                  缓存下载的 Sarasa（"auto" = 用户缓存目录）
  -proxy URL|direct            代理，例如 http://127.0.0.1:7890
  -mirror URL                  下载地址前缀，例如 https://ghfast.top/
  -temp ./temp  -keep-temp

custom 选项：
  -font 文件                   可重复；支持 TTF/OTF/TTC/OTC/WOFF
  -style regular|semibold      regular = 正文用 400；semibold = 正文用最接近 600 的字重（与两个预设同理）
  -assign 槽位=文件[#序号]      手动指定，槽位：light regular semibold bold simsun segoe
  -outputs msyh.ttc,...         只生成其中几个文件（默认 6 个全部）
  -result 目录 | -out 目录 [-zip]
`)
}

func cacheRoot(v string) string {
	if v != "auto" {
		return v
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "yahei-sarasa-go")
	}
	return filepath.Join(os.TempDir(), "yahei-sarasa-go-cache")
}

func runGUI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("gui", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:0", "listen address (loopback only)")
	browser := fs.Bool("browser", false, "open in the default browser instead of an app window")
	noOpen := fs.Bool("no-open", false, "do not open any window; print the URL")
	out := fs.String("out", "", "output root")
	_ = fs.Parse(args)
	return gui.Run(ctx, gui.Options{Addr: *addr, ForceBrowser: *browser, NoOpen: *noOpen, OutRoot: *out})
}

func runBuild(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	opt := app.PresetOptions{}
	fs.StringVar(&opt.Preset, "preset", "semibold", "semibold (main) or regular")
	fs.BoolVar(&opt.Hinted, "hinted", true, "use hinted Sarasa UI TTF package (ignored when -hint is given)")
	hint := fs.String("hint", "", "auto, hinted or unhinted (auto decides from this computer's display scaling)")
	tempDir := fs.String("temp", "./temp", "temporary directory")
	fs.StringVar(&opt.ResultDir, "result", "./result", "result directory (exactly the six fonts)")
	fs.BoolVar(&opt.KeepTemp, "keep-temp", false, "keep extracted Sarasa source TTF files")
	fs.StringVar(&opt.Local, "source-dir", "", "skip network access and read the SarasaUiSC TTF files from this directory or ZIP")
	fs.StringVar(&opt.Version, "version", "", "Sarasa version (default latest)")
	cache := fs.String("cache", "", "cache directory for downloaded Sarasa sources (auto = user cache dir)")
	fs.StringVar(&opt.Proxy, "proxy", "", "proxy URL, or direct")
	fs.StringVar(&opt.Mirror, "mirror", "", "download URL prefix")
	out := fs.String("out", "", "write a full package (fonts/, manifest, checksums, notes) under this directory instead of -result")
	fs.BoolVar(&opt.Zip, "zip", false, "with -out: also write a zip")
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *hint != "" {
		mode, err := display.ParseMode(*hint)
		if err != nil {
			return err
		}
		opt.Hinted, opt.HintNote = display.Resolve(mode, display.Detect)
	}
	opt.CacheDir = cacheRoot(*cache)
	opt.TempDir = *tempDir
	if *out != "" {
		opt.OutRoot, opt.ResultDir = *out, ""
	}
	sink := progress.Console(os.Stdout)
	_, err := app.RunPreset(ctx, opt, sink)
	if opt.CacheDir == "" && !opt.KeepTemp {
		_ = os.Remove(opt.TempDir) // only succeeds when empty, as before
	}
	return err
}

// parseInterspersed lets file arguments and flags appear in any order
// (the standard flag package stops at the first non-flag argument).
func parseInterspersed(fs *flag.FlagSet, args []string) []string {
	var positional []string
	for {
		_ = fs.Parse(args)
		args = fs.Args()
		if len(args) == 0 {
			return positional
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func runCustom(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("custom", flag.ExitOnError)
	var fonts, assigns multiFlag
	fs.Var(&fonts, "font", "font file (repeatable)")
	fs.Var(&assigns, "assign", "slot=file[#index] (repeatable)")
	style := fs.String("style", "regular", "regular or semibold")
	outputs := fs.String("outputs", "", "comma-separated subset of output files")
	result := fs.String("result", "", "result directory (fonts only)")
	out := fs.String("out", "", "package root directory")
	zipIt := fs.Bool("zip", false, "with -out: also write a zip")
	name := fs.String("name", "", "package name")
	fonts = append(fonts, parseInterspersed(fs, args)...)
	if len(fonts) == 0 && len(assigns) == 0 {
		return errors.New("custom: give at least one -font")
	}
	if *result == "" && *out == "" {
		*result = "./result"
	}
	st, err := custom.ParseStyle(*style)
	if err != nil {
		return err
	}

	var faces []*custom.Face
	byPath := map[string]*custom.File{}
	load := func(path string) (*custom.File, error) {
		abs, _ := filepath.Abs(path)
		if f, ok := byPath[abs]; ok {
			return f, nil
		}
		f, err := custom.Inspect(abs)
		if err != nil {
			return nil, err
		}
		if f.Error != "" {
			return nil, fmt.Errorf("%s：%s", f.Name, f.Error)
		}
		byPath[abs] = f
		faces = append(faces, f.Faces...)
		return f, nil
	}
	for _, p := range fonts {
		if _, err := load(p); err != nil {
			return err
		}
	}
	req := custom.Request{Style: st, Assign: custom.Assignment{}}
	for _, a := range assigns {
		slotName, target, ok := strings.Cut(a, "=")
		slot := recipe.Slot(strings.TrimSpace(slotName))
		if !ok || !slot.Valid() {
			return fmt.Errorf("bad -assign %q (want slot=file[#index])", a)
		}
		path, idx := target, 0
		if i := strings.LastIndex(target, "#"); i > 0 {
			n, err := strconv.Atoi(target[i+1:])
			if err != nil {
				return fmt.Errorf("bad face index in %q", a)
			}
			path, idx = target[:i], n
		}
		f, err := load(path)
		if err != nil {
			return err
		}
		if idx < 0 || idx >= len(f.Faces) {
			return fmt.Errorf("%s has %d faces, index %d out of range", f.Name, len(f.Faces), idx)
		}
		req.Assign[slot] = f.Faces[idx].ID
	}
	if *outputs != "" {
		for _, o := range strings.Split(*outputs, ",") {
			req.Outputs = append(req.Outputs, strings.TrimSpace(o))
		}
	}

	ev := custom.Evaluate(faces, req)
	if info := display.Detect(); info.Supported && len(info.Monitors) > 0 {
		rec := display.Recommend(info)
		custom.ApplyDisplayContext(&ev, true, rec.Hinted, rec.Basis)
	}
	printEvaluation(ev)
	if !ev.CanBuild {
		_, err := custom.ToRecipe(faces, ev)
		return err
	}
	sink := progress.Console(os.Stdout)
	copt := app.CustomOptions{Faces: faces, Request: req, ResultDir: *result, OutRoot: *out, Zip: *zipIt, Name: *name}
	info := display.Detect()
	rec := display.Recommend(info)
	copt.DisplayDetected, copt.HintingMatters, copt.DisplayBasis = info.Supported && len(info.Monitors) > 0, rec.Hinted, rec.Basis
	_, err = app.RunCustom(ctx, copt, sink)
	return err
}

func printEvaluation(ev custom.Evaluation) {
	fmt.Println("========> 字体分配：")
	for _, s := range ev.Slots {
		mark := "手动"
		if s.Auto {
			mark = "自动"
		}
		fmt.Printf("  %-22s ← %s（字重 %d，%s）\n", custom.SlotTitle(s.Slot), s.FaceLabel, s.SourceWeight, mark)
		for _, i := range s.Issues {
			fmt.Printf("      [%s] %s\n", sevLabel(i.Severity), i.Msg)
		}
	}
	for _, i := range ev.Issues {
		fmt.Printf("  [%s] %s\n", sevLabel(i.Severity), i.Msg)
	}
	for _, g := range ev.Faces {
		fmt.Printf("  %s：\n", g.Label)
		for _, i := range g.Issues {
			fmt.Printf("      [%s] %s\n", sevLabel(i.Severity), i.Msg)
		}
		for _, f := range g.Fixes {
			fmt.Printf("      [修正] %s\n", f.Msg)
		}
	}
	for _, o := range ev.Outputs {
		for _, i := range o.Issues {
			fmt.Printf("  %s [%s] %s\n", o.Name, sevLabel(i.Severity), i.Msg)
		}
	}
}

func sevLabel(s custom.Severity) string {
	switch s {
	case custom.SevError:
		return "错误"
	case custom.SevWarn:
		return "注意"
	}
	return "信息"
}

func runDisplay(args []string) error {
	fs := flag.NewFlagSet("display", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	_ = fs.Parse(args)
	info := display.Detect()
	rec := display.Recommend(info)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"info": info, "recommendation": rec})
	}
	fmt.Println(display.Describe(info))
	for i, m := range info.Monitors {
		fmt.Printf("  屏幕 %d  %-14s %5d×%-5d  DPI %3d  缩放 %d%%  主屏 %v\n", i+1, m.Name, m.Width, m.Height, m.DPI, m.Scale, m.Primary)
	}
	choice := "unhinted"
	if rec.Hinted {
		choice = "hinted"
	}
	fmt.Printf("建议：%s\n理由：%s\n", choice, rec.Reason)
	return nil
}

func runSystem(args []string) error {
	if len(args) == 0 {
		return errors.New("system: 需要子命令：status、install、restore、clear-cache、cancel")
	}
	sub := args[0]
	fs := flag.NewFlagSet("system "+sub, flag.ExitOnError)
	resultID := fs.String("result", "", "write the outcome to the protected results folder (used by the GUI)")
	importDir := fs.String("import", "", "folder holding genuine Windows originals")
	asJSON := fs.Bool("json", false, "machine-readable status")
	positional := parseInterspersed(fs, args[1:])

	sys := sysinstall.System()
	paths, err := sysinstall.DefaultPaths()
	if err != nil {
		return err
	}
	if sub == "status" {
		r := sysinstall.GetStatus(sys, paths)
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(r)
		}
		fmt.Println(r.Message)
		for _, f := range r.Files {
			mark := ""
			switch {
			case f.Expected != nil && f.MatchesInstalled:
				mark = "（与安装记录一致）"
			case f.Expected != nil:
				mark = "（与安装记录不一致）"
			case f.MatchesOriginal:
				mark = "（原版）"
			}
			fmt.Printf("  %-12s %-10s %s%s\n", f.Name, f.State, f.SHA256, mark)
		}
		if r.Originals != nil {
			fmt.Printf("原版备份：%s（%s）\n", paths.OriginalsDir(), r.Originals.Created)
		} else {
			fmt.Println("原版备份：还没有")
		}
		if r.PendingActive {
			fmt.Printf("待重启：%d 个操作\n", r.PendingCount)
		}
		return nil
	}

	var lines []string
	logf := func(format string, a ...any) {
		line := fmt.Sprintf(format, a...)
		lines = append(lines, line)
		if *resultID == "" {
			fmt.Println("========>", line)
		}
	}
	var opErr error
	switch sub {
	case "install":
		if len(positional) != 1 {
			opErr = errors.New("system install: 需要一个字体包文件夹")
			break
		}
		opErr = sysinstall.Install(sys, paths, positional[0], sysinstall.Options{ImportOriginals: *importDir}, logf)
	case "restore":
		opErr = sysinstall.Restore(sys, paths, logf)
	case "clear-cache":
		opErr = sysinstall.ClearCache(sys, paths, logf)
	case "cancel":
		opErr = sysinstall.Cancel(sys, paths, logf)
	default:
		opErr = fmt.Errorf("system: 未知子命令 %q", sub)
	}
	if *resultID != "" {
		r := sysinstall.Result{OK: opErr == nil, Log: lines}
		if opErr != nil {
			r.Error = opErr.Error()
		}
		if err := sysinstall.WriteResult(sys, paths, *resultID, r); err != nil {
			return fmt.Errorf("写入结果失败：%w", err)
		}
	}
	if errors.Is(opErr, sysinstall.ErrNotAdmin) {
		return errors.New("需要管理员权限：请在「以管理员身份运行」的终端里执行，或使用图形界面（会弹出授权提示）")
	}
	return opErr
}

func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	paths := parseInterspersed(fs, args)
	if len(paths) == 0 {
		return errors.New("inspect: give one or more font files")
	}
	var files []*custom.File
	for _, p := range paths {
		f, err := custom.Inspect(p)
		if err != nil {
			return err
		}
		files = append(files, f)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(files)
	}
	for _, f := range files {
		fmt.Printf("%s  (%s, %.1f MiB)\n  sha256 %s\n", f.Path, f.Container, float64(f.Size)/(1<<20), f.SHA256)
		if f.Error != "" {
			fmt.Printf("  [错误] %s\n", f.Error)
			continue
		}
		for _, face := range f.Faces {
			x := face.Facts
			fmt.Printf("  #%d %s / %s  weight %d  %s  glyphs %d  UPM %d  win %d/%d (%.2f em)\n",
				face.Index, x.Family, x.Subfamily, x.WeightClass, x.Outline, x.NumGlyphs, x.Metrics.UPM,
				x.Metrics.WinAscent, x.Metrics.WinDescent, x.Metrics.WinLineHeight())
			fmt.Printf("     version %q  fsSelection 0x%04X  fsType 0x%04X  OS/2 v%d  codepages 0x%08X\n",
				x.VersionString, x.FSSelection, x.FSType, x.OS2Version, x.CodePage1)
			var cov []string
			for _, c := range face.Coverage {
				cov = append(cov, fmt.Sprintf("%s %d/%d", c.Name, c.Have, c.Total))
			}
			sort.Strings(cov)
			fmt.Printf("     覆盖：%s；Segoe 白名单内 %d\n", strings.Join(cov, "，"), face.SegoeKept)
			for _, i := range face.Issues {
				fmt.Printf("     [%s] %s\n", sevLabel(i.Severity), i.Msg)
			}
			for _, fix := range face.Fixes {
				fmt.Printf("     [修正] %s\n", fix.Msg)
			}
		}
	}
	return nil
}
