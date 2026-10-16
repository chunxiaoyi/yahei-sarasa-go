package gui

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/app"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/custom"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/display"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/fetch"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/pack"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/platform"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/progress"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/recipe"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/sarasa"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/sysfonts"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/sysinstall"
)

// Settings persist between runs in <cache>/settings.json.
type Settings struct {
	OutRoot     string `json:"outRoot"`
	HintMode    string `json:"hintMode"` // auto | hinted | unhinted
	Zip         bool   `json:"zip"`
	Version     string `json:"version"`     // "" = latest
	UseLocal    bool   `json:"useLocal"`    // use SarasaLocal instead of downloading
	SarasaLocal string `json:"sarasaLocal"` // folder or zip
	ProxyMode   string `json:"proxyMode"`   // auto | direct | custom
	Proxy       string `json:"proxy"`
	Mirror      string `json:"mirror"`
}

type job struct {
	ID      string      `json:"id"`
	Kind    string      `json:"kind"`
	Title   string      `json:"title"`
	State   string      `json:"state"` // running, done, failed, canceled
	Started time.Time   `json:"started"`
	Ended   *time.Time  `json:"ended,omitempty"`
	Error   string      `json:"error,omitempty"`
	Result  *app.Result `json:"result,omitempty"`
	Backup  string      `json:"backup,omitempty"`
	Action  string      `json:"action,omitempty"` // system jobs: install, restore, clear-cache, cancel
	Events  []jobEvent  `json:"events,omitempty"`
	cancel  context.CancelFunc
}

type jobEvent struct {
	progress.Event
	T int64 `json:"t"` // unix ms
}

type server struct {
	opt       Options
	token     string
	addr      string
	cacheDir  string
	uploadDir string
	log       *guiLogger
	shutdown  context.CancelFunc

	mu       sync.Mutex
	settings Settings
	job      *job
	jobSeq   int
	files    map[string]*custom.File
	order    []string

	// detect reports the screens; tests replace it.
	detect func() display.Info

	clientsMu   sync.Mutex
	clients     map[chan []byte]bool
	firstClient chan struct{}
	firstOnce   sync.Once
}

func newServer(opt Options) (*server, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	cacheDir := opt.CacheDir
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	uploadDir, err := os.MkdirTemp("", "yahei-sarasa-upload-")
	if err != nil {
		return nil, err
	}
	s := &server{
		opt: opt, token: token, cacheDir: cacheDir, uploadDir: uploadDir,
		log:         newLogger(cacheDir),
		files:       map[string]*custom.File{},
		clients:     map[chan []byte]bool{},
		firstClient: make(chan struct{}),
		shutdown:    func() {},
		detect:      display.Detect,
	}
	s.settings = s.loadSettings()
	if opt.OutRoot != "" {
		s.settings.OutRoot = opt.OutRoot
	}
	fetch.SystemProxy = platform.SystemProxy
	return s, nil
}

func (s *server) logf(format string, args ...any) { s.log.printf(format, args...) }

func (s *server) cleanup() {
	s.log.close()
	_ = os.RemoveAll(s.uploadDir)
}

func defaultCacheDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "yahei-sarasa-go")
	}
	return filepath.Join(os.TempDir(), "yahei-sarasa-go")
}

// defaultOutRoot prefers an "output" folder next to the program (portable
// use), falling back to Documents when that is not writable.
func defaultOutRoot() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if !strings.Contains(dir, "go-build") && writable(dir) {
			return filepath.Join(dir, "output")
		}
	}
	if wd, err := os.Getwd(); err == nil && writable(wd) {
		return filepath.Join(wd, "output")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents", "yahei-sarasa-go")
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func (s *server) settingsPath() string { return filepath.Join(s.cacheDir, "settings.json") }

func (s *server) loadSettings() Settings {
	st := Settings{OutRoot: defaultOutRoot(), HintMode: string(display.ModeAuto), Zip: true, ProxyMode: "auto"}
	if b, err := os.ReadFile(s.settingsPath()); err == nil {
		_ = json.Unmarshal(b, &st)
		// Settings written before the hint mode existed carry "hinted": bool.
		// An explicit false was a choice; true was the old default -> auto.
		var old struct {
			Hinted *bool `json:"hinted"`
		}
		if json.Unmarshal(b, &old) == nil && old.Hinted != nil && !strings.Contains(string(b), `"hintMode"`) {
			if *old.Hinted {
				st.HintMode = string(display.ModeAuto)
			} else {
				st.HintMode = string(display.ModeUnhinted)
			}
		}
	}
	if _, err := display.ParseMode(st.HintMode); err != nil {
		st.HintMode = string(display.ModeAuto)
	}
	if st.OutRoot == "" {
		st.OutRoot = defaultOutRoot()
	}
	if st.ProxyMode == "" {
		st.ProxyMode = "auto"
	}
	return st
}

func (s *server) saveSettings(st Settings) error {
	if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	return os.WriteFile(s.settingsPath(), b, 0o644)
}

// ---- routing & security --------------------------------------------------

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/state", s.api(s.handleState))
	mux.HandleFunc("/api/settings", s.api(s.handleSettings))
	mux.HandleFunc("/api/display", s.api(s.handleDisplay))
	mux.HandleFunc("/api/preset", s.api(s.handlePreset))
	mux.HandleFunc("/api/cancel", s.api(s.handleCancel))
	mux.HandleFunc("/api/pick", s.api(s.handlePick))
	mux.HandleFunc("/api/reveal", s.api(s.handleReveal))
	mux.HandleFunc("/api/fonts/add", s.api(s.handleFontsAdd))
	mux.HandleFunc("/api/fonts/upload", s.api(s.handleFontsUpload))
	mux.HandleFunc("/api/fonts/remove", s.api(s.handleFontsRemove))
	mux.HandleFunc("/api/custom/plan", s.api(s.handleCustomPlan))
	mux.HandleFunc("/api/custom/build", s.api(s.handleCustomBuild))
	mux.HandleFunc("/api/system", s.api(s.handleSystem))
	mux.HandleFunc("/api/system/backup", s.api(s.handleSystemBackup))
	mux.HandleFunc("/api/system/plan", s.api(s.handleSystemPlan))
	mux.HandleFunc("/api/system/run", s.api(s.handleSystemRun))
	mux.HandleFunc("/api/system/reboot", s.api(s.handleSystemReboot))
	mux.HandleFunc("/api/packages", s.api(s.handlePackages))

	static := http.FileServer(http.FS(staticFS()))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && !s.tokenOK(r.URL.Query().Get("t")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		static.ServeHTTP(w, r)
	})
	return s.guard(mux)
}

func (s *server) tokenOK(t string) bool {
	return len(t) == len(s.token) && subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

// guard rejects requests whose Host is not our loopback address (DNS
// rebinding) and sets conservative headers.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := net.SplitHostPort(s.addr)
		host := r.Host
		if host != s.addr && host != "localhost:"+port && host != "127.0.0.1:"+port {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
		next.ServeHTTP(w, r)
	})
}

type apiFunc func(r *http.Request) (any, error)

type httpError struct {
	code int
	msg  string
}

func (e httpError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return httpError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

// api wraps a JSON endpoint: token header required, JSON in/out, errors as
// {"error": "..."}.
func (s *server) api(fn apiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.tokenOK(r.Header.Get("X-Token")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		out, err := fn(r)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			code := http.StatusInternalServerError
			var he httpError
			if errors.As(err, &he) {
				code = he.code
			}
			s.logf("%s %s: %v", r.Method, r.URL.Path, err)
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func decode(r *http.Request, v any) error {
	if r.Method != http.MethodPost {
		return httpError{http.StatusMethodNotAllowed, "POST required"}
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct != "application/json" {
		return badRequest("JSON body required")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return badRequest("bad JSON: %v", err)
	}
	return nil
}

// ---- events --------------------------------------------------------------

func (s *server) clientCount() int {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	return len(s.clients)
}

func (s *server) broadcast(kind string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	msg := []byte("event: " + kind + "\ndata: " + string(b) + "\n\n")
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for c := range s.clients {
		select {
		case c <- msg:
		default: // a stalled client misses this event rather than blocking the build
		}
	}
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.tokenOK(r.URL.Query().Get("t")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch := make(chan []byte, 4096)
	s.clientsMu.Lock()
	s.clients[ch] = true
	s.clientsMu.Unlock()
	s.firstOnce.Do(func() { close(s.firstClient) })
	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ch)
		s.clientsMu.Unlock()
	}()

	s.mu.Lock()
	hello, _ := json.Marshal(map[string]any{"job": s.job})
	s.mu.Unlock()
	fmt.Fprintf(w, "retry: 1000\nevent: hello\ndata: %s\n\n", hello)
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// ---- jobs ----------------------------------------------------------------

func (s *server) startJob(kind, title string, run func(ctx context.Context, sink progress.Sink) (*app.Result, string, error)) (*job, error) {
	s.mu.Lock()
	if s.job != nil && s.job.State == "running" {
		s.mu.Unlock()
		return nil, httpError{http.StatusConflict, "已有任务在运行，请等它结束或先取消"}
	}
	s.jobSeq++
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{ID: fmt.Sprintf("job-%d", s.jobSeq), Kind: kind, Title: title, State: "running", Started: time.Now(), cancel: cancel}
	s.job = j
	s.mu.Unlock()
	s.logf("job %s started: %s", j.ID, title)
	s.broadcast("job", j.summary())

	sink := func(e progress.Event) {
		je := jobEvent{Event: e, T: time.Now().UnixMilli()}
		s.mu.Lock()
		if e.Kind == progress.KindProgress && len(j.Events) > 0 {
			// Coalesce consecutive progress updates for the same item.
			last := &j.Events[len(j.Events)-1]
			if last.Kind == progress.KindProgress && last.Msg == e.Msg {
				*last = je
			} else {
				j.Events = append(j.Events, je)
			}
		} else {
			j.Events = append(j.Events, je)
		}
		if len(j.Events) > 3000 {
			j.Events = j.Events[len(j.Events)-3000:]
		}
		s.mu.Unlock()
		if e.Kind == progress.KindLog {
			s.logf("[%s] %s", e.Level, e.Msg)
		}
		s.broadcast("event", map[string]any{"job": j.ID, "event": je})
	}

	go func() {
		defer func() {
			if p := recover(); p != nil {
				buf := make([]byte, 8<<10)
				buf = buf[:runtime.Stack(buf, false)]
				s.logf("job %s panic: %v\n%s", j.ID, p, buf)
				s.finishJob(j, nil, "", fmt.Errorf("内部错误：%v（详情见 gui.log）", p))
			}
		}()
		res, backup, err := run(ctx, sink)
		s.finishJob(j, res, backup, err)
	}()
	return j, nil
}

func (s *server) finishJob(j *job, res *app.Result, backup string, err error) {
	now := time.Now()
	s.mu.Lock()
	j.Ended = &now
	j.Result = res
	j.Backup = backup
	switch {
	case err == nil:
		j.State = "done"
	case errors.Is(err, context.Canceled):
		j.State = "canceled"
		j.Error = "已取消"
	default:
		j.State = "failed"
		j.Error = err.Error()
	}
	s.mu.Unlock()
	s.logf("job %s %s %s", j.ID, j.State, j.Error)
	s.broadcast("job", j.summary())
	runtime.GC()
}

// summary is the job without its (possibly long) event backlog.
func (j *job) summary() *job {
	c := *j
	c.Events = nil
	return &c
}

func (s *server) cancelJob() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job != nil && s.job.State == "running" && s.job.cancel != nil {
		s.job.cancel()
	}
}

func (s *server) handleCancel(r *http.Request) (any, error) {
	s.cancelJob()
	return map[string]bool{"ok": true}, nil
}

// ---- state & settings ----------------------------------------------------

type presetView struct {
	ID      string                 `json:"id"`
	Name    string                 `json:"name"`
	Branch  string                 `json:"branch"`
	Summary string                 `json:"summary"`
	Map     map[recipe.Slot]string `json:"map"`
}

type slotView struct {
	ID     recipe.Slot `json:"id"`
	Title  string      `json:"title"`
	Weight uint16      `json:"weight"`
}

type outputView struct {
	Name  string        `json:"name"`
	Slots []recipe.Slot `json:"slots"`
	Desc  string        `json:"desc"`
}

var outputDesc = map[string]string{
	recipe.OutMSYH:    "微软雅黑 / 微软雅黑 UI Regular",
	recipe.OutMSYHBD:  "微软雅黑 Bold + SemiBold 600",
	recipe.OutMSYHL:   "微软雅黑 Light",
	recipe.OutSimSun:  "宋体 / 新宋体",
	recipe.OutSimSunB: "SimSun-ExtB",
	recipe.OutSegoeUI: "Segoe UI Regular（仅英文、数字等原版码位）",
}

func (s *server) handleState(r *http.Request) (any, error) {
	s.mu.Lock()
	st := s.settings
	var jobView *job
	if s.job != nil {
		c := *s.job
		jobView = &c
	}
	files := s.fileList()
	s.mu.Unlock()

	var presets []presetView
	for _, p := range recipe.Presets {
		presets = append(presets, presetView{p.ID, p.Name, p.Branch, p.Summary, p.Map})
	}
	var slots []slotView
	for _, sl := range recipe.AllSlots {
		slots = append(slots, slotView{sl, custom.SlotTitle(sl), sl.TargetWeight()})
	}
	var outputs []outputView
	for _, o := range recipe.AllOutputs {
		outputs = append(outputs, outputView{o, recipe.SlotsFor(o), outputDesc[o]})
	}
	return map[string]any{
		"version":    app.Version,
		"os":         runtime.GOOS,
		"canPick":    platform.CanPick(),
		"settings":   st,
		"cacheDir":   s.cacheDir,
		"logPath":    s.log.path,
		"cached":     sarasa.CachedVersions(s.cacheDir),
		"presets":    presets,
		"slots":      slots,
		"outputs":    outputs,
		"fonts":      files,
		"job":        jobView,
		"systemDir":  platform.SystemFontsDir(),
		"canElevate": platform.CanElevate(),
		"display":    s.displayView(),
		"sysProxy":   platform.SystemProxy(),
	}, nil
}

func (s *server) handleSettings(r *http.Request) (any, error) {
	if r.Method == http.MethodGet {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.settings, nil
	}
	s.mu.Lock()
	next := s.settings
	s.mu.Unlock()
	if err := decode(r, &next); err != nil {
		return nil, err
	}
	next.OutRoot = strings.TrimSpace(next.OutRoot)
	if next.OutRoot == "" {
		next.OutRoot = defaultOutRoot()
	}
	if !filepath.IsAbs(next.OutRoot) {
		return nil, badRequest("输出文件夹需要是完整路径")
	}
	if m, err := display.ParseMode(next.HintMode); err != nil {
		return nil, badRequest("%v", err)
	} else {
		next.HintMode = string(m)
	}
	switch next.ProxyMode {
	case "auto", "direct", "custom":
	default:
		return nil, badRequest("unknown proxy mode")
	}
	if next.ProxyMode == "custom" {
		if _, _, err := fetch.NewClient(next.Proxy); err != nil {
			return nil, badRequest("%v", err)
		}
	}
	if _, err := fetch.ApplyMirror(next.Mirror, "https://github.com/"); err != nil {
		return nil, badRequest("镜像前缀无效：%v", err)
	}
	if _, err := fetch.NormalizeVersion(next.Version); err != nil {
		return nil, badRequest("%v", err)
	}
	s.mu.Lock()
	s.settings = next
	s.mu.Unlock()
	if err := s.saveSettings(next); err != nil {
		s.logf("save settings: %v", err)
	}
	return next, nil
}

func (s *server) proxyValue(st Settings) string {
	switch st.ProxyMode {
	case "direct":
		return fetch.ProxyNone
	case "custom":
		return st.Proxy
	}
	return ""
}

// ---- display -----------------------------------------------------------------

type displayView struct {
	Info           display.Info           `json:"info"`
	Recommendation display.Recommendation `json:"recommendation"`
}

func (s *server) displayView() displayView {
	info := s.detect()
	return displayView{Info: info, Recommendation: display.Recommend(info)}
}

// handleDisplay re-detects on demand: monitors and scaling can change while
// the window is open (docking a laptop, changing the scale).
func (s *server) handleDisplay(r *http.Request) (any, error) { return s.displayView(), nil }

func (s *server) customDisplayOptions(opt *app.CustomOptions) {
	info := s.detect()
	r := display.Recommend(info)
	opt.DisplayDetected = info.Supported && len(info.Monitors) > 0
	opt.HintingMatters = r.Hinted
	opt.DisplayBasis = r.Basis
}

// ---- presets ---------------------------------------------------------------

func (s *server) handlePreset(r *http.Request) (any, error) {
	var req struct {
		Preset string `json:"preset"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	p, err := recipe.PresetByID(req.Preset)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	s.mu.Lock()
	st := s.settings
	s.mu.Unlock()
	mode, _ := display.ParseMode(st.HintMode)
	hinted, hintNote := display.Resolve(mode, s.detect)
	opt := app.PresetOptions{
		Preset: p.ID, Hinted: hinted, HintNote: hintNote, Version: st.Version,
		CacheDir: s.cacheDir, TempDir: filepath.Join(s.cacheDir, "tmp"),
		Proxy: s.proxyValue(st), Mirror: st.Mirror,
		OutRoot: st.OutRoot, Zip: st.Zip,
	}
	if st.UseLocal {
		if strings.TrimSpace(st.SarasaLocal) == "" {
			return nil, badRequest("已选择使用本地 Sarasa，但还没有选择文件夹或压缩包")
		}
		opt.Local = st.SarasaLocal
	}
	j, err := s.startJob("preset", "生成 "+p.Name+"（原 "+p.Branch+" 分支）", func(ctx context.Context, sink progress.Sink) (*app.Result, string, error) {
		res, err := app.RunPreset(ctx, opt, sink)
		return res, "", err
	})
	if err != nil {
		return nil, err
	}
	return j.summary(), nil
}

// ---- pickers & reveal --------------------------------------------------------

func (s *server) handlePick(r *http.Request) (any, error) {
	var req struct {
		Purpose string `json:"purpose"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if !platform.CanPick() {
		return nil, httpError{http.StatusNotImplemented, "这个系统上没有原生文件对话框，请拖入文件或直接输入路径"}
	}
	fontFilter := []platform.Filter{
		{Name: "字体文件 (*.ttf; *.otf; *.ttc; *.otc; *.woff)", Pattern: "*.ttf;*.otf;*.ttc;*.otc;*.woff"},
		{Name: "所有文件", Pattern: "*.*"},
	}
	var opt platform.PickOptions
	switch req.Purpose {
	case "fonts":
		opt = platform.PickOptions{Title: "选择字体文件（可多选）", Multi: true, Filters: fontFilter}
	case "sarasa-folder":
		opt = platform.PickOptions{Title: "选择包含 SarasaUiSC-*.ttf 的文件夹", Folder: true}
	case "sarasa-zip":
		opt = platform.PickOptions{Title: "选择 SarasaUi-TTF-*.zip", Filters: []platform.Filter{{Name: "Sarasa UI 压缩包 (*.zip)", Pattern: "*.zip"}}}
	case "package":
		opt = platform.PickOptions{Title: "选择字体包文件夹（含 fonts 子文件夹）", Folder: true}
		s.mu.Lock()
		if st, err := os.Stat(s.settings.OutRoot); err == nil && st.IsDir() {
			opt.Initial = s.settings.OutRoot
		}
		s.mu.Unlock()
	case "output":
		opt = platform.PickOptions{Title: "选择输出文件夹", Folder: true}
		s.mu.Lock()
		if st, err := os.Stat(s.settings.OutRoot); err == nil && st.IsDir() {
			opt.Initial = s.settings.OutRoot
		}
		s.mu.Unlock()
	default:
		return nil, badRequest("unknown purpose")
	}
	paths, err := platform.Pick(opt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"paths": paths}, nil
}

func (s *server) handleReveal(r *http.Request) (any, error) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	p := filepath.Clean(req.Path)
	if !filepath.IsAbs(p) {
		return nil, badRequest("path must be absolute")
	}
	if _, err := os.Stat(p); err != nil {
		return nil, badRequest("找不到 %s", p)
	}
	if err := platform.Reveal(p); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// ---- custom fonts -----------------------------------------------------------

func (s *server) fileList() []*custom.File {
	out := make([]*custom.File, 0, len(s.order))
	for _, id := range s.order {
		if f := s.files[id]; f != nil {
			out = append(out, f)
		}
	}
	return out
}

func (s *server) faces() []*custom.Face {
	var out []*custom.Face
	for _, f := range s.fileList() {
		out = append(out, f.Faces...)
	}
	return out
}

func (s *server) register(f *custom.File) {
	key := f.ID
	if f.Error != "" {
		key = "err:" + f.Path
	}
	if _, ok := s.files[key]; !ok {
		s.order = append(s.order, key)
	}
	s.files[key] = f
}

type addResult struct {
	Files  []*custom.File `json:"files"`
	Errors []string       `json:"errors,omitempty"`
}

func (s *server) handleFontsAdd(r *http.Request) (any, error) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	var errs []string
	for _, p := range req.Paths {
		p = strings.Trim(strings.TrimSpace(p), `"`)
		if p == "" {
			continue
		}
		f, err := custom.Inspect(p)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		s.mu.Lock()
		s.register(f)
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return addResult{Files: s.fileList(), Errors: errs}, nil
}

func (s *server) handleFontsUpload(r *http.Request) (any, error) {
	if r.Method != http.MethodPost {
		return nil, httpError{http.StatusMethodNotAllowed, "POST required"}
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 2<<30)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, badRequest("multipart body required")
	}
	var errs []string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, badRequest("upload: %v", err)
		}
		name := filepath.Base(strings.ReplaceAll(part.FileName(), `\`, "/"))
		if name == "" || name == "." || name == "/" {
			part.Close()
			continue
		}
		dir, err := os.MkdirTemp(s.uploadDir, "f-")
		if err != nil {
			return nil, err
		}
		dst := filepath.Join(dir, name)
		out, err := os.Create(dst)
		if err != nil {
			return nil, err
		}
		n, err := io.Copy(out, io.LimitReader(part, custom.MaxFileSize+1))
		out.Close()
		part.Close()
		if err != nil {
			return nil, badRequest("upload %s: %v", name, err)
		}
		if n > custom.MaxFileSize {
			errs = append(errs, name+" 太大")
			continue
		}
		f, err := custom.Inspect(dst)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		s.mu.Lock()
		s.register(f)
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return addResult{Files: s.fileList(), Errors: errs}, nil
}

func (s *server) handleFontsRemove(r *http.Request) (any, error) {
	var req struct {
		ID  string `json:"id"`
		All bool   `json:"all"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.All {
		s.files, s.order = map[string]*custom.File{}, nil
	} else {
		delete(s.files, req.ID)
		for i, id := range s.order {
			if id == req.ID {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
	runtime.GC()
	return addResult{Files: s.fileList()}, nil
}

func (s *server) handleCustomPlan(r *http.Request) (any, error) {
	var req custom.Request
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	faces := s.faces()
	s.mu.Unlock()
	if len(faces) == 0 {
		return map[string]any{"empty": true}, nil
	}
	ev := custom.Evaluate(faces, req)
	var d app.CustomOptions
	s.customDisplayOptions(&d)
	custom.ApplyDisplayContext(&ev, d.DisplayDetected, d.HintingMatters, d.DisplayBasis)
	return ev, nil
}

func (s *server) handleCustomBuild(r *http.Request) (any, error) {
	var req custom.Request
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	faces := s.faces()
	st := s.settings
	s.mu.Unlock()
	ev := custom.Evaluate(faces, req)
	if _, err := custom.ToRecipe(faces, ev); err != nil {
		return nil, badRequest("%v", err)
	}
	opt := app.CustomOptions{Faces: faces, Request: req, OutRoot: st.OutRoot, Zip: st.Zip}
	s.customDisplayOptions(&opt)
	j, err := s.startJob("custom", "生成自定义字体包", func(ctx context.Context, sink progress.Sink) (*app.Result, string, error) {
		res, err := app.RunCustom(ctx, opt, sink)
		return res, "", err
	})
	if err != nil {
		return nil, err
	}
	return j.summary(), nil
}

// ---- system fonts -------------------------------------------------------------

func (s *server) handleSystem(r *http.Request) (any, error) {
	dir := platform.SystemFontsDir()
	if dir == "" {
		return map[string]any{"supported": false}, nil
	}
	paths, err := sysinstall.DefaultPaths()
	if err != nil {
		// Fall back to the read-only file table.
		return map[string]any{"supported": true, "dir": dir, "files": sysfonts.Status(dir), "error": err.Error()}, nil
	}
	rep := sysinstall.GetStatus(sysinstall.System(), paths)
	return map[string]any{"supported": true, "dir": dir, "files": rep.Files, "report": rep, "canElevate": platform.CanElevate()}, nil
}

type systemRequest struct {
	Action   string `json:"action"`   // install, restore, forget, clear-cache, cancel
	Package  string `json:"package"`  // install: package folder
	Snapshot string `json:"snapshot"` // restore (optional, default newest), forget (required)
}

func (req systemRequest) validate() error {
	switch req.Action {
	case "install":
		if strings.TrimSpace(req.Package) == "" {
			return badRequest("请选择要安装的字体包")
		}
		if !filepath.IsAbs(req.Package) {
			return badRequest("字体包需要是完整路径")
		}
	case "forget":
		if !sysinstall.ValidSnapshotID(req.Snapshot) {
			return badRequest("请选择要删除的备份")
		}
	case "restore", "clear-cache", "cancel":
	default:
		return badRequest("unknown action %q", req.Action)
	}
	if req.Snapshot != "" && !sysinstall.ValidSnapshotID(req.Snapshot) {
		return badRequest("备份编号无效")
	}
	return nil
}

// handleSystemPlan is a dry run without privileges; the elevated helper
// repeats every check itself.
func (s *server) handleSystemPlan(r *http.Request) (any, error) {
	var req systemRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	paths, err := sysinstall.DefaultPaths()
	if err != nil {
		return sysinstall.Plan{Action: req.Action, Blockers: []string{err.Error()}}, nil
	}
	sys := sysinstall.System()
	switch req.Action {
	case "install":
		return sysinstall.PlanInstall(sys, paths, req.Package), nil
	case "restore":
		return sysinstall.PlanRestore(sys, paths, req.Snapshot), nil
	case "clear-cache":
		return sysinstall.PlanClearCache(sys, paths), nil
	}
	return sysinstall.Plan{Action: req.Action, Steps: []string{"移除本工具登记的待重启操作，并恢复改动过的文件权限"}}, nil
}

func (s *server) handleSystemRun(r *http.Request) (any, error) {
	var req systemRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := req.validate(); err != nil {
		return nil, err
	}
	if !platform.CanElevate() {
		return nil, httpError{http.StatusNotImplemented, sysinstall.ErrUnsupported.Error()}
	}
	titles := map[string]string{"install": "安装字体包到系统", "restore": "还原备份", "forget": "删除一份备份", "clear-cache": "清空字体缓存", "cancel": "取消待重启的操作"}
	j, err := s.startJob("system", titles[req.Action], func(ctx context.Context, sink progress.Sink) (*app.Result, string, error) {
		return nil, "", s.runElevated(sink, req)
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	j.Action = req.Action
	s.mu.Unlock()
	return j.summary(), nil
}

// runElevated starts this program as `system <action>` through UAC and
// relays the result it leaves in the protected results folder.
func (s *server) runElevated(sink progress.Sink, req systemRequest) error {
	paths, err := sysinstall.DefaultPaths()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	id, err := randomToken()
	if err != nil {
		return err
	}
	id = id[:32]
	args := []string{"system", req.Action}
	switch req.Action {
	case "install":
		args = append(args, req.Package)
	case "restore", "forget":
		if req.Snapshot != "" {
			args = append(args, req.Snapshot)
		}
	}
	args = append(args, "-result", id)
	sink.Stage("elevate", "等待管理员授权：请在弹出的窗口里选择「是」")
	code, err := platform.RunElevated(exe, args)
	if err != nil {
		return err
	}
	res, rerr := sysinstall.ReadResult(paths, id)
	if rerr != nil {
		return fmt.Errorf("管理员进程已退出（代码 %d），但没有留下结果：%v", code, rerr)
	}
	for _, line := range res.Log {
		sink.Infof("%s", line)
	}
	if !res.OK {
		return errors.New(res.Error)
	}
	return nil
}

func (s *server) handleSystemReboot(r *http.Request) (any, error) {
	if r.Method != http.MethodPost {
		return nil, httpError{http.StatusMethodNotAllowed, "POST required"}
	}
	if err := platform.Reboot(); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func (s *server) handlePackages(r *http.Request) (any, error) {
	s.mu.Lock()
	root := s.settings.OutRoot
	s.mu.Unlock()
	return map[string]any{"root": root, "packages": pack.List(root)}, nil
}

func (s *server) handleSystemBackup(r *http.Request) (any, error) {
	dir := platform.SystemFontsDir()
	if dir == "" {
		return nil, httpError{http.StatusNotImplemented, "只有 Windows 上才能备份系统字体"}
	}
	s.mu.Lock()
	root := s.settings.OutRoot
	s.mu.Unlock()
	j, err := s.startJob("backup", "备份当前系统字体", func(ctx context.Context, sink progress.Sink) (*app.Result, string, error) {
		sink.Stage("backup", "正在从 "+dir+" 复制 6 个字体文件")
		dest, err := sysfonts.Backup(dir, root, sink)
		if err == nil {
			sink.OKf("备份完成：%s", dest)
		}
		return nil, dest, err
	})
	if err != nil {
		return nil, err
	}
	return j.summary(), nil
}
