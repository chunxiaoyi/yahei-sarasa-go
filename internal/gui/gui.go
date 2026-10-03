// Package gui serves the graphical interface: a small web app embedded in the
// binary, served on a random loopback port behind a per-run token, shown in a
// chromeless Microsoft Edge window (or the default browser as a fallback).
package gui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/platform"
)

//go:embed web
var webFS embed.FS

// Options configures Run.
type Options struct {
	Addr         string // default 127.0.0.1:0
	ForceBrowser bool
	NoOpen       bool
	OutRoot      string
	CacheDir     string
	// IdleGrace is how long the server lingers after the last window
	// closes (zero = 10s).
	IdleGrace time.Duration
}

const (
	defaultIdleGrace = 10 * time.Second
	firstClientWait  = 45 * time.Second
)

// Run serves the GUI until the window is closed or ctx ends.
func Run(ctx context.Context, opt Options) error {
	platform.Prepare()
	if opt.Addr == "" {
		opt.Addr = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(opt.Addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("gui: refusing to listen on non-loopback address %s", opt.Addr)
	}
	if opt.IdleGrace == 0 {
		opt.IdleGrace = defaultIdleGrace
	}

	s, err := newServer(opt)
	if err != nil {
		return err
	}
	defer s.cleanup()

	ln, err := net.Listen("tcp", opt.Addr)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String()
	url := fmt.Sprintf("http://%s/?t=%s", s.addr, s.token)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.shutdown = cancel

	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		s.cancelJob()
		sctx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	}()

	s.logf("listening on %s", s.addr)
	if opt.NoOpen {
		fmt.Println("用浏览器打开这个地址使用图形界面：", url)
	} else {
		go s.openWindow(ctx, url, opt.ForceBrowser)
		go s.watchLifetime(ctx, opt.IdleGrace)
	}

	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func (s *server) openWindow(ctx context.Context, url string, forceBrowser bool) {
	if !forceBrowser {
		cmd, err := platform.AppWindow(url, filepath.Join(s.cacheDir, "window"))
		switch {
		case err == nil && cmd != nil:
			s.logf("opened app window (pid %d)", cmd.Process.Pid)
			go func() { _ = cmd.Wait() }()
			// If the page never connects, fall back to the default browser.
			select {
			case <-ctx.Done():
				return
			case <-s.firstClient:
				return
			case <-time.After(15 * time.Second):
				s.logf("app window did not connect; falling back to the default browser")
			}
		case err != nil:
			s.logf("app window failed: %v", err)
		}
	}
	if err := platform.OpenBrowser(url); err != nil {
		s.logf("open browser: %v", err)
		fmt.Println("请手动用浏览器打开：", url)
	}
}

// watchLifetime stops the server once no window has been connected for
// grace (after the first connection), or when nothing ever connects.
func (s *server) watchLifetime(ctx context.Context, grace time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-s.firstClient:
	case <-time.After(firstClientWait):
		s.logf("no window ever connected; exiting")
		s.shutdown()
		return
	}
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	var idleSince time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if s.clientCount() > 0 {
			idleSince = time.Time{}
			continue
		}
		if idleSince.IsZero() {
			idleSince = time.Now()
		}
		if time.Since(idleSince) >= grace {
			s.logf("window closed; exiting")
			s.shutdown()
			return
		}
	}
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// guiLogger writes gui.log in the cache folder for troubleshooting.
type guiLogger struct {
	mu   sync.Mutex
	l    *log.Logger
	path string
}

func newLogger(dir string) *guiLogger {
	g := &guiLogger{}
	if err := os.MkdirAll(dir, 0o755); err == nil {
		g.path = filepath.Join(dir, "gui.log")
		if f, err := os.Create(g.path); err == nil {
			g.l = log.New(io.MultiWriter(f, os.Stderr), "", log.LstdFlags)
			return g
		}
	}
	g.l = log.New(os.Stderr, "", log.LstdFlags)
	return g
}

func (g *guiLogger) printf(format string, args ...any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.l.Printf(format, args...)
}

func staticFS() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	return sub
}
