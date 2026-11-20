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
	link  string
	log   *log.Logger
	save  func(*session) error

	// The modem reports DISCONNECTED on the command port while the last data
	// can still be on its way over the data port, so a session stays open
	// this long for late data.
	grace time.Duration

	mycalls []string
	target  string
	pttOn   bool
	dialAt  time.Time

	rec *recorder
	// The AGWPE connection whose "C" frame started rec. Other programs can
	// share the TNC, and their frames are not part of the session. Always 0
	// for VARA and ARDOP.
	owner int
	// command lines since the last session, kept for the next one
	before []pendingLine
}

type pendingLine struct {
	conn int
	c    commandLine
}

func newTracker(modem, link string, logger *log.Logger, save func(*session) error) *tracker {
	return &tracker{modem: modem, link: link, log: logger, save: save, grace: 2 * time.Second}
}

// command handles one line from the command port, or one AGWPE frame
// written out as a line by agwFrame.line. conn tells AGWPE connections
// apart and is 0 for VARA and ARDOP.
func (t *tracker) command(conn int, dir direction, line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	f := strings.Fields(line)
	buffer := t.isBuffer(f)
	// Pat polls AGWPE for the frame count several times a second.
	if !buffer || t.modem != "agwpe" {
		who := "host "
		if dir == inbound {
			who = "modem"
		}
		t.log.Printf("%s %s", who, line)
	}
	if t.foreign(conn) {
		return
	}
	if dir == outbound {
		t.hostLine(now, f)
	} else {
		t.modemLine(now, conn, line, f)
	}
	// After the line is handled, so CONNECTED goes with the session it
	// starts and DISCONNECTED with the one it ends.
	if !buffer {
		t.note(now, conn, dir, line)
	}
}

// foreign reports whether conn belongs to another program on the same AGWPE
// TNC while a session is open.
func (t *tracker) foreign(conn int) bool { return t.rec != nil && conn != t.owner }

// isBuffer reports whether a line is a buffer report or query. Those come
// often and are kept in the session's buffer samples instead.
func (t *tracker) isBuffer(f []string) bool {
	if t.modem == "agwpe" {
		return f[0] == "Y" || f[0] == "y"
	}
	return strings.EqualFold(f[0], "BUFFER")
}

func (t *tracker) note(now time.Time, conn int, dir direction, line string) {
	c := commandLine{T: stamp(now), Dir: dir, Line: line}
	// Once the session has ended, what the host sends is for the next one.
	if t.rec != nil && !(t.rec.ended && dir == outbound) {
		t.rec.s.Commands = append(t.rec.s.Commands, c)
		return
	}
	t.before = append(t.before, pendingLine{conn, c})
	// enough for the setup and the connect attempts before a session
	if len(t.before) > 50 {
		t.before = t.before[len(t.before)-50:]
	}
}

func (t *tracker) data(conn int, dir direction, b []byte) {
	if len(b) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rec != nil && !t.foreign(conn) {
		t.rec.data(dir, time.Now(), b)
	}
}

func (t *tracker) connClosed(conn int, what string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.before = slices.DeleteFunc(t.before, func(l pendingLine) bool { return l.conn == conn && conn != 0 })
	if t.foreign(conn) {
		return
	}
	t.end(time.Now(), what+" closed")
}

func (t *tracker) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.end(time.Now(), "proxy stopped")
}

func (t *tracker) hostLine(now time.Time, f []string) {
	if t.modem == "agwpe" {
		switch f[0] {
		case "X":
			if len(f) > 2 && !t.isMine(f[2]) {
				t.mycalls = append(t.mycalls, f[2])
			}
		case "C", "v":
			t.dialAt = now
		case "d":
			t.dialAt = time.Time{}
		}
		return
	}
	switch strings.ToUpper(f[0]) {
	case "MYCALL":
		t.mycalls = f[1:]
	case "CONNECT", "ARQCALL":
		t.dialAt = now
	// a dial given up on, which a TNC may never answer
	case "DISCONNECT", "ABORT":
		t.dialAt = time.Time{}
	}
}

func (t *tracker) modemLine(now time.Time, conn int, line string, f []string) {
	if t.modem == "agwpe" {
		t.agwLine(now, conn, line, f)
		return
	}
	f = strings.Fields(strings.ToUpper(line))
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
		if len(f) > 1 {
			t.buffer(now, f[1])
		}
	case "CONNECTED":
		t.connected(now, 0, line, f)
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

// agwLine handles an AGWPE frame from the TNC: "C" is a connection, "d"
// its end, "Y" the frames still waiting to be acknowledged on it.
func (t *tracker) agwLine(now time.Time, conn int, line string, f []string) {
	switch f[0] {
	case "C":
		if len(f) > 3 {
			t.connected(now, conn, line, []string{"C", strings.ToUpper(f[2]), strings.ToUpper(f[3])})
		}
	case "d":
		t.disconnected(now)
	case "Y":
		if len(f) > 4 {
			t.buffer(now, f[4])
		}
	}
}

func (t *tracker) buffer(now time.Time, value string) {
	r := t.active()
	if r == nil {
		return
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return
	}
	// polled, so only the changes say anything
	if b := r.s.Buffer; t.modem == "agwpe" && len(b) > 0 && b[len(b)-1].Bytes == n {
		return
	}
	r.s.Buffer = append(r.s.Buffer, bufferSample{T: stamp(now), Bytes: n})
}

func (t *tracker) connected(now time.Time, conn int, line string, f []string) {
	t.end(now, "new CONNECTED")
	t.owner = conn
	r := newRecorder(t.modem, now, line)
	r.s.Link = t.link
	r.s.LocalCall, r.s.RemoteCall = t.calls(f)
	if !t.dialAt.IsZero() {
		r.s.ConnectMs = now.Sub(t.dialAt).Milliseconds()
		t.dialAt = time.Time{}
	}
	for _, l := range t.before {
		if l.conn == conn {
			r.s.Commands = append(r.s.Commands, l.c)
		}
	}
	t.before = nil
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
	// a failed connect attempt ends here too
	t.dialAt = time.Time{}
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
	// proposals sent each way that the other side has not answered yet
	waiting [2][]int
	txStart time.Time
	lastOff time.Time

	// ended is set at DISCONNECTED. Late data is still added after that, but
	// command-port events are not.
	ended bool
}

func newRecorder(modem string, now time.Time, line string) *recorder {
	r := &recorder{s: session{
		Modem:       modem,
		Start:       stamp(now),
		ConnectLine: line,
		Turns:       []turn{},
		PTT:         []pttInterval{},
		GapMs:       []int64{},
		Buffer:      []bufferSample{},
		States:      []stateLine{},
		Commands:    []commandLine{},
		Proposals:   []proposal{},
		Messages:    []message{},
		Errors:      []streamError{},
	}}
	if modem == "agwpe" {
		r.s.BufferUnit = "frames"
	}
	return r
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
		r.line(dir, e)
	}
}

// line keeps the SIDs, the proposals and the answers to them.
func (r *recorder) line(dir direction, e element) {
	switch {
	case e.phase == phaseHandshake && isSID(e.line):
		sid := &r.s.SIDOut
		if dir == inbound {
			sid = &r.s.SIDIn
		}
		if *sid == "" {
			*sid = e.line
		}
	case e.phase == phaseProposals && e.line[1] != '>':
		p := parseProposal(e.line)
		p.Dir = dir
		r.waiting[dir] = append(r.waiting[dir], len(r.s.Proposals))
		r.s.Proposals = append(r.s.Proposals, p)
	case e.phase == phaseAnswer:
		other := 1 - dir
		for i, a := range parseAnswers(e.line) {
			if i >= len(r.waiting[other]) {
				break
			}
			p := &r.s.Proposals[r.waiting[other][i]]
			p.Answer, p.Offset = a.kind, a.offset
		}
		r.waiting[other] = nil
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
