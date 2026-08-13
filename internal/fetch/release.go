package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const LatestReleaseAPI = "https://api.github.com/repos/be5invis/Sarasa-Gothic/releases/latest"

type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

func Latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestReleaseAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "yahei-sarasa-semibold-go")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GitHub latest release: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if r.TagName == "" {
		return nil, fmt.Errorf("GitHub latest release: missing tag_name")
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
// three SarasaUiSC entries we need.
func UIZipAssetName(version string, hinted bool) string {
	if hinted {
		return "SarasaUi-TTF-" + version + ".zip"
	}
	return "SarasaUi-TTF-Unhinted-" + version + ".zip"
}
