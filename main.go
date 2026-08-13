package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/chunxiaoyi/yahei-sarasa-semibold-go/internal/config"
	"github.com/chunxiaoyi/yahei-sarasa-semibold-go/internal/fetch"
	"github.com/chunxiaoyi/yahei-sarasa-semibold-go/internal/otfont"
	"github.com/chunxiaoyi/yahei-sarasa-semibold-go/internal/remotezip"
)

type options struct {
	hinted    bool
	tempDir   string
	resultDir string
	keepTemp  bool
	sourceDir string
}

func main() {
	var opt options
	flag.BoolVar(&opt.hinted, "hinted", true, "use hinted Sarasa UI TTF package")
	flag.StringVar(&opt.tempDir, "temp", "./temp", "temporary directory")
	flag.StringVar(&opt.resultDir, "result", "./result", "result directory")
	flag.BoolVar(&opt.keepTemp, "keep-temp", false, "keep extracted Sarasa source TTF files")
	flag.StringVar(&opt.sourceDir, "source-dir", "", "skip network access and read the three required SarasaUiSC TTF files from this directory")
	flag.Parse()

	if err := run(context.Background(), opt); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opt options) error {
	if err := recreateDir(opt.resultDir); err != nil {
		return err
	}
	if opt.sourceDir == "" {
		if err := recreateDir(opt.tempDir); err != nil {
			return err
		}
		if !opt.keepTemp {
			defer os.RemoveAll(opt.tempDir)
		}
		if err := acquireSources(ctx, opt); err != nil {
			return err
		}
		opt.sourceDir = opt.tempDir
	}
	fmt.Println("========> Generating Windows 11 compatible font identities")
	if err := generateAll(opt.sourceDir, opt.resultDir); err != nil {
		return err
	}
	fmt.Println("========> Done:", opt.resultDir)
	return nil
}

func acquireSources(ctx context.Context, opt options) error {
	r, err := fetch.Latest(ctx)
	if err != nil {
		return err
	}
	assetName := fetch.UIZipAssetName(r.Version(), opt.hinted)
	asset, err := r.FindAsset(assetName)
	if err != nil {
		return err
	}
	fmt.Printf("========> Latest Sarasa: %s\n", r.TagName)
	fmt.Printf("========> Remote package: %s (%.1f MiB logical ZIP)\n", asset.Name, float64(asset.Size)/(1<<20))
	fmt.Println("========> HTTP Range mode: only the three required SarasaUiSC entries will be fetched")
	fetched, err := remotezip.ExtractSelected(ctx, http.DefaultClient, asset.BrowserDownloadURL, asset.Size, opt.tempDir, config.RequiredSources)
	if err != nil {
		return err
	}
	fmt.Printf("========> Network bytes fetched for ZIP ranges: %.1f MiB\n", float64(fetched)/(1<<20))
	return nil
}

func generateAll(sourceDir, resultDir string) error {
	regularSource, err := loadFont(filepath.Join(sourceDir, config.RegularSource+".ttf"))
	if err != nil {
		return err
	}
	boldSource, err := loadFont(filepath.Join(sourceDir, config.BoldSource+".ttf"))
	if err != nil {
		return err
	}
	lightSource, err := loadFont(filepath.Join(sourceDir, config.LightSource+".ttf"))
	if err != nil {
		return err
	}

	regVersion, err := otfont.VersionFromFont(regularSource)
	if err != nil {
		return fmt.Errorf("regular source version: %w", err)
	}
	boldVersion, err := otfont.VersionFromFont(boldSource)
	if err != nil {
		return fmt.Errorf("bold source version: %w", err)
	}
	lightVersion, err := otfont.VersionFromFont(lightSource)
	if err != nil {
		return fmt.Errorf("light source version: %w", err)
	}

	// D4 keeps D2 intact and adds one Segoe UI Regular 400 output. Like D3,
	// its glyph source is SarasaUiSC-SemiBold; unlike D3, its cmap is capped
	// by the Windows 11 original Segoe UI coverage snapshot so CJK does not
	// get claimed by Segoe and continues to fall back to Microsoft YaHei UI.
	segoe, err := otfont.ApplySpec(regularSource, config.SegoeUIRegular(regVersion))
	if err != nil {
		return err
	}
	kept, dropped, err := segoe.FilterCmapToWhitelist(config.SegoeUIRegularAllows)
	if err != nil {
		return fmt.Errorf("segoeui.ttf cmap filter: %w", err)
	}
	if err := otfont.VerifySpec(segoe, config.SegoeUIRegular(regVersion)); err != nil {
		return fmt.Errorf("segoeui.ttf: %w", err)
	}
	verifiedCmap, err := otfont.VerifyCmapWhitelist(segoe, config.SegoeUIRegularAllows)
	if err != nil {
		return fmt.Errorf("segoeui.ttf: %w", err)
	}
	if verifiedCmap != kept {
		return fmt.Errorf("segoeui.ttf: cmap count changed after build: kept=%d verify=%d", kept, verifiedCmap)
	}
	segoeBytes, err := segoe.Bytes()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(resultDir, "segoeui.ttf"), segoeBytes, 0o644); err != nil {
		return err
	}
	fmt.Printf("========> segoeui.ttf generated (D4: Sarasa SemiBold glyphs, Win11 Segoe cmap whitelist; kept %d, dropped %d source mappings)\n", kept, dropped)

	if err := makeTTC(filepath.Join(resultDir, "msyh.ttc"), regularSource,
		config.Regular(regVersion), config.RegularUI(regVersion)); err != nil {
		return err
	}
	fmt.Println("========> msyh.ttc generated")

	// D2 keeps the two original C1 Bold faces at indices 0/1 and appends two
	// real 600-weight SemiBold faces at indices 2/3. The new faces use the same
	// SarasaUiSC-SemiBold source that C1 exposes as Regular, but retain weight
	// 600 so DirectWrite has an exact match for WinUI SemiBold requests.
	if err := makeTTCFaces(filepath.Join(resultDir, "msyhbd.ttc"),
		faceBuild{boldSource, config.Bold(boldVersion)},
		faceBuild{boldSource, config.BoldUI(boldVersion)},
		faceBuild{regularSource, config.SemiBold(regVersion)},
		faceBuild{regularSource, config.SemiBoldUI(regVersion)},
	); err != nil {
		return err
	}
	fmt.Println("========> msyhbd.ttc generated (Bold 700 + experimental SemiBold 600 faces)")

	if err := makeTTC(filepath.Join(resultDir, "msyhl.ttc"), lightSource,
		config.Light(lightVersion), config.LightUI(lightVersion)); err != nil {
		return err
	}
	fmt.Println("========> msyhl.ttc generated")

	if err := makeTTC(filepath.Join(resultDir, "simsun.ttc"), regularSource,
		config.SimSun(regVersion), config.NSimSun(regVersion)); err != nil {
		return err
	}
	fmt.Println("========> simsun.ttc generated")

	ext, err := otfont.ApplySpec(regularSource, config.SimSunExtB(regVersion))
	if err != nil {
		return err
	}
	if err := otfont.VerifySpec(ext, config.SimSunExtB(regVersion)); err != nil {
		return fmt.Errorf("simsunb.ttf: %w", err)
	}
	b, err := ext.Bytes()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(resultDir, "simsunb.ttf"), b, 0o644); err != nil {
		return err
	}
	fmt.Println("========> simsunb.ttf generated")

	return verifyResultDirectory(resultDir)
}

type faceBuild struct {
	src  *otfont.Font
	spec config.FontSpec
}

func makeTTC(path string, src *otfont.Font, aSpec, bSpec config.FontSpec) error {
	return makeTTCFaces(path, faceBuild{src, aSpec}, faceBuild{src, bSpec})
}

func makeTTCFaces(path string, inputs ...faceBuild) error {
	fonts := make([]*otfont.Font, 0, len(inputs))
	for i, in := range inputs {
		f, err := otfont.ApplySpec(in.src, in.spec)
		if err != nil {
			return fmt.Errorf("%s face %d apply: %w", filepath.Base(path), i, err)
		}
		if err := otfont.VerifySpec(f, in.spec); err != nil {
			return fmt.Errorf("%s face %d: %w", filepath.Base(path), i, err)
		}
		fonts = append(fonts, f)
	}
	data, err := otfont.TTC(fonts...)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadFont(path string) (*otfont.Font, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := otfont.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

func recreateDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

func verifyResultDirectory(dir string) error {
	want := map[string]bool{
		"msyh.ttc": true, "msyhbd.ttc": true, "msyhl.ttc": true,
		"simsun.ttc": true, "simsunb.ttf": true,
		"segoeui.ttf": true,
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != len(want) {
		return fmt.Errorf("result: expected exactly 6 files, got %d", len(entries))
	}
	for _, e := range entries {
		if e.IsDir() || !want[e.Name()] {
			return fmt.Errorf("result: unexpected entry %q", e.Name())
		}
	}
	return nil
}
