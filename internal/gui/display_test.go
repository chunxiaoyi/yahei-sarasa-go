package gui

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/display"
)

func fakeDisplay(scale int) func() display.Info {
	return func() display.Info {
		return display.Info{Supported: true, Smoothing: display.SmoothingClearType, Monitors: []display.Monitor{
			{Name: `\\.\DISPLAY1`, Width: 2560, Height: 1440, DPI: scale * 96 / 100, Scale: scale, Primary: true},
		}}
	}
}

func TestSettingsMigrationFromHintedBool(t *testing.T) {
	for legacy, want := range map[string]string{
		`{"hinted": false}`: "unhinted",
		`{"hinted": true}`:  "auto",
		`{}`:                "auto",
		`{"hinted": false, "hintMode": "hinted"}`: "hinted",
		`{"hintMode": "bogus"}`:                   "auto",
	} {
		cache := t.TempDir()
		if err := os.WriteFile(filepath.Join(cache, "settings.json"), []byte(legacy), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := newServer(Options{CacheDir: cache, OutRoot: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if s.settings.HintMode != want {
			t.Errorf("%s -> %q want %q", legacy, s.settings.HintMode, want)
		}
		s.cleanup()
	}
}

func TestHintModeValidationAndDisplayEndpoint(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do("POST", "/api/settings", map[string]any{"hintMode": "maybe"}, true); code != http.StatusBadRequest {
		t.Fatalf("bad hint mode accepted: %d", code)
	}
	if code, st := e.do("POST", "/api/settings", map[string]any{"hintMode": "unhinted"}, true); code != http.StatusOK || st["hintMode"] != "unhinted" {
		t.Fatalf("%d %v", code, st)
	}
	e.s.detect = fakeDisplay(200)
	_, d := e.do("GET", "/api/display", nil, true)
	rec := d["recommendation"].(map[string]any)
	if rec["hinted"] != false {
		t.Fatalf("200%% should recommend unhinted: %v", rec)
	}
	e.s.detect = fakeDisplay(100)
	_, d = e.do("GET", "/api/display", nil, true)
	if d["recommendation"].(map[string]any)["hinted"] != true {
		t.Fatal("100% should recommend hinted")
	}
}

// The fixtures have no hinting; the finding is a warning on a 100 % screen
// and only informational on a 200 % one.
func TestUnhintedCustomFontDependsOnDisplay(t *testing.T) {
	e := newEnv(t)
	fx, _ := filepath.Abs(filepath.Join("..", "custom", "testdata", "fixture-regular.ttf"))
	e.do("POST", "/api/fonts/add", map[string]any{"paths": []string{fx}}, true)
	severity := func(scale int) string {
		e.s.detect = fakeDisplay(scale)
		_, plan := e.do("POST", "/api/custom/plan", map[string]any{"style": "regular"}, true)
		for _, g := range plan["faces"].([]any) {
			for _, is := range g.(map[string]any)["issues"].([]any) {
				m := is.(map[string]any)
				if m["code"] == "unhinted" {
					return m["severity"].(string)
				}
			}
		}
		return ""
	}
	if got := severity(100); got != "warn" {
		t.Fatalf("100%%: %q", got)
	}
	if got := severity(200); got != "info" {
		t.Fatalf("200%%: %q", got)
	}
}
