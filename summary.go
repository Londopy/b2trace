package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

func runSummary(args []string, w io.Writer) error {
	flags := newFlags("summary", "sessions.jsonl")
	flags.Parse(args)
	if flags.NArg() != 1 {
		return errors.New("summary takes one file, for example: b2trace summary sessions.jsonl")
	}
	sessions, err := readSessions(flags.Arg(0))
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Fprintf(w, "no sessions in %s\n", flags.Arg(0))
		return nil
	}
	var all stats
	for i, s := range sessions {
		st := sessionStats(s)
		fmt.Fprintf(w, "session %d: %s\n\n", i+1, describe(s, st))
		st.print(w)
		fmt.Fprintln(w)
		all = all.plus(st)
	}
	fmt.Fprintf(w, "total: %d sessions, %s\n\n", len(sessions), all.duration.Round(100*time.Millisecond))
	all.print(w)
	return nil
}

func readSessions(path string) ([]session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var sessions []session
	sc := bufio.NewScanner(f)
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
	d := fmt.Sprintf("%s, %s, %s", s.Modem, s.Start.Format("2006-01-02 15:04:05"), st.duration.Round(100*time.Millisecond))
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
	turns       int
	turnarounds int
	gaps        int
	gapMs       int64
	ptt         int
	localTXMs   int64
	stalls      int
	msgsOut     int
	msgsIn      int
	errors      int
}

func sessionStats(s session) stats {
	st := stats{
		duration:    s.End.Sub(s.Start),
		out:         s.BytesOut,
		in:          s.BytesIn,
		turns:       s.TurnCount,
		turnarounds: s.Turnarounds,
		gaps:        len(s.GapMs),
		ptt:         len(s.PTT),
		localTXMs:   s.LocalTXMs,
		stalls:      bufferStalls(s.Buffer),
		errors:      len(s.Errors),
	}
	for _, g := range s.GapMs {
		st.gapMs += g
	}
	for _, m := range s.Messages {
		if m.Dir == outbound {
			st.msgsOut++
		} else {
			st.msgsIn++
		}
	}
	return st
}

func (a stats) plus(b stats) stats {
	a.duration += b.duration
	a.out = a.out.plus(b.out)
	a.in = a.in.plus(b.in)
	a.turns += b.turns
	a.turnarounds += b.turnarounds
	a.gaps += b.gaps
	a.gapMs += b.gapMs
	a.ptt += b.ptt
	a.localTXMs += b.localTXMs
	a.stalls += b.stalls
	a.msgsOut += b.msgsOut
	a.msgsIn += b.msgsIn
	a.errors += b.errors
	return a
}

func (st stats) print(w io.Writer) {
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
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-17s %d\n", "turns", st.turns)
	fmt.Fprintf(w, "%-17s %d\n", "turnarounds", st.turnarounds)
	fmt.Fprintf(w, "%-17s %d\n", "PTT intervals", st.ptt)
	fmt.Fprintf(w, "%-17s %.1f s\n", "local TX", float64(st.localTXMs)/1000)
	fmt.Fprintf(w, "%-17s %d\n", "gaps", st.gaps)
	fmt.Fprintf(w, "%-17s %s\n", "mean gap", st.meanGap())
	fmt.Fprintf(w, "%-17s %d (BUFFER non-zero and unchanged for 2+ samples)\n", "possible retries", st.stalls)
	fmt.Fprintf(w, "%-17s %d out, %d in\n", "messages", st.msgsOut, st.msgsIn)
	fmt.Fprintf(w, "%-17s %d\n", "errors", st.errors)
}

func (st stats) meanGap() string {
	if st.gaps == 0 {
		return "-"
	}
	mean := float64(st.gapMs) / float64(st.gaps) / 1000
	return fmt.Sprintf("%.2f s (estimate: local PTT off to next PTT on)", mean)
}

func share(n, total int64) string {
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total))
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
