package gui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/app"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/progress"
)

type testEnv struct {
	t   *testing.T
	s   *server
	ts  *httptest.Server
	out string
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	out := t.TempDir()
	s, err := newServer(Options{CacheDir: t.TempDir(), OutRoot: out})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.cleanup)
	ts := httptest.NewUnstartedServer(nil)
	s.addr = ts.Listener.Addr().String()
	ts.Config.Handler = s.routes()
	ts.Start()
	t.Cleanup(ts.Close)
	return &testEnv{t, s, ts, out}
}

func (e *testEnv) do(method, path string, body any, token bool) (int, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token {
		req.Header.Set("X-Token", e.s.token)
	}
	resp, err := e.ts.Client().Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestTokenAndHostGuards(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do("GET", "/api/state", nil, false); code != http.StatusForbidden {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := e.do("GET", "/api/state", nil, true); code != http.StatusOK {
		t.Fatalf("with token: %d", code)
	}
	resp, _ := e.ts.Client().Get(e.ts.URL + "/")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("index without token: %d", resp.StatusCode)
	}
	resp, _ = e.ts.Client().Get(e.ts.URL + "/?t=" + e.s.token)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("index with token: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", e.ts.URL+"/api/state", nil)
	req.Host = "attacker.example"
	req.Header.Set("X-Token", e.s.token)
	resp, _ = e.ts.Client().Do(req)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Host accepted: %d", resp.StatusCode)
	}
	resp, _ = e.ts.Client().Get(e.ts.URL + "/api/events")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("events without token: %d", resp.StatusCode)
	}
	// A cross-site form post cannot set JSON + token, but check JSON is required.
	req, _ = http.NewRequest("POST", e.ts.URL+"/api/preset", strings.NewReader("preset=regular"))
	req.Header.Set("X-Token", e.s.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, _ = e.ts.Client().Do(req)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("form post: %d", resp.StatusCode)
	}
}

func TestSettingsValidation(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do("POST", "/api/settings", map[string]any{"outRoot": "relative/dir"}, true); code != http.StatusBadRequest {
		t.Fatalf("relative outRoot accepted: %d", code)
	}
	if code, _ := e.do("POST", "/api/settings", map[string]any{"proxyMode": "custom", "proxy": "ftp://x"}, true); code != http.StatusBadRequest {
		t.Fatalf("bad proxy accepted: %d", code)
	}
	if code, _ := e.do("POST", "/api/settings", map[string]any{"version": "1.0.x"}, true); code != http.StatusBadRequest {
		t.Fatalf("bad version accepted: %d", code)
	}
	code, st := e.do("POST", "/api/settings", map[string]any{"version": "1.0.42", "zip": false}, true)
	if code != http.StatusOK || st["version"] != "1.0.42" || st["zip"] != false {
		t.Fatalf("%d %v", code, st)
	}
	if _, err := os.Stat(e.s.settingsPath()); err != nil {
		t.Fatal("settings not persisted")
	}
}

func TestCustomFlowThroughAPI(t *testing.T) {
	e := newEnv(t)
	fx := func(n string) string { p, _ := filepath.Abs(filepath.Join("..", "custom", "testdata", n)); return p }
	code, res := e.do("POST", "/api/fonts/add", map[string]any{"paths": []string{fx("fixture-regular.ttf"), fx("fixture-bold.ttf"), fx("fixture-light.ttf"), "/nonexistent.ttf"}}, true)
	if code != http.StatusOK || len(res["files"].([]any)) != 3 || len(res["errors"].([]any)) != 1 {
		t.Fatalf("%d %v", code, res)
	}
	code, plan := e.do("POST", "/api/custom/plan", map[string]any{"style": "semibold"}, true)
	if code != http.StatusOK || plan["canBuild"] != true {
		t.Fatalf("%d %v", code, plan)
	}
	code, job := e.do("POST", "/api/custom/build", map[string]any{"style": "regular", "outputs": []string{"msyh.ttc", "segoeui.ttf"}}, true)
	if code != http.StatusOK || job["state"] != "running" {
		t.Fatalf("%d %v", code, job)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, st := e.do("GET", "/api/state", nil, true)
		j := st["job"].(map[string]any)
		if j["state"] != "running" {
			if j["state"] != "done" {
				t.Fatalf("job ended %v: %v", j["state"], j["error"])
			}
			r := j["result"].(map[string]any)
			if len(r["outputs"].([]any)) != 2 || r["zipPath"] == "" {
				t.Fatalf("result %v", r)
			}
			if _, err := os.Stat(r["zipPath"].(string)); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Removing a font file drops it from the plan.
	files := res["files"].([]any)
	id := files[0].(map[string]any)["id"].(string)
	_, res = e.do("POST", "/api/fonts/remove", map[string]any{"id": id}, true)
	if len(res["files"].([]any)) != 2 {
		t.Fatalf("remove: %v", res)
	}
}

func TestSecondJobRejectedWhileRunning(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	_, err := e.s.startJob("test", "blocking", func(ctx context.Context, _ progress.Sink) (*app.Result, string, error) {
		<-release
		return nil, "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	code, _ := e.do("POST", "/api/preset", map[string]any{"preset": "regular"}, true)
	close(release)
	if code != http.StatusConflict {
		t.Fatalf("second job: %d", code)
	}
}
