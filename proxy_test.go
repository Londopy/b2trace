package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// listenAdjacent listens on two free ports next to each other, the way a
// modem uses them.
func listenAdjacent(t *testing.T) (net.Listener, net.Listener) {
	t.Helper()
	for range 50 {
		a, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		if next, err := nextPort(a.Addr().String()); err == nil {
			if b, err := net.Listen("tcp", next); err == nil {
				return a, b
			}
		}
		a.Close()
	}
	t.Fatal("found no two free adjacent ports")
	return nil, nil
}

// peer is one end of a connection through the proxy: Pat's or the modem's.
type peer struct {
	t *testing.T
	c net.Conn
}

func dialPeer(t *testing.T, addr string) peer {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	t.Cleanup(func() { c.Close() })
	return peer{t, c}
}

func acceptPeer(t *testing.T, ln net.Listener) peer {
	t.Helper()
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	t.Cleanup(func() { c.Close() })
	return peer{t, c}
}

// relay sends b from one end and checks that it arrives unchanged at the
// other. Once it has arrived, the proxy has handled it.
func relay(from, to peer, b []byte) {
	from.t.Helper()
	if _, err := from.c.Write(b); err != nil {
		from.t.Fatal(err)
	}
	got := make([]byte, len(b))
	if _, err := io.ReadFull(to.c, got); err != nil {
		to.t.Fatalf("waiting for %q: %v", b, err)
	}
	if !bytes.Equal(got, b) {
		to.t.Fatalf("got %q, want %q", got, b)
	}
}

func waitForLines(t *testing.T, path string, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(b, []byte("\n")) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s has %q, want %d lines", path, b, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type modemCase struct {
	modem           string
	connected       string
	pttOn, pttOff   string
	afterConnect    []string
	afterDisconnect []string
	toModem         func([]byte) []byte
	toHost          func([]byte) []byte
	noise           []byte
}

func TestProxySession(t *testing.T) {
	same := func(b []byte) []byte { return b }
	cases := []modemCase{{
		modem:     "vara",
		connected: "CONNECTED N0CALL LA1B 2300",
		pttOn:     "PTT ON",
		pttOff:    "PTT OFF",
		toModem:   same,
		toHost:    same,
	}, {
		modem:           "ardop",
		connected:       "CONNECTED LA1B 500",
		pttOn:           "PTT TRUE",
		pttOff:          "PTT FALSE",
		afterConnect:    []string{"NEWSTATE IRS"},
		afterDisconnect: []string{"NEWSTATE DISC"},
		toModem:         hostFrame,
		toHost:          func(b []byte) []byte { return tncFrame("ARQ", b) },
		noise:           tncFrame("IDF", []byte(" LA1B:[JP20QE] ")),
	}}
	for _, mc := range cases {
		t.Run(mc.modem, func(t *testing.T) { testProxySession(t, mc) })
	}
}

// rig is a proxy between a fake Pat and a fake modem, both played by the test.
type rig struct {
	patCmd, patData, modemCmd, modemData peer
	logPath                              string
	stop                                 func() error
}

func newRig(t *testing.T, modem string) *rig {
	t.Helper()
	modemCmdLn, modemDataLn := listenAdjacent(t)
	t.Cleanup(func() { modemCmdLn.Close(); modemDataLn.Close() })
	proxyCmdLn, proxyDataLn := listenAdjacent(t)

	r := &rig{logPath: filepath.Join(t.TempDir(), "sessions.jsonl")}
	f, err := os.Create(r.logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	p, err := newProxy(modem, modemCmdLn.Addr().String(), log.New(io.Discard, "", 0), appendSession(f))
	if err != nil {
		t.Fatal(err)
	}
	p.tr.grace = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- p.serve(ctx, proxyCmdLn, proxyDataLn) }()
	r.stop = sync.OnceValue(func() error { cancel(); return <-served })
	t.Cleanup(func() { r.stop() })

	r.patCmd = dialPeer(t, proxyCmdLn.Addr().String())
	r.modemCmd = acceptPeer(t, modemCmdLn)
	r.patData = dialPeer(t, proxyDataLn.Addr().String())
	r.modemData = acceptPeer(t, modemDataLn)
	return r
}

// onlySession waits for the log to hold a session and checks it is the only one.
func (r *rig) onlySession(t *testing.T) []byte {
	t.Helper()
	waitForLines(t, r.logPath, 1)
	if err := r.stop(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(r.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(raw, []byte("\n")); n != 1 {
		t.Fatalf("want 1 session in the log, got %d:\n%s", n, raw)
	}
	return raw
}

func testProxySession(t *testing.T, mc modemCase) {
	r := newRig(t, mc.modem)
	patCmd, modemCmd, patData, modemData := r.patCmd, r.modemCmd, r.patData, r.modemData
	fromModem := func(line string) { relay(modemCmd, patCmd, []byte(line+"\r")) }
	sendIn := func(b []byte) { relay(modemData, patData, mc.toHost(b)) }
	sendOut := func(b []byte) { relay(patData, modemData, mc.toModem(b)) }

	cmsHello := "[WL2K-5.0-B2FWIHJM$]\r;PQ: 61245892\rCMS via LA1B >\r"
	patHello := ";FW: N0CALL\r[PAT-0.16.0-B2FHM$]\r;PR: 27315468\r; LA1B DE N0CALL (JO59JS)\r"
	pm := ";PM: N0CALL WX7Q2LZPK9HD 300 sender@example.com Hello\r"
	cmsProposals := proposalBlock("FC EM WX7Q2LZPK9HD 300 120 0")
	msg := transfer("Hello", 0, bytes.Repeat([]byte("ab\r\x01"), 30), 125)

	relay(patCmd, modemCmd, []byte("MYCALL N0CALL\r"))
	fromModem(mc.connected)
	for _, l := range mc.afterConnect {
		fromModem(l)
	}
	if mc.noise != nil {
		relay(modemData, patData, mc.noise)
	}
	sendIn([]byte(cmsHello))

	fromModem(mc.pttOn)
	time.Sleep(30 * time.Millisecond)
	sendOut([]byte(patHello + "FF\r"))
	fromModem("BUFFER 120")
	fromModem("BUFFER 120")
	fromModem("BUFFER 0")
	fromModem(mc.pttOff)
	time.Sleep(30 * time.Millisecond)

	sendIn([]byte(pm + cmsProposals))
	fromModem(mc.pttOn)
	sendOut([]byte("FS +\r"))
	fromModem(mc.pttOff)

	sendIn(msg[:50])
	sendIn(msg[50:])
	fromModem(mc.pttOn)
	sendOut([]byte("FQ\r"))
	fromModem(mc.pttOff)
	fromModem("DISCONNECTED")
	for _, l := range mc.afterDisconnect {
		fromModem(l)
	}

	raw := r.onlySession(t)
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"bytes_out", "bytes_in", "turn_count", "turnarounds", "turns",
		"local_tx_ms", "ptt", "gap_ms", "buffer", "messages", "errors"} {
		if _, ok := fields[k]; !ok {
			t.Errorf("the JSON line has no %q field", k)
		}
	}
	var s session
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}

	if s.Modem != mc.modem || s.ConnectLine != mc.connected || s.EndReason != "disconnected" {
		t.Errorf("modem %q, connect line %q, end reason %q", s.Modem, s.ConnectLine, s.EndReason)
	}
	if s.LocalCall != "N0CALL" || s.RemoteCall != "LA1B" {
		t.Errorf("local %q remote %q, want N0CALL and LA1B", s.LocalCall, s.RemoteCall)
	}
	wantIn := phaseBytes{
		Handshake: int64(len(cmsHello)),
		Pending:   int64(len(pm)),
		Proposals: int64(len(cmsProposals)),
		Payload:   int64(len(msg)),
	}
	wantOut := phaseBytes{
		Handshake: int64(len(patHello)),
		Answer:    int64(len("FS +\r")),
		End:       int64(len("FF\r") + len("FQ\r")),
	}
	if s.BytesIn != wantIn || s.BytesOut != wantOut {
		t.Errorf("bytes in %+v out %+v, want in %+v out %+v", s.BytesIn, s.BytesOut, wantIn, wantOut)
	}

	wantTurns := []turn{
		{Dir: inbound, Bytes: phaseBytes{Handshake: int64(len(cmsHello))}},
		{Dir: outbound, Bytes: phaseBytes{Handshake: int64(len(patHello)), End: 3}},
		{Dir: inbound, Bytes: phaseBytes{Pending: int64(len(pm)), Proposals: int64(len(cmsProposals))}},
		{Dir: outbound, Bytes: phaseBytes{Answer: 5}},
		{Dir: inbound, Bytes: phaseBytes{Payload: int64(len(msg))}},
		{Dir: outbound, Bytes: phaseBytes{End: 3}},
	}
	if s.TurnCount != 6 || s.Turnarounds != 5 || len(s.Turns) != len(wantTurns) {
		t.Fatalf("%d turns, %d turnarounds, %d listed; want 6, 5, 6", s.TurnCount, s.Turnarounds, len(s.Turns))
	}
	for i, tn := range s.Turns {
		if tn.Dir != wantTurns[i].Dir || tn.Bytes != wantTurns[i].Bytes {
			t.Errorf("turn %d: %s %+v, want %s %+v", i, tn.Dir, tn.Bytes, wantTurns[i].Dir, wantTurns[i].Bytes)
		}
		if tn.FirstByte.Before(s.Start) || tn.LastByte.Before(tn.FirstByte) {
			t.Errorf("turn %d: times out of order: %v to %v, session start %v", i, tn.FirstByte, tn.LastByte, s.Start)
		}
	}

	if len(s.PTT) != 3 || s.PTT[0].LocalTXMs < 30 {
		t.Errorf("PTT intervals %+v, want 3 with the first at least 30 ms", s.PTT)
	}
	var sum int64
	for _, iv := range s.PTT {
		sum += iv.LocalTXMs
	}
	if s.LocalTXMs != sum {
		t.Errorf("local_tx_ms %d is not the sum of the intervals, %d", s.LocalTXMs, sum)
	}
	if len(s.GapMs) != 2 || s.GapMs[0] < 30 {
		t.Errorf("gaps %v, want 2 with the first at least 30 ms", s.GapMs)
	}
	var buffer []int
	for _, b := range s.Buffer {
		buffer = append(buffer, b.Bytes)
	}
	if !reflect.DeepEqual(buffer, []int{120, 120, 0}) {
		t.Errorf("BUFFER samples %v", buffer)
	}
	var states []string
	for _, st := range s.States {
		states = append(states, st.Line)
	}
	if !reflect.DeepEqual(states, mc.afterConnect) {
		t.Errorf("states %q, want %q", states, mc.afterConnect)
	}
	wantMsgs := []message{{Dir: inbound, Title: "Hello", CompressedSize: 120, Complete: true}}
	if !reflect.DeepEqual(s.Messages, wantMsgs) {
		t.Errorf("messages %+v, want %+v", s.Messages, wantMsgs)
	}
	if len(s.Errors) != 0 {
		t.Errorf("errors %+v", s.Errors)
	}
}

// A link that drops mid-message is common on HF. The session must still be
// written, with the partial message and the open PTT interval closed.
func TestProxySessionCutShort(t *testing.T) {
	cases := []struct {
		reason string
		cut    func(t *testing.T, r *rig)
	}{
		{"data connection closed", func(t *testing.T, r *rig) { r.modemData.c.Close() }},
		{"proxy stopped", func(t *testing.T, r *rig) {
			if err := r.stop(); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.reason, func(t *testing.T) {
			r := newRig(t, "vara")
			msg := transfer("Field day plans", 0, bytes.Repeat([]byte{stx, eot, '\r'}, 200), 125)
			relay(r.patCmd, r.modemCmd, []byte("MYCALL N0CALL\r"))
			relay(r.modemCmd, r.patCmd, []byte("CONNECTED N0CALL LA1B 2300\r"))
			relay(r.modemCmd, r.patCmd, []byte("PTT ON\r"))
			relay(r.patData, r.modemData, []byte("FS +\r"))
			relay(r.modemData, r.patData, msg[:300])
			c.cut(t, r)

			var s session
			if err := json.Unmarshal(r.onlySession(t), &s); err != nil {
				t.Fatal(err)
			}
			if s.EndReason != c.reason {
				t.Errorf("end reason %q, want %q", s.EndReason, c.reason)
			}
			// 300 bytes are a 20-byte header and three blocks: 274 data
			// bytes behind three 2-byte block starts.
			wantMsgs := []message{{Dir: inbound, Title: "Field day plans", CompressedSize: 274}}
			if !reflect.DeepEqual(s.Messages, wantMsgs) {
				t.Errorf("messages %+v, want %+v", s.Messages, wantMsgs)
			}
			if len(s.Errors) != 1 || s.Errors[0].Text != `message "Field day plans": cut off before the end` {
				t.Errorf("errors %+v", s.Errors)
			}
			if s.BytesIn.Payload != 300 || s.BytesOut.Answer != 5 || s.TurnCount != 2 {
				t.Errorf("payload in %d, answer out %d, %d turns; want 300, 5, 2",
					s.BytesIn.Payload, s.BytesOut.Answer, s.TurnCount)
			}
			if len(s.PTT) != 1 || !s.PTT[0].Off.Equal(s.End) {
				t.Errorf("PTT %+v, want one interval closed at the session end %v", s.PTT, s.End)
			}
		})
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestProxyModemNotRunning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	var logs syncBuffer
	p, err := newProxy("vara", addr, log.New(&logs, "", 0), func(*session) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	cmdLn, dataLn := listenAdjacent(t)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error)
	go func() { served <- p.serve(ctx, cmdLn, dataLn) }()
	defer func() { cancel(); <-served }()

	pat := dialPeer(t, cmdLn.Addr().String())
	if _, err := pat.c.Read(make([]byte, 1)); err == nil {
		t.Fatal("the proxy kept the connection open without a modem")
	}
	if got := logs.String(); !strings.Contains(got, "cannot reach the VARA command port at "+addr) {
		t.Errorf("the log does not say which address failed: %q", got)
	}
}
