// Package progress carries build/download events from the core pipeline to
// whichever front end is running it (console CLI or the GUI's event stream).
package progress

import (
	"fmt"
	"io"
	"sync"
)

type Level string

const (
	Info  Level = "info"
	OK    Level = "ok"
	Warn  Level = "warn"
	Error Level = "error"
)

type Kind string

const (
	KindLog      Kind = "log"
	KindStage    Kind = "stage"
	KindProgress Kind = "progress"
	KindStep     Kind = "step" // Done of Total build steps finished
)

// Event is one pipeline event. Progress events carry Done/Total bytes for the
// item named in Msg; stage events name the current step in Stage.
type Event struct {
	Kind  Kind   `json:"kind"`
	Level Level  `json:"level,omitempty"`
	Stage string `json:"stage,omitempty"`
	Msg   string `json:"msg,omitempty"`
	Done  int64  `json:"done,omitempty"`
	Total int64  `json:"total,omitempty"`
}

// Sink receives events. A nil Sink discards everything.
type Sink func(Event)

func (s Sink) emit(e Event) {
	if s != nil {
		s(e)
	}
}

func (s Sink) Logf(level Level, format string, args ...any) {
	s.emit(Event{Kind: KindLog, Level: level, Msg: fmt.Sprintf(format, args...)})
}
func (s Sink) Infof(format string, args ...any) { s.Logf(Info, format, args...) }
func (s Sink) OKf(format string, args ...any)   { s.Logf(OK, format, args...) }
func (s Sink) Warnf(format string, args ...any) { s.Logf(Warn, format, args...) }

// Stage announces the start of a named step ("download", "build", "pack"...).
func (s Sink) Stage(stage, msg string) {
	s.emit(Event{Kind: KindStage, Stage: stage, Msg: msg})
}

// Progress reports byte progress for one item.
func (s Sink) Progress(item string, done, total int64) {
	s.emit(Event{Kind: KindProgress, Msg: item, Done: done, Total: total})
}

// Step reports that done of total build steps are finished.
func (s Sink) Step(done, total int) {
	s.emit(Event{Kind: KindStep, Done: int64(done), Total: int64(total)})
}

// Console renders events the way the original CLI printed them:
// "========> message" lines plus an in-place percentage for downloads.
func Console(w io.Writer) Sink {
	var mu sync.Mutex
	inProgress := false
	lastPct := map[string]int64{}
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Kind {
		case KindStep:
			return
		case KindProgress:
			if e.Total <= 0 {
				return
			}
			pct := e.Done * 100 / e.Total
			if last, ok := lastPct[e.Msg]; ok && pct < last+10 && e.Done != e.Total {
				return
			}
			lastPct[e.Msg] = pct
			fmt.Fprintf(w, "\r           %-28s %3d%%", e.Msg, pct)
			inProgress = e.Done != e.Total
			if !inProgress {
				fmt.Fprintln(w)
			}
		case KindStage:
			if e.Msg != "" {
				if inProgress {
					fmt.Fprintln(w)
					inProgress = false
				}
				fmt.Fprintln(w, "========>", e.Msg)
			}
		default:
			if inProgress {
				fmt.Fprintln(w)
				inProgress = false
			}
			prefix := "========>"
			switch e.Level {
			case Warn:
				prefix = "WARNING:"
			case Error:
				prefix = "ERROR:"
			}
			fmt.Fprintln(w, prefix, e.Msg)
		}
	}
}

// Tee fans one event out to several sinks.
func Tee(sinks ...Sink) Sink {
	return func(e Event) {
		for _, s := range sinks {
			s.emit(e)
		}
	}
}
