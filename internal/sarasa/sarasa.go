// Package sarasa obtains the Sarasa UI SC source TTFs: from a local folder or
// ZIP the user points at, from the on-disk cache, or from the GitHub release
// via HTTP Range requests (only the needed ZIP entries are transferred).
package sarasa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/fetch"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/otfont"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/progress"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/remotezip"
)

// Options controls Acquire.
type Options struct {
	// Local is an optional directory or .zip that already holds the TTFs.
	// When set, no network access happens.
	Local string
	// Version pins a Sarasa release ("" = latest).
	Version string
	Hinted  bool
	// CacheDir holds downloaded sources; "" disables caching (files go to a
	// temporary directory that the caller removes).
	CacheDir string
	TempDir  string
	Client   *http.Client
	Mirror   string // optional download URL prefix
	Files    []string
}

// Result describes where the sources ended up.
type Result struct {
	Dir      string            `json:"dir"`
	Version  string            `json:"version"`
	Package  string            `json:"package"`
	Origin   string            `json:"origin"` // "local", "cache", "download"
	Files    map[string]string `json:"files"`  // name -> sha256
	Fetched  int64             `json:"fetchedBytes,omitempty"`
	TempDir  bool              `json:"-"`
	Resolved *fetch.Resolved   `json:"resolved,omitempty"`
}

type cacheMeta struct {
	Version string            `json:"version"`
	Package string            `json:"package"`
	Hinted  bool              `json:"hinted"`
	Files   map[string]string `json:"files"`
}

const metaName = "cache.json"

// Acquire returns a directory containing opt.Files (default: all four
// SarasaUiSC files).
func Acquire(ctx context.Context, opt Options, sink progress.Sink) (*Result, error) {
	files := opt.Files
	if len(files) == 0 {
		files = recipe.SarasaFiles
	}
	if opt.Local != "" {
		return acquireLocal(opt, files, sink)
	}

	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}
	pinned, err := fetch.NormalizeVersion(opt.Version)
	if err != nil {
		return nil, err
	}

	// A pinned version that is already cached needs no network at all.
	if pinned != "" && opt.CacheDir != "" {
		if r, ok := tryCache(cacheDir(opt.CacheDir, pinned, opt.Hinted), files, sink); ok {
			return r, nil
		}
	}

	sink.Stage("resolve", "正在查询 GitHub 上的 Sarasa 发布信息")
	res, err := fetch.Resolve(ctx, fetch.ResolveOptions{Version: pinned, Hinted: opt.Hinted, Client: client, SizeProbe: remotezip.ProbeSize})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		// Offline: fall back to the newest cached copy, if any.
		if pinned == "" && opt.CacheDir != "" {
			if dir, v := newestCache(opt.CacheDir, opt.Hinted); dir != "" {
				if r, ok := tryCache(dir, files, sink); ok {
					sink.Warnf("无法连接 GitHub（%v），改用已缓存的 Sarasa %s", err, v)
					return r, nil
				}
			}
		}
		return nil, fmt.Errorf("无法获取 Sarasa 发布信息：%w", err)
	}
	if res.Via == "web" {
		sink.Warnf("GitHub API 不可用，已通过网页地址定位 %s", res.Name)
	}
	sink.Infof("Sarasa 版本：%s", res.Tag)

	dir := ""
	if opt.CacheDir != "" {
		dir = cacheDir(opt.CacheDir, res.Version, opt.Hinted)
		if r, ok := tryCache(dir, files, sink); ok {
			r.Resolved = &res
			return r, nil
		}
	} else {
		base := opt.TempDir
		if base == "" {
			base = os.TempDir()
		}
		if err := os.MkdirAll(base, 0o755); err != nil {
			return nil, err
		}
		dir, err = os.MkdirTemp(base, "sarasa-")
		if err != nil {
			return nil, err
		}
	}

	// Download every SarasaUiSC file into the cache so the other preset does
	// not need the network again; without a cache only fetch what was asked.
	want := files
	if opt.CacheDir != "" {
		want = union(files, recipe.SarasaFiles)
	}
	url, err := fetch.ApplyMirror(opt.Mirror, res.URL)
	if err != nil {
		return nil, err
	}
	sink.Stage("download", fmt.Sprintf("远程压缩包：%s（%.1f MiB，仅按需分段下载）", res.Name, float64(res.Size)/(1<<20)))
	sink.Infof("HTTP Range 模式：只取 %d 个 SarasaUiSC 文件", len(want))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	fetched, err := remotezip.ExtractSelected(ctx, client, url, res.Size, dir, want, sink)
	if err != nil {
		return nil, err
	}
	sink.Infof("实际下载流量：%.1f MiB", float64(fetched)/(1<<20))

	sums, err := verifyFiles(dir, want)
	if err != nil {
		return nil, err
	}
	if opt.CacheDir != "" {
		meta := cacheMeta{Version: res.Version, Package: res.Name, Hinted: opt.Hinted, Files: sums}
		b, _ := json.MarshalIndent(meta, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, metaName), b, 0o644); err != nil {
			return nil, err
		}
	}
	return &Result{Dir: dir, Version: res.Version, Package: res.Name, Origin: "download", Files: pick(sums, files), Fetched: fetched, TempDir: opt.CacheDir == "", Resolved: &res}, nil
}

func acquireLocal(opt Options, files []string, sink progress.Sink) (*Result, error) {
	st, err := os.Stat(opt.Local)
	if err != nil {
		return nil, fmt.Errorf("本地 Sarasa 来源不存在：%w", err)
	}
	dir := opt.Local
	origin := "local"
	tempDir := false
	if !st.IsDir() {
		if !strings.EqualFold(filepath.Ext(opt.Local), ".zip") {
			return nil, fmt.Errorf("本地 Sarasa 来源必须是文件夹或 SarasaUi-TTF-*.zip：%s", opt.Local)
		}
		base := opt.TempDir
		if base == "" {
			base = os.TempDir()
		}
		if err := os.MkdirAll(base, 0o755); err != nil {
			return nil, err
		}
		dir, err = os.MkdirTemp(base, "sarasa-zip-")
		if err != nil {
			return nil, err
		}
		tempDir = true
		sink.Stage("extract", "正在从本地压缩包解出 "+filepath.Base(opt.Local))
		if err := remotezip.ExtractLocal(opt.Local, dir, files, sink); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	}
	var missing []string
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s 中缺少：%s", opt.Local, strings.Join(missing, ", "))
	}
	sums, err := verifyFiles(dir, files)
	if err != nil {
		return nil, err
	}
	version := ""
	if b, err := os.ReadFile(filepath.Join(dir, files[0])); err == nil {
		if f, err := otfont.Parse(b); err == nil {
			version, _ = otfont.VersionFromFont(f)
		}
	}
	sink.Infof("使用本地 Sarasa 字体：%s（版本 %s）", opt.Local, version)
	return &Result{Dir: dir, Version: version, Package: filepath.Base(opt.Local), Origin: origin, Files: sums, TempDir: tempDir}, nil
}

func cacheDir(root, version string, hinted bool) string {
	kind := "hinted"
	if !hinted {
		kind = "unhinted"
	}
	return filepath.Join(root, "sarasa", version+"-"+kind)
}

// tryCache accepts a cache directory only if every requested file is listed
// in cache.json and still hashes to the recorded value.
func tryCache(dir string, files []string, sink progress.Sink) (*Result, bool) {
	b, err := os.ReadFile(filepath.Join(dir, metaName))
	if err != nil {
		return nil, false
	}
	var meta cacheMeta
	if json.Unmarshal(b, &meta) != nil {
		return nil, false
	}
	for _, f := range files {
		want, ok := meta.Files[f]
		if !ok {
			return nil, false
		}
		got, err := hashFile(filepath.Join(dir, f))
		if err != nil || got != want {
			sink.Warnf("缓存文件 %s 校验失败，将重新下载", f)
			return nil, false
		}
	}
	sink.Infof("使用已缓存的 Sarasa %s（%s），无需下载", meta.Version, meta.Package)
	return &Result{Dir: dir, Version: meta.Version, Package: meta.Package, Origin: "cache", Files: pick(meta.Files, files)}, true
}

// newestCache finds the highest cached version for the hinting mode.
func newestCache(root string, hinted bool) (string, string) {
	kind := "-hinted"
	if !hinted {
		kind = "-unhinted"
	}
	entries, err := os.ReadDir(filepath.Join(root, "sarasa"))
	if err != nil {
		return "", ""
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), kind) {
			versions = append(versions, strings.TrimSuffix(e.Name(), kind))
		}
	}
	if len(versions) == 0 {
		return "", ""
	}
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[i], versions[j]) })
	v := versions[len(versions)-1]
	return filepath.Join(root, "sarasa", v+kind), v
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscan(pa[i], &x)
		}
		if i < len(pb) {
			fmt.Sscan(pb[i], &y)
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// CachedVersions lists cached Sarasa versions (for the GUI).
func CachedVersions(root string) []string {
	entries, err := os.ReadDir(filepath.Join(root, "sarasa"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(root, "sarasa", e.Name(), metaName)); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

func verifyFiles(dir string, files []string) (map[string]string, error) {
	sums := map[string]string{}
	for _, f := range files {
		path := filepath.Join(dir, f)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if _, err := otfont.Parse(b); err != nil {
			return nil, fmt.Errorf("%s 不是有效的 TTF：%w", f, err)
		}
		sum := sha256.Sum256(b)
		sums[f] = hex.EncodeToString(sum[:])
	}
	return sums, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string(nil), a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func pick(m map[string]string, keys []string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}
