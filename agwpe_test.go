package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func agwBytes(kind byte, from, to string, data []byte) []byte {
	h := make([]byte, 36)
	h[4] = kind
	copy(h[8:18], from)
	copy(h[18:28], to)
	binary.LittleEndian.PutUint32(h[28:32], uint32(len(data)))
	return append(h, data...)
}

func agwCount(n uint32) []byte { return binary.LittleEndian.AppendUint32(nil, n) }

func TestAGWSplitter(t *testing.T) {
	stream := concat(
		agwBytes('X', "N0CALL", "", nil),
		agwBytes('D', "N0GATE", "N0CALL", []byte("FS +\r")),
		agwBytes('Y', "N0GATE", "N0CALL", agwCount(3)),
	)
	for _, size := range []int{1, 7, 36, 41, len(stream)} {
		var s agwSplitter
		var got []string
		for b := stream; len(b) > 0; {
			n := min(size, len(b))
			frames, err := s.write(b[:n])
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range frames {
				if f.kind == 'D' {
					got = append(got, "data "+string(f.data))
				} else {
					got = append(got, f.line())
				}
			}
			b = b[n:]
		}
		want := []string{"X 0 N0CALL -", "data FS +\r", "Y 0 N0GATE N0CALL 3"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("reads of %d bytes: got %q, want %q", size, got, want)
		}
	}
}

// Pointed at a KISS port by mistake, the proxy must keep relaying instead of
// waiting for gigabytes or panicking on a 32-bit build.
func TestAGWSplitterNotAGWPE(t *testing.T) {
	var s agwSplitter
	good := agwBytes('X', "N0CALL", "", nil)
	bad := agwBytes('D', "N0CALL", "N0GATE", nil)
	binary.LittleEndian.PutUint32(bad[28:32], 0xffffffff)
	frames, err := s.write(concat(good, bad))
	if len(frames) != 1 || err == nil {
		t.Fatalf("got %d frames and error %v, want the good frame and an error", len(frames), err)
	}
	if frames, err := s.write(good); frames != nil || err != nil {
		t.Errorf("after a bad length: %d frames, error %v; want nothing", len(frames), err)
	}
}

// A login frame carries a username and password, which must not reach the log.
func TestAGWLineHidesData(t *testing.T) {
	data := make([]byte, 510)
	copy(data, "n0call")
	copy(data[255:], "secret")
	f := agwFrame{kind: 'P', data: data}
	if got := f.line(); strings.Contains(got, "secret") || got != "P 0 - - (data length 510)" {
		t.Errorf("login frame written as %q", got)
	}
}

func concat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestProxyAGWPE(t *testing.T) {
	tnc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tnc.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "sessions.jsonl")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := newProxy("agwpe", "packet1200", tnc.Addr().String(), log.New(io.Discard, "", 0), appendSession(f))
	if err != nil {
		t.Fatal(err)
	}
	p.tr.grace = 500 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- p.serve(ctx, ln, nil) }()
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			cancel()
			if err := <-served; err != nil {
				t.Error(err)
			}
		}
	}
	defer stop()

	pat := dialPeer(t, ln.Addr().String())
	modem := acceptPeer(t, tnc)
	out := func(kind byte, data []byte) { relay(pat, modem, agwBytes(kind, "N0CALL", "N0GATE", data)) }
	in := func(kind byte, data []byte) { relay(modem, pat, agwBytes(kind, "N0GATE", "N0CALL", data)) }

	cmsHello := "[WL2K-5.0-B2FWIHJM$]\rCMS via N0GATE >\r"
	patHello := "[PAT-0.17.0-B2FHMZ$]\r; N0GATE DE N0CALL (JO59JS)\r"
	proposals := proposalBlock("FC EM WX7Q2LZPK9HD 300 120 0")
	msg := transfer("Hello", 0, bytes.Repeat([]byte("ab\r\x01"), 30), 125)

	relay(pat, modem, agwBytes('X', "N0CALL", "", nil))
	relay(modem, pat, agwBytes('X', "N0CALL", "", []byte{1}))
	out('C', nil)
	time.Sleep(20 * time.Millisecond)
	in('C', []byte("*** CONNECTED With Station N0GATE\r\x00"))
	in('D', []byte(cmsHello))
	out('D', []byte(patHello+"FF\r"))
	for _, n := range []uint32{2, 2, 2, 0} {
		out('Y', nil)
		in('Y', agwCount(n))
	}
	in('D', []byte(proposals))
	out('D', []byte("FS +\r"))
	// one data frame split across two TCP writes
	frame := agwBytes('D', "N0GATE", "N0CALL", msg)
	relay(modem, pat, frame[:20])
	relay(modem, pat, frame[20:])
	out('D', []byte("FQ\r"))
	in('d', []byte("*** DISCONNECTED From Station N0GATE\r\x00"))

	waitForLines(t, logPath, 1)
	stop()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var s session
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}

	if s.Modem != "agwpe" || s.Link != "packet1200" || s.EndReason != "disconnected" {
		t.Errorf("modem %q, link %q, end reason %q", s.Modem, s.Link, s.EndReason)
	}
	if s.LocalCall != "N0CALL" || s.RemoteCall != "N0GATE" {
		t.Errorf("local %q remote %q, want N0CALL and N0GATE", s.LocalCall, s.RemoteCall)
	}
	if s.ConnectMs < 20 {
		t.Errorf("connect_ms %d, want at least 20", s.ConnectMs)
	}
	wantIn := phaseBytes{Handshake: int64(len(cmsHello)), Proposals: int64(len(proposals)), Payload: int64(len(msg))}
	wantOut := phaseBytes{Handshake: int64(len(patHello)), Answer: 5, End: 6}
	if s.BytesIn != wantIn || s.BytesOut != wantOut || s.TurnCount != 6 {
		t.Errorf("bytes in %+v out %+v, %d turns; want in %+v out %+v, 6 turns", s.BytesIn, s.BytesOut, s.TurnCount, wantIn, wantOut)
	}
	var buffer []int
	for _, b := range s.Buffer {
		buffer = append(buffer, b.Bytes)
	}
	if !reflect.DeepEqual(buffer, []int{2, 0}) || s.BufferUnit != "frames" {
		t.Errorf("buffer %v in %q, want the changes [2 0] in frames", buffer, s.BufferUnit)
	}
	wantMsgs := []message{{Dir: inbound, Title: "Hello", CompressedSize: 120, Complete: true}}
	if !reflect.DeepEqual(s.Messages, wantMsgs) {
		t.Errorf("messages %+v, want %+v", s.Messages, wantMsgs)
	}
	if len(s.Proposals) != 1 || s.Proposals[0].Answer != "accept" || s.SIDOut != "[PAT-0.17.0-B2FHMZ$]" {
		t.Errorf("proposals %+v, sid out %q", s.Proposals, s.SIDOut)
	}
	var got []string
	for _, c := range s.Commands {
		got = append(got, c.Line)
	}
	want := []string{
		"X 0 N0CALL -",
		"X 0 N0CALL - (data length 1)",
		"C 0 N0CALL N0GATE",
		"C 0 N0GATE N0CALL *** CONNECTED With Station N0GATE",
		"d 0 N0GATE N0CALL *** DISCONNECTED From Station N0GATE",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("commands %q, want %q", got, want)
	}

	var sum bytes.Buffer
	if err := runSummary([]string{logPath}, &sum); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sum.String(), "possible retries  - (AGWPE does not report them)") {
		t.Errorf("summary claims a retry count for AGWPE:\n%s", sum.String())
	}
}
