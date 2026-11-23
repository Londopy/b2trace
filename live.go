package main

import (
	"slices"
	"strings"
	"sync"
	"time"
)

var phaseNames = [...]string{"handshake", "pending", "proposals", "answer", "payload", "end", "error", "other"}

func (p phase) String() string { return phaseNames[p] }

// liveState is what the live view shows: the session in progress, if there
// is one, and the sessions written so far.
type liveState struct {
	// the session in progress, with End set to now; nil between sessions
	cur   *session
	ptt   bool
	phase phase
	dir   direction
	// whether any B2F data has passed yet, so phase and dir mean something
	data bool

	saved     int
	lastSaved *session
	saveErr   error
}

// snapshot copies what the live view needs, so the UI can read it while the
// relay goroutines go on.
func (t *tracker) snapshot(now time.Time) liveState {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := liveState{ptt: t.pttOn, saved: t.saved, lastSaved: t.lastSaved, saveErr: t.saveErr}
	if r := t.active(); r != nil {
		s := r.s
		s.End = stamp(now)
		s.Turns = slices.Clone(s.Turns)
		s.TurnCount = len(s.Turns)
		s.Turnarounds = max(0, s.TurnCount-1)
		s.PTT = slices.Clone(s.PTT)
		s.GapMs = slices.Clone(s.GapMs)
		s.Buffer = slices.Clone(s.Buffer)
		s.Proposals = slices.Clone(s.Proposals)
		s.Messages = slices.Clone(s.Messages)
		s.Errors = slices.Clone(s.Errors)
		// the PTT interval still open counts too
		if !r.txStart.IsZero() {
			s.LocalTXMs += now.Sub(r.txStart).Milliseconds()
		}
		st.cur = &s
		st.phase, st.dir, st.data = r.lastPhase, r.lastDir, r.sawData
	}
	return st
}

// lineLog keeps the last lines written to it, for the live view's log.
type lineLog struct {
	mu    sync.Mutex
	lines []string
	part  []byte
	keep  int
}

func newLineLog(keep int) *lineLog { return &lineLog{keep: keep} }

func (l *lineLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.part = append(l.part, p...)
	for {
		i := slices.Index(l.part, '\n')
		if i < 0 {
			break
		}
		l.lines = append(l.lines, strings.TrimRight(string(l.part[:i]), "\r"))
		l.part = l.part[i+1:]
	}
	if len(l.lines) > l.keep {
		l.lines = slices.Clone(l.lines[len(l.lines)-l.keep:])
	}
	return len(p), nil
}

// last returns up to n of the most recent lines, oldest first.
func (l *lineLog) last(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines[max(0, len(l.lines)-n):])
}
