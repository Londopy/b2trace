package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runSummary(args []string, w io.Writer) error {
	flags := newFlags("summary", "sessions.jsonl...")
	flags.Parse(args)
	if flags.NArg() == 0 {
		return errors.New("summary takes one or more files, for example: b2trace summary sessions.jsonl")
	}
	sessions, err := readAll(flags.Args())
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Fprintf(w, "no sessions in %s\n", strings.Join(flags.Args(), ", "))
		return nil
	}
	var all stats
	for i, s := range sessions {
		all = all.plus(printSession(w, fmt.Sprintf("session %d", i+1), s))
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "total: %d sessions, %s\n\n", len(sessions), all.duration.Round(100*time.Millisecond))
	all.print(w)
	return nil
}

// printSession writes one session the way summary shows it, under the given
// name, and returns its stats.
func printSession(w io.Writer, name string, s session) stats {
	st := sessionStats(s)
	fmt.Fprintf(w, "%s: %s\n", name, describe(s, st))
	if s.SIDOut != "" {
		fmt.Fprintf(w, "sid out %s\n", s.SIDOut)
	}
	if s.SIDIn != "" {
		fmt.Fprintf(w, "sid in  %s\n", s.SIDIn)
	}
	fmt.Fprintln(w)
	st.print(w)
	return st
}

func readAll(args []string) ([]session, error) {
	paths, err := expand(args)
	if err != nil {
		return nil, err
	}
	var all []session
	for _, p := range paths {
		s, err := readSessions(p)
		if err != nil {
			return nil, err
		}
		all = append(all, s...)
	}
	return all, nil
}

// expand does the globbing that PowerShell and cmd leave to the program, so
// "results/*.jsonl" works there too.
func expand(args []string) ([]string, error) {
	var paths []string
	for _, a := range args {
		if !strings.ContainsAny(a, "*?[") {
			paths = append(paths, a)
			continue
		}
		m, err := filepath.Glob(a)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", a, err)
		}
		if len(m) == 0 {
			return nil, fmt.Errorf("no files match %s", a)
		}
		paths = append(paths, m...)
	}
	return paths, nil
}

func readSessions(path string) ([]session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	if bom, _ := br.Peek(2); bytes.Equal(bom, []byte{0xff, 0xfe}) || bytes.Equal(bom, []byte{0xfe, 0xff}) {
		return nil, fmt.Errorf("%s is UTF-16, which is what > writes in Windows PowerShell 5; make it with b2trace share -o instead", path)
	}
	var sessions []session
	sc := bufio.NewScanner(br)
	// A long session with many BUFFER samples is one long line.
	sc.Buffer(nil, 64<<20)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var s session
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		sessions = append(sessions, s)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return sessions, nil
}

func describe(s session, st stats) string {
	d := s.Modem
	if s.Link != "" {
		d += " (" + s.Link + ")"
	}
	d += fmt.Sprintf(", %s, %s", s.Start.Format("2006-01-02 15:04:05"), st.duration.Round(100*time.Millisecond))
	if s.LocalCall != "" {
		d += ", local " + s.LocalCall
	}
	if s.RemoteCall != "" {
		d += ", remote " + s.RemoteCall
	}
	return d + ", ended: " + s.EndReason
}

// stats is what the summary prints, for one session or for all of them.
type stats struct {
	duration    time.Duration
	out, in     phaseBytes
	time        phaseTimes
	turns       int
	turnarounds int
	gaps        int
	gapMs       int64
	// how long Pat took to answer, see replyTimes
	replies   int
	replyMs   float64
	ptt       int
	localTXMs int64
	stalls    int
	// false when no session came from a modem with BUFFER reports
	retryInfo bool
	msgsOut   int
	msgsIn    int
	sent      propStats
	received  propStats
	errors    int
}

// phaseTimes is how long a session spent on each phase, in milliseconds.
// See turnTimes for how it is worked out.
type phaseTimes struct {
	connect, setup, disconnect float64
	phase                      [phaseOther + 1]float64
}

func (a phaseTimes) plus(b phaseTimes) phaseTimes {
	a.connect += b.connect
	a.setup += b.setup
	a.disconnect += b.disconnect
	for p := range a.phase {
		a.phase[p] += b.phase[p]
	}
	return a
}

// propStats counts the proposals sent one way and how they were answered.
type propStats struct {
	codes                                         [5]int // FA to FE
	accept, reject, deferred, resume, other, none int
	optimistic                                    int
}

func (a propStats) plus(b propStats) propStats {
	for i := range a.codes {
		a.codes[i] += b.codes[i]
	}
	a.accept += b.accept
	a.reject += b.reject
	a.deferred += b.deferred
	a.resume += b.resume
	a.other += b.other
	a.none += b.none
	a.optimistic += b.optimistic
	return a
}

func (ps *propStats) add(p proposal) {
	if len(p.Code) == 2 && p.Code[1] >= 'A' && p.Code[1] <= 'E' {
		ps.codes[p.Code[1]-'A']++
	}
	if p.Optimistic {
		ps.optimistic++
	}
	switch p.Answer {
	case "accept":
		ps.accept++
	case "reject":
		ps.reject++
	case "defer":
		ps.deferred++
	case "resume":
		ps.resume++
	case "other":
		ps.other++
	default:
		ps.none++
	}
}

func (ps propStats) String() string {
	total := 0
	var codes []string
	for i, n := range ps.codes {
		total += n
		if n > 0 {
			codes = append(codes, fmt.Sprintf("F%c %d", 'A'+i, n))
		}
	}
	if total == 0 {
		return "0"
	}
	var answers []string
	for _, a := range []struct {
		n    int
		name string
	}{{ps.accept, "accepted"}, {ps.resume, "resumed"}, {ps.deferred, "deferred"},
		{ps.reject, "rejected"}, {ps.other, "answered otherwise"}, {ps.none, "not answered"}, {ps.optimistic, "sent optimistically"}} {
		if a.n > 0 {
			answers = append(answers, fmt.Sprintf("%d %s", a.n, a.name))
		}
	}
	return fmt.Sprintf("%d (%s): %s", total, strings.Join(codes, ", "), strings.Join(answers, ", "))
}

func sessionStats(s session) stats {
	st := stats{
		duration:    s.End.Sub(s.Start),
		out:         s.BytesOut,
		in:          s.BytesIn,
		time:        turnTimes(s),
		turns:       s.TurnCount,
		turnarounds: s.Turnarounds,
		gaps:        len(s.GapMs),
		ptt:         len(s.PTT),
		localTXMs:   s.LocalTXMs,
		stalls:      bufferStalls(s.Buffer),
		retryInfo:   s.Modem != "agwpe",
		errors:      len(s.Errors),
	}
	for _, g := range s.GapMs {
		st.gapMs += g
	}
	for _, d := range replyTimes(s) {
		st.replies++
		st.replyMs += d
	}
	for _, m := range s.Messages {
		if m.Dir == outbound {
			st.msgsOut++
		} else {
			st.msgsIn++
		}
	}
	for _, p := range s.Proposals {
		if p.Dir == outbound {
			st.sent.add(p)
		} else {
			st.received.add(p)
		}
	}
	return st
}

// turnTimes splits a session's time between the B2F phases. A turn lasts
// from its first byte to the first byte of the next turn, so it takes in the
// turnaround and the start of the reply, and its time is shared between its
// phases by bytes. Setup is the time from CONNECTED to the first byte,
// disconnect from the last byte to the end.
func turnTimes(s session) phaseTimes {
	pt := phaseTimes{connect: float64(s.ConnectMs)}
	if len(s.Turns) == 0 {
		pt.setup = ms(s.End.Sub(s.Start))
		return pt
	}
	pt.setup = ms(s.Turns[0].FirstByte.Sub(s.Start))
	for i, tn := range s.Turns {
		end := tn.LastByte
		if i+1 < len(s.Turns) {
			end = s.Turns[i+1].FirstByte
		}
		d, total := ms(end.Sub(tn.FirstByte)), tn.Bytes.total()
		if total == 0 {
			continue
		}
		for p := phaseHandshake; p <= phaseOther; p++ {
			pt.phase[p] += d * float64(*tn.Bytes.field(p)) / float64(total)
		}
	}
	pt.disconnect = ms(s.End.Sub(s.Turns[len(s.Turns)-1].LastByte))
	return pt
}

// ms is d in milliseconds, or 0 for the small negative spans that late data
// after DISCONNECTED gives.
func ms(d time.Duration) float64 { return max(0, float64(d)/float64(time.Millisecond)) }

func (a stats) plus(b stats) stats {
	a.duration += b.duration
	a.out = a.out.plus(b.out)
	a.in = a.in.plus(b.in)
	a.time = a.time.plus(b.time)
	a.turns += b.turns
	a.turnarounds += b.turnarounds
	a.gaps += b.gaps
	a.gapMs += b.gapMs
	a.replies += b.replies
	a.replyMs += b.replyMs
	a.ptt += b.ptt
	a.localTXMs += b.localTXMs
	a.stalls += b.stalls
	a.retryInfo = a.retryInfo || b.retryInfo
	a.msgsOut += b.msgsOut
	a.msgsIn += b.msgsIn
	a.sent = a.sent.plus(b.sent)
	a.received = a.received.plus(b.received)
	a.errors += b.errors
	return a
}

func (st stats) print(w io.Writer) {
	st.printBytes(w)
	fmt.Fprintln(w)
	st.time.print(w, "disconnect")
	fmt.Fprintln(w)
	st.printCounts(w)
}

func (st stats) printBytes(w io.Writer) {
	o, i := st.out, st.in
	rows := []struct {
		name    string
		out, in int64
	}{
		{"handshake", o.Handshake, i.Handshake},
		{"proposals", o.Pending + o.Proposals, i.Pending + i.Proposals},
		{"payload", o.Payload, i.Payload},
		{"answers/end", o.Answer + o.End, i.Answer + i.End},
		{"other", o.Error + o.Other, i.Error + i.Other},
		{"total", o.total(), i.total()},
	}
	all := o.total() + i.total()
	fmt.Fprintf(w, "%-12s %9s %9s %9s %7s\n", "bytes", "out", "in", "both", "share")
	for _, r := range rows {
		fmt.Fprintf(w, "%-12s %9d %9d %9d %7s\n", r.name, r.out, r.in, r.out+r.in, share(r.out+r.in, all))
	}
}

func (st stats) printCounts(w io.Writer) {
	fmt.Fprintf(w, "%-17s %d\n", "turns", st.turns)
	fmt.Fprintf(w, "%-17s %d\n", "turnarounds", st.turnarounds)
	fmt.Fprintf(w, "%-17s %d\n", "PTT intervals", st.ptt)
	fmt.Fprintf(w, "%-17s %.1f s\n", "local TX", float64(st.localTXMs)/1000)
	fmt.Fprintf(w, "%-17s %d\n", "gaps", st.gaps)
	fmt.Fprintf(w, "%-17s %s\n", "mean gap", st.meanGap())
	fmt.Fprintf(w, "%-17s %s\n", "reply time", st.meanReply())
	if st.retryInfo {
		fmt.Fprintf(w, "%-17s %d (BUFFER non-zero and unchanged for 2+ samples)\n", "possible retries", st.stalls)
	} else {
		fmt.Fprintf(w, "%-17s - (AGWPE does not report them)\n", "possible retries")
	}
	fmt.Fprintf(w, "%-17s %s\n", "proposals out", st.sent)
	fmt.Fprintf(w, "%-17s %s\n", "proposals in", st.received)
	fmt.Fprintf(w, "%-17s %d out, %d in\n", "messages", st.msgsOut, st.msgsIn)
	fmt.Fprintf(w, "%-17s %d\n", "errors", st.errors)
}

// print writes the time table. last names the time after the last byte.
func (pt phaseTimes) print(w io.Writer, last string) {
	p := pt.phase
	rows := []struct {
		name string
		ms   float64
	}{
		{"connect", pt.connect},
		{"setup", pt.setup},
		{"handshake", p[phaseHandshake]},
		{"proposals", p[phasePending] + p[phaseProposals]},
		{"payload", p[phasePayload]},
		{"answers/end", p[phaseAnswer] + p[phaseEnd]},
		{"other", p[phaseError] + p[phaseOther]},
		{last, pt.disconnect},
	}
	total := pt.total()
	fmt.Fprintf(w, "%-12s %9s %7s  (estimate, see README)\n", "time", "seconds", "share")
	for _, r := range rows {
		fmt.Fprintf(w, "%-12s %9.1f %7s\n", r.name, r.ms/1000, shareF(r.ms, total))
	}
	fmt.Fprintf(w, "%-12s %9.1f %7s\n", "total", total/1000, shareF(total, total))
}

func (pt phaseTimes) total() float64 {
	t := pt.connect + pt.setup + pt.disconnect
	for _, p := range pt.phase {
		t += p
	}
	return t
}

// replyTimes lists, in milliseconds, how long Pat took to answer each remote
// turn: from the remote's last byte reaching Pat to Pat's first byte of the
// next turn, both as they passed the proxy. The modem's decoding before and
// its keying after are not in it.
func replyTimes(s session) []float64 {
	var d []float64
	for i := 1; i < len(s.Turns); i++ {
		prev, next := s.Turns[i-1], s.Turns[i]
		if prev.Dir == inbound && next.Dir == outbound {
			d = append(d, ms(next.FirstByte.Sub(prev.LastByte)))
		}
	}
	return d
}

func (st stats) meanReply() string {
	if st.replies == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f s (mean, remote's last byte to your next byte)", st.replyMs/float64(st.replies)/1000)
}

func (st stats) meanGap() string {
	if st.gaps == 0 {
		return "-"
	}
	mean := float64(st.gapMs) / float64(st.gaps) / 1000
	return fmt.Sprintf("%.2f s (estimate: local PTT off to next PTT on)", mean)
}

func share(n, total int64) string { return shareF(float64(n), float64(total)) }

func shareF(n, total float64) string {
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", 100*n/total)
}

// bufferStalls counts runs of two or more BUFFER samples with the same
// non-zero value: data was queued and none of it got acknowledged.
func bufferStalls(samples []bufferSample) int {
	n := 0
	for i := 1; i < len(samples); i++ {
		v := samples[i].Bytes
		if v != 0 && v == samples[i-1].Bytes && (i == 1 || samples[i-2].Bytes != v) {
			n++
		}
	}
	return n
}
