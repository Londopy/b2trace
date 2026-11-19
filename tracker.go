package main

import (
	"cmp"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tracker turns what the proxy sees on the command and data ports into
// sessions. All its methods are safe to call from the relay goroutines.
type tracker struct {
	mu    sync.Mutex
	modem string
	log   *log.Logger
	save  func(*session) error

	// The modem reports DISCONNECTED on the command port while the last data
	// can still be on its way over the data port, so a session stays open
	// this long for late data.
	grace time.Duration

	mycalls []string
	target  string
	pttOn   bool

	rec *recorder
}

func newTracker(modem string, logger *log.Logger, save func(*session) error) *tracker {
	return &tracker{modem: modem, log: logger, save: save, grace: 2 * time.Second}
}

// command handles one line from the command port.
func (t *tracker) command(dir direction, line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if dir == outbound {
		t.log.Printf("host  %s", line)
		t.hostLine(line)
		return
	}
	t.log.Printf("modem %s", line)
	t.modemLine(time.Now(), line)
}

func (t *tracker) data(dir direction, b []byte) {
	if len(b) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rec != nil {
		t.rec.data(dir, time.Now(), b)
	}
}

func (t *tracker) connClosed(what string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.end(time.Now(), what+" closed")
}

func (t *tracker) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.end(time.Now(), "proxy stopped")
}

func (t *tracker) hostLine(line string) {
	f := strings.Fields(line)
	if len(f) > 1 && strings.EqualFold(f[0], "MYCALL") {
		t.mycalls = f[1:]
	}
}

func (t *tracker) modemLine(now time.Time, line string) {
	f := strings.Fields(strings.ToUpper(line))
	switch f[0] {
	case "PTT":
		if len(f) > 1 {
			switch f[1] {
			case "ON", "TRUE":
				t.ptt(now, true)
			case "OFF", "FALSE":
				t.ptt(now, false)
			}
		}
	case "BUFFER":
		t.buffer(now, f)
	case "CONNECTED":
		t.connected(now, line, f)
	case "DISCONNECTED":
		t.disconnected(now)
	case "NEWSTATE", "STATUS":
		if r := t.active(); r != nil {
			r.s.States = append(r.s.States, stateLine{T: stamp(now), Line: line})
		}
		if f[0] == "NEWSTATE" && len(f) > 1 && f[1] == "DISC" {
			t.disconnected(now)
		}
	case "TARGET":
		if len(f) > 1 {
			t.target = f[1]
		}
	}
}

// active returns the session that command-port events still belong to.
func (t *tracker) active() *recorder {
	if t.rec == nil || t.rec.ended {
		return nil
	}
	return t.rec
}

func (t *tracker) ptt(now time.Time, on bool) {
	if on == t.pttOn {
		return
	}
	t.pttOn = on
	r := t.active()
	switch {
	case r == nil:
	case on:
		r.txOn(now)
	default:
		r.txOff(now)
	}
}

func (t *tracker) buffer(now time.Time, f []string) {
	r := t.active()
	if r == nil || len(f) < 2 {
		return
	}
	n, err := strconv.Atoi(f[1])
	if err != nil {
		return
	}
	r.s.Buffer = append(r.s.Buffer, bufferSample{T: stamp(now), Bytes: n})
}

func (t *tracker) connected(now time.Time, line string, f []string) {
	t.end(now, "new CONNECTED")
	r := newRecorder(t.modem, now, line)
	r.s.LocalCall, r.s.RemoteCall = t.calls(f)
	if t.pttOn {
		r.txOn(now)
	}
	t.rec = r
}

// calls works out the local and remote call from a CONNECTED line. VARA
// reports "CONNECTED <source> <destination> ...", ARDOP only the remote
// call, "CONNECTED <remote> <bandwidth>".
func (t *tracker) calls(f []string) (local, remote string) {
	if t.modem == "ardop" {
		if len(f) > 1 {
			remote = f[1]
		}
		local, t.target = t.target, ""
		if local == "" && len(t.mycalls) > 0 {
			local = strings.ToUpper(t.mycalls[0])
		}
		return local, remote
	}
	if len(f) < 3 {
		return "", ""
	}
	src, dst := f[1], f[2]
	switch {
	case t.isMine(src):
		return src, dst
	case t.isMine(dst):
		return dst, src
	}
	return "", ""
}

func (t *tracker) isMine(call string) bool {
	return slices.ContainsFunc(t.mycalls, func(c string) bool { return strings.EqualFold(c, call) })
}

func (t *tracker) disconnected(now time.Time) {
	r := t.active()
	if r == nil {
		return
	}
	r.close(now, "disconnected")
	time.AfterFunc(t.grace, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.rec == r {
			t.finish()
		}
	})
}

// end closes the current session, if there is one, and writes it out.
func (t *tracker) end(now time.Time, reason string) {
	if t.rec == nil {
		return
	}
	if !t.rec.ended {
		t.rec.close(now, reason)
	}
	t.finish()
}

func (t *tracker) finish() {
	r := t.rec
	t.rec = nil
	r.flush()
	s := &r.s
	if err := t.save(s); err != nil {
		t.log.Printf("cannot write session: %v", err)
		return
	}
	t.log.Printf("session ended (%s): remote %s, bytes out %d, bytes in %d, turns %d, local TX %.1f s",
		s.EndReason, cmp.Or(s.RemoteCall, "unknown"), s.BytesOut.total(), s.BytesIn.total(),
		s.TurnCount, float64(s.LocalTXMs)/1000)
}

// recorder builds one session.
type recorder struct {
	s       session
	parsers [2]parser
	txStart time.Time
	lastOff time.Time

	// ended is set at DISCONNECTED. Late data is still added after that, but
	// command-port events are not.
	ended bool
}

func newRecorder(modem string, now time.Time, line string) *recorder {
	return &recorder{s: session{
		Modem:       modem,
		Start:       stamp(now),
		ConnectLine: line,
		Turns:       []turn{},
		PTT:         []pttInterval{},
		GapMs:       []int64{},
		Buffer:      []bufferSample{},
		States:      []stateLine{},
		Messages:    []message{},
		Errors:      []streamError{},
	}}
}

func (r *recorder) data(dir direction, now time.Time, b []byte) {
	if n := len(r.s.Turns); n == 0 || r.s.Turns[n-1].Dir != dir {
		r.s.Turns = append(r.s.Turns, turn{Dir: dir, FirstByte: stamp(now)})
	}
	tn := &r.s.Turns[len(r.s.Turns)-1]
	tn.LastByte = stamp(now)
	r.add(dir, tn, now, r.parsers[dir].write(b))
}

func (r *recorder) add(dir direction, tn *turn, now time.Time, els []element) {
	total := &r.s.BytesOut
	if dir == inbound {
		total = &r.s.BytesIn
	}
	for _, e := range els {
		tn.Bytes.add(e.phase, e.bytes)
		total.add(e.phase, e.bytes)
		if e.msg != nil {
			m := *e.msg
			m.Dir = dir
			r.s.Messages = append(r.s.Messages, m)
		}
		if e.err != "" {
			r.s.Errors = append(r.s.Errors, streamError{T: stamp(now), Dir: dir, Text: e.err})
		}
	}
}

func (r *recorder) txOn(now time.Time) {
	if !r.lastOff.IsZero() {
		r.s.GapMs = append(r.s.GapMs, now.Sub(r.lastOff).Milliseconds())
	}
	r.txStart = now
}

func (r *recorder) txOff(now time.Time) {
	if r.txStart.IsZero() {
		return
	}
	ms := now.Sub(r.txStart).Milliseconds()
	r.s.PTT = append(r.s.PTT, pttInterval{On: stamp(r.txStart), Off: stamp(now), LocalTXMs: ms})
	r.s.LocalTXMs += ms
	r.txStart = time.Time{}
	r.lastOff = now
}

func (r *recorder) close(now time.Time, reason string) {
	r.ended = true
	r.s.End = stamp(now)
	r.s.EndReason = reason
	r.txOff(now)
}

// flush counts what the end of the session cut off, in the last turn of each
// direction, since that is where those bytes arrived.
func (r *recorder) flush() {
	for _, dir := range []direction{outbound, inbound} {
		if els := r.parsers[dir].flush(); len(els) > 0 {
			r.add(dir, r.lastTurn(dir), r.s.End, els)
		}
	}
	r.s.TurnCount = len(r.s.Turns)
	r.s.Turnarounds = max(0, r.s.TurnCount-1)
}

// lastTurn returns the last turn in direction dir. A parser only holds part
// of an element if data came that way, so flush always finds one.
func (r *recorder) lastTurn(dir direction) *turn {
	for i := len(r.s.Turns) - 1; i >= 0; i-- {
		if r.s.Turns[i].Dir == dir {
			return &r.s.Turns[i]
		}
	}
	return nil
}
