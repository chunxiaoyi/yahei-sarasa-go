package gui

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/chunxiaoyi/yahei-sarasa-go/internal/app"
	"github.com/chunxiaoyi/yahei-sarasa-go/internal/progress"
)

// A running job keeps rewriting its last progress event while the page
// polls /api/state; run with -race to catch shared event slices.
func TestStateWhileJobReportsProgress(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	done := make(chan struct{})
	_, err := e.s.startJob("test", "progress", "", func(ctx context.Context, sink progress.Sink) (*app.Result, string, error) {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-release:
				return nil, "", nil
			default:
			}
			sink(progress.Event{Kind: progress.KindProgress, Msg: "下载", Done: int64(i), Total: 1 << 30})
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if code, _ := e.do("GET", "/api/state", nil, true); code != http.StatusOK {
					t.Errorf("state: %d", code)
				}
			}
		}()
	}
	wg.Wait()
	close(release)
	<-done
}

// A system job's action must be on the job from the first moment anyone
// can see it, not written after the job has started.
func TestJobActionSetBeforeStart(t *testing.T) {
	e := newEnv(t)
	seen := make(chan string, 1)
	snap, err := e.s.startJob("system", "cancel", "cancel", func(ctx context.Context, _ progress.Sink) (*app.Result, string, error) {
		e.s.mu.Lock()
		seen <- e.s.job.Action
		e.s.mu.Unlock()
		return nil, "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap.Action != "cancel" {
		t.Fatalf("returned job has action %q", snap.Action)
	}
	if got := <-seen; got != "cancel" {
		t.Fatalf("running job has action %q", got)
	}
}
