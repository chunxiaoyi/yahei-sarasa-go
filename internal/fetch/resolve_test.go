package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withEndpoints(t *testing.T, base string) {
	t.Helper()
	old := []string{latestAPI, tagReleaseAPI, latestReleaseWeb, downloadBase}
	latestAPI = base + "/api/latest"
	tagReleaseAPI = base + "/api/tags/"
	latestReleaseWeb = base + "/web/latest"
	downloadBase = base + "/dl/"
	t.Cleanup(func() { latestAPI, tagReleaseAPI, latestReleaseWeb, downloadBase = old[0], old[1], old[2], old[3] })
}

func TestResolveViaAPI(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/latest" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"tag_name":"v1.0.42","assets":[{"name":"SarasaUi-TTF-1.0.42.zip","browser_download_url":"https://x/zip","size":123}]}`)
	}))
	defer ts.Close()
	withEndpoints(t, ts.URL)
	r, err := Resolve(context.Background(), ResolveOptions{Hinted: true, Client: ts.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "1.0.42" || r.Size != 123 || r.Via != "api" {
		t.Fatalf("%+v", r)
	}
}

// When the API is rate-limited, the latest version comes from the web
// redirect and the asset size from a probe.
func TestResolveFallsBackToWeb(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/latest":
			http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
		case "/web/latest":
			http.Redirect(w, r, "/be5invis/Sarasa-Gothic/releases/tag/v1.0.43", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	withEndpoints(t, ts.URL)
	var probed string
	r, err := Resolve(context.Background(), ResolveOptions{Hinted: false, Client: ts.Client(),
		SizeProbe: func(_ context.Context, _ *http.Client, u string) (int64, error) { probed = u; return 999, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "1.0.43" || r.Via != "web" || r.Size != 999 || r.Name != "SarasaUi-TTF-Unhinted-1.0.43.zip" {
		t.Fatalf("%+v", r)
	}
	if probed != ts.URL+"/dl/v1.0.43/SarasaUi-TTF-Unhinted-1.0.43.zip" {
		t.Fatalf("probed %s", probed)
	}
}
