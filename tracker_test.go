package main

import (
	"io"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"
)

// newTestTracker returns a tracker that collects the sessions it writes.
func newTestTracker(modem string, grace time.Duration) (*tracker, *[]session) {
	var got []session
	tr := newTracker(modem, "", log.New(io.Discard, "", 0), func(s *session) error {
		got = append(got, *s)
		return nil
	})
	tr.grace = grace
	return tr, &got
}

func commandLines(s session) []string {
	var l []string
	for _, c := range s.Commands {
		l = append(l, c.Line)
	}
	return l
}

func TestTrackerCommandsAcrossSessions(t *testing.T) {
	tr, got := newTestTracker("vara", 20*time.Millisecond)
	host := func(l string) { tr.command(0, outbound, l) }
	modem := func(l string) { tr.command(0, inbound, l) }

	host("MYCALL N0CALL")
	host("CONNECT N0CALL N0GATE")
	modem("CONNECTED N0CALL N0GATE 2300")
	modem("PTT ON")
	modem("BUFFER 10")
	modem("PTT OFF")
	modem("DISCONNECTED")
	// within the grace period: the host's next command is for the next
	// session, the modem's answer still for this one
	host("BW2300")
	modem("OK")
	time.Sleep(200 * time.Millisecond)

	// a dial that fails, then a call the remote starts
	host("CONNECT N0CALL N0RMTB")
	time.Sleep(20 * time.Millisecond)
	modem("DISCONNECTED")
	host("LISTEN ON")
	modem("CONNECTED N0RMTC N0CALL 2300")
	modem("DISCONNECTED")
	time.Sleep(200 * time.Millisecond)

	// a long idle spell: only the last 50 lines are kept
	for range 60 {
		host("LISTEN ON")
	}
	modem("CONNECTED N0RMTD N0CALL 2300")
	tr.stop()

	if len(*got) != 3 {
		t.Fatalf("%d sessions, want 3", len(*got))
	}
	s1, s2, s3 := (*got)[0], (*got)[1], (*got)[2]
	want1 := []string{"MYCALL N0CALL", "CONNECT N0CALL N0GATE", "CONNECTED N0CALL N0GATE 2300", "PTT ON", "PTT OFF", "DISCONNECTED", "OK"}
	if l := commandLines(s1); !reflect.DeepEqual(l, want1) {
		t.Errorf("session 1 commands %q, want %q", l, want1)
	}
	want2 := []string{"BW2300", "CONNECT N0CALL N0RMTB", "DISCONNECTED", "LISTEN ON", "CONNECTED N0RMTC N0CALL 2300", "DISCONNECTED"}
	if l := commandLines(s2); !reflect.DeepEqual(l, want2) {
		t.Errorf("session 2 commands %q, want %q", l, want2)
	}
	if s2.ConnectMs != 0 || s2.LocalCall != "N0CALL" || s2.RemoteCall != "N0RMTC" {
		t.Errorf("session 2: connect_ms %d (the failed dial must not count), local %q, remote %q",
			s2.ConnectMs, s2.LocalCall, s2.RemoteCall)
	}
	l := commandLines(s3)
	if len(l) != 51 || l[0] != "LISTEN ON" || l[50] != "CONNECTED N0RMTD N0CALL 2300" {
		t.Errorf("session 3 has %d commands, want 50 carried over and CONNECTED", len(l))
	}
}

// Other programs can use the same AGWPE TNC as Pat. Their frames must not
// reach Pat's session, and their hanging up must not end it.
func TestTrackerAGWPEOtherClient(t *testing.T) {
	tr, got := newTestTracker("agwpe", time.Hour)
	const pat, aprs = 1, 2
	tr.command(aprs, outbound, "X 0 N0APRS-9 -")
	tr.command(pat, outbound, "X 0 N0CALL -")
	tr.command(pat, outbound, "v 0 N0CALL N0GATE (data length 11)")
	time.Sleep(20 * time.Millisecond)
	tr.command(pat, inbound, "C 0 N0GATE N0CALL *** CONNECTED With Station N0GATE")
	sid := "[WL2K-5.0-B2FWIHJM$]\r"
	tr.data(pat, inbound, []byte(sid))
	tr.command(aprs, inbound, "U 0 N0DIGI APRS (data length 5)")
	tr.data(aprs, inbound, []byte("not B2F\r"))
	tr.connClosed(aprs, "API connection")
	tr.data(pat, outbound, []byte("FF\r"))
	tr.command(pat, inbound, "d 0 N0GATE N0CALL *** DISCONNECTED From Station N0GATE")
	tr.stop()

	if len(*got) != 1 {
		t.Fatalf("%d sessions, want 1", len(*got))
	}
	s := (*got)[0]
	if s.EndReason != "disconnected" || s.BytesIn.total() != int64(len(sid)) || s.BytesOut.End != 3 {
		t.Errorf("end %q, bytes in %d, end out %d; want disconnected, %d, 3", s.EndReason, s.BytesIn.total(), s.BytesOut.End, len(sid))
	}
	for _, l := range commandLines(s) {
		if strings.Contains(l, "N0APRS") || strings.Contains(l, "N0DIGI") {
			t.Errorf("the other client's frame %q is in Pat's session", l)
		}
	}
	if s.ConnectMs < 20 || s.LocalCall != "N0CALL" {
		t.Errorf("connect_ms %d after a dial with digipeaters, local %q", s.ConnectMs, s.LocalCall)
	}
}

func TestRecorderAnswersOpenProposals(t *testing.T) {
	r := newRecorder("vara", time.Now(), "CONNECTED N0CALL N0GATE 2300")
	send := func(dir direction, s string) { r.data(dir, time.Now(), []byte(s)) }
	send(inbound, "FC EM AAAAAAAAAAAA 100 50 0\rFC EM BBBBBBBBBBBB 100 50 0\rF> 00\r")
	send(outbound, "FS +=\r")
	send(inbound, "FC EM CCCCCCCCCCCC 100 50 0\rF> 00\r")
	send(outbound, "FS !20\r")
	var got []string
	for _, p := range r.s.Proposals {
		got = append(got, p.MID[:1]+" "+p.Answer)
	}
	want := []string{"A accept", "B defer", "C resume"}
	if !reflect.DeepEqual(got, want) || r.s.Proposals[2].Offset != 20 {
		t.Errorf("answers %q (offset %d), want %q with offset 20", got, r.s.Proposals[2].Offset, want)
	}
}

// A bracketed banner before the SID is not the SID.
func TestRecorderSIDAfterBanner(t *testing.T) {
	r := newRecorder("vara", time.Now(), "CONNECTED N0CALL N0GATE 2300")
	r.data(inbound, time.Now(), []byte("[Welcome to the N0GATE gateway]\r[WL2K-5.0-B2FWIHJM$]\r"))
	if r.s.SIDIn != "[WL2K-5.0-B2FWIHJM$]" {
		t.Errorf("sid_in %q", r.s.SIDIn)
	}
}
