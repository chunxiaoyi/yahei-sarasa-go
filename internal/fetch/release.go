package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	Repo             = "be5invis/Sarasa-Gothic"
	LatestReleaseAPI = "https://api.github.com/repos/" + Repo + "/releases/latest"
	UserAgent        = "yahei-sarasa-go"
)

// Endpoints are variables only so tests can point them at a local server.
var (
	latestAPI        = LatestReleaseAPI
	tagReleaseAPI    = "https://api.github.com/repos/" + Repo + "/releases/tags/"
	latestReleaseWeb = "https://github.com/" + Repo + "/releases/latest"
	downloadBase     = "https://github.com/" + Repo + "/releases/download/"
)

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Latest asks the GitHub API for the newest release. GITHUB_TOKEN, when set,
// is sent to lift the anonymous 60 requests/hour limit.
func Latest(ctx context.Context) (*Release, error) {
	return getRelease(ctx, http.DefaultClient, latestAPI)
}

func getRelease(ctx context.Context, client *http.Client, api string) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", UserAgent)
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GitHub release API: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if r.TagName == "" {
		return nil, fmt.Errorf("GitHub release API: missing tag_name")
	}
	return &r, nil
}

func (r *Release) Version() string { return strings.TrimPrefix(r.TagName, "v") }

func (r *Release) FindAsset(name string) (Asset, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s does not contain asset %q", r.TagName, name)
}

// UIZipAssetName selects the Single Family Ui TTF ZIP. The program does not
// download the whole ZIP: remotezip issues HTTP Range requests only for the
// SarasaUiSC entries we need.
func UIZipAssetName(version string, hinted bool) string {
	if hinted {
		return "SarasaUi-TTF-" + version + ".zip"
	}
	return "SarasaUi-TTF-Unhinted-" + version + ".zip"
}

// Resolved is a concrete downloadable Sarasa UI package.
type Resolved struct {
	Version string `json:"version"`
	Tag     string `json:"tag"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Size    int64  `json:"size"`
	Via     string `json:"via"` // "api" or "web"
}

// ResolveOptions controls Resolve.
type ResolveOptions struct {
	Version string // "" or "latest" for the newest release, e.g. "1.0.42"
	Hinted  bool
	Client  *http.Client
	// SizeProbe measures an asset when the API is unavailable.
	SizeProbe func(ctx context.Context, client *http.Client, url string) (int64, error)
}

// Resolve finds the Sarasa UI TTF package. It prefers the GitHub API and
// falls back to the public web endpoints (no rate limit) when the API is
// unreachable or rate-limited.
func Resolve(ctx context.Context, opt ResolveOptions) (Resolved, error) {
	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}
	version, err := NormalizeVersion(opt.Version)
	if err != nil {
		return Resolved{}, err
	}

	api := latestAPI
	if version != "" {
		api = tagReleaseAPI + "v" + version
	}
	rel, apiErr := getRelease(ctx, client, api)
	if apiErr == nil {
		name := UIZipAssetName(rel.Version(), opt.Hinted)
		a, err := rel.FindAsset(name)
		if err != nil {
			return Resolved{}, err
		}
		return Resolved{Version: rel.Version(), Tag: rel.TagName, Name: a.Name, URL: a.BrowserDownloadURL, Size: a.Size, Via: "api"}, nil
	}
	if ctx.Err() != nil {
		return Resolved{}, ctx.Err()
	}

	// Fallback: github.com/<repo>/releases/latest redirects to .../tag/vX.
	if version == "" {
		v, err := latestViaRedirect(ctx, client)
		if err != nil {
			return Resolved{}, fmt.Errorf("%v; web fallback: %w", apiErr, err)
		}
		version = v
	}
	name := UIZipAssetName(version, opt.Hinted)
	u := downloadBase + "v" + version + "/" + name
	if opt.SizeProbe == nil {
		return Resolved{}, fmt.Errorf("%v; web fallback needs a size probe", apiErr)
	}
	size, err := opt.SizeProbe(ctx, client, u)
	if err != nil {
		return Resolved{}, fmt.Errorf("%v; web fallback %s: %w", apiErr, name, err)
	}
	return Resolved{Version: version, Tag: "v" + version, Name: name, URL: u, Size: size, Via: "web"}, nil
}

// NormalizeVersion turns "", "latest", "v1.0.42" or "1.0.42" into "" (latest)
// or a bare dotted version.
func NormalizeVersion(v string) (string, error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" || strings.EqualFold(v, "latest") {
		return "", nil
	}
	if !validVersion(v) {
		return "", fmt.Errorf("invalid Sarasa version %q (expected like 1.0.42)", v)
	}
	return v, nil
}

func latestViaRedirect(ctx context.Context, client *http.Client) (string, error) {
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestReleaseWeb, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := noRedirect.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("no redirect from %s (%s)", latestReleaseWeb, resp.Status)
	}
	return versionFromTagURL(loc)
}

func versionFromTagURL(loc string) (string, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return "", err
	}
	i := strings.LastIndex(u.Path, "/tag/")
	if i < 0 {
		return "", fmt.Errorf("unexpected release redirect %q", loc)
	}
	v := strings.TrimPrefix(u.Path[i+len("/tag/"):], "v")
	if !validVersion(v) {
		return "", fmt.Errorf("unexpected release tag in %q", loc)
	}
	return v, nil
}

func validVersion(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// ApplyMirror prefixes a GitHub download URL with a mirror such as
// "https://ghfast.top/". An empty mirror returns the URL unchanged.
func ApplyMirror(mirror, u string) (string, error) {
	mirror = strings.TrimSpace(mirror)
	if mirror == "" {
		return u, nil
	}
	mu, err := url.Parse(mirror)
	if err != nil || (mu.Scheme != "https" && mu.Scheme != "http") || mu.Host == "" {
		return "", errors.New("mirror must be an http(s) URL prefix, e.g. https://ghfast.top/")
	}
	if !strings.HasSuffix(mirror, "/") {
		mirror += "/"
	}
	return mirror + u, nil
}
