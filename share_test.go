package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestShareStrips(t *testing.T) {
	at := time.Date(2026, 11, 19, 18, 0, 0, 0, time.UTC)
	s := session{
		Modem:       "vara",
		Link:        "varahf",
		Start:       at,
		End:         at.Add(time.Minute),
		ConnectLine: "CONNECTED N0CALL N0GATE 2300",
		LocalCall:   "N0CALL",
		RemoteCall:  "N0GATE",
		SIDIn:       "[WL2K-5.0-B2FWIHJM$]",
		Commands: []commandLine{
			{T: at, Line: "MYCALL N0CALL N0CALL-10"},
			{T: at, Line: "GRIDSQUARE JO59JS"},
			{T: at, Line: "CONNECT N0CALL N0GATE"},
			{T: at, Line: "PTT ON"},
			{T: at, Line: "C 0 N0GATE N0CALL *** CONNECTED With Station N0GATE"},
		},
		States:    []stateLine{{T: at, Line: "STATUS CONNECT TO N0GATE FAILED!"}},
		Proposals: []proposal{{Dir: inbound, Code: "FC", MID: "WX7Q2LZPK9HD", Size: 300, CompressedSize: 120, Answer: "accept"}},
		Messages:  []message{{Dir: inbound, Title: "Meet at 6 by the tower", CompressedSize: 120, Complete: true}},
		Errors: []streamError{
			{T: at, Text: `message "Meet at 6 by the tower": bad checksum`},
			{T: at, Text: "*** Secure login failed for n0call from 88.90.2.95 (user@example.com)"},
		},
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "sessions.jsonl")
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runShare([]string{in}, &out); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"N0CALL", "n0call", "N0GATE", "JO59JS", "WX7Q2LZPK9HD", "tower", "88.90", "example.com"} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("shared output still has %q:\n%s", secret, out.String())
		}
	}

	var got session
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Shared || got.Link != "varahf" || got.SIDIn != s.SIDIn || got.ConnectLine != "CONNECTED 2300" {
		t.Errorf("shared %v, link %q, sid %q, connect line %q", got.Shared, got.Link, got.SIDIn, got.ConnectLine)
	}
	var lines []string
	for _, c := range got.Commands {
		lines = append(lines, c.Line)
	}
	want := []string{"MYCALL", "GRIDSQUARE", "CONNECT", "PTT ON", "C 0 *** CONNECTED With Station"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("commands %q, want %q", lines, want)
	}
	p := got.Proposals[0]
	if p.Size != 300 || p.CompressedSize != 120 || p.Answer != "accept" {
		t.Errorf("proposal numbers lost: %+v", p)
	}
	if got.Errors[0].Text != `message "": bad checksum` {
		t.Errorf("error %q", got.Errors[0].Text)
	}

	shared := filepath.Join(dir, "shared.jsonl")
	if err := os.WriteFile(shared, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	var table bytes.Buffer
	if err := runResults([]string{shared}, &table); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table.String(), "| varahf | 1 | 1 | 0 |") {
		t.Errorf("results table:\n%s", table.String())
	}
	if err := runResults([]string{in}, &table); err == nil {
		t.Error("results took a session that was not shared")
	}
}

// A title with quotes in it comes out of the parser's error escaped, as
// "Meet \"Alice Smith\" at the lodge", and none of it may survive.
func TestShareQuotedTitle(t *testing.T) {
	var p parser
	b := transfer(`Meet "Alice Smith" at the lodge`, 0, []byte("data"), 125)
	els := p.write(b[:len(b)-1])
	els = append(els, p.flush()...)
	s := session{LocalCall: "N0CALL"}
	for _, e := range els {
		if e.err != "" {
			s.Errors = append(s.Errors, streamError{Text: e.err})
		}
	}
	// the transfer is cut off before its checksum, so there is an error
	if len(s.Errors) != 1 {
		t.Fatalf("errors %+v", s.Errors)
	}
	strip(&s)
	if got := s.Errors[0].Text; got != `message "": cut off before the end` {
		t.Errorf("shared error %q", got)
	}
}

func TestReadAllGlobAndUTF16(t *testing.T) {
	dir := t.TempDir()
	line := []byte(`{"modem":"vara","start":"2026-11-19T18:00:00Z","end":"2026-11-19T18:01:00Z"}` + "\n")
	for _, name := range []string{"a.jsonl", "b.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), line, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := readAll([]string{filepath.Join(dir, "*.jsonl")})
	if err != nil || len(sessions) != 2 {
		t.Fatalf("glob read %d sessions, error %v; want 2", len(sessions), err)
	}
	if _, err := readAll([]string{filepath.Join(dir, "*.json")}); err == nil {
		t.Error("a glob matching nothing did not fail")
	}
	utf16 := filepath.Join(dir, "ps5.jsonl")
	if err := os.WriteFile(utf16, []byte{0xff, 0xfe, '{', 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readAll([]string{utf16}); err == nil || !strings.Contains(err.Error(), "share -o") {
		t.Errorf("UTF-16 file gave %v, want a pointer to share -o", err)
	}
}

// Files in results/ are published, so each must already be what share makes
// of it, and RESULTS.md must show the table they give.
func TestResultsAreShared(t *testing.T) {
	files, err := filepath.Glob("results/*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		sessions, err := readSessions(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, s := range sessions {
			before, _ := json.Marshal(s)
			strip(&s)
			after, _ := json.Marshal(s)
			if !bytes.Equal(before, after) {
				t.Errorf("%s: session %d has not been through b2trace share", f, i+1)
			}
		}
	}
	page, err := os.ReadFile("RESULTS.md")
	if err != nil {
		t.Fatal(err)
	}
	page = bytes.ReplaceAll(page, []byte("\r\n"), []byte("\n"))
	if len(files) == 0 {
		if !bytes.Contains(page, []byte("No sessions have been shared yet.")) {
			t.Error("RESULTS.md shows results, but results/ has none")
		}
		return
	}
	var table bytes.Buffer
	if err := runResults(files, &table); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, table.Bytes()) {
		t.Errorf("RESULTS.md does not have the table from results/; run b2trace results results/*.jsonl:\n%s", table.String())
	}
}

func TestParseAnswers(t *testing.T) {
	got := parseAnswers("FS +-=!1234Y!0E")
	want := []answer{{"accept", 0}, {"reject", 0}, {"defer", 0}, {"resume", 1234}, {"accept", 0}, {"resume", 0}, {"other", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseProposal(t *testing.T) {
	cases := map[string]proposal{
		"FC EM WX7Q2LZPK9HD 3200 1780 0": {Code: "FC", MID: "WX7Q2LZPK9HD", Size: 3200, CompressedSize: 1780},
		"FE EM ABCDEF123456 900 410 1":   {Code: "FE", MID: "ABCDEF123456", Size: 900, CompressedSize: 410, Optimistic: true},
		"FA PN N0CALL@WW 123_N0CALL 400": {Code: "FA"},
	}
	for line, want := range cases {
		if got := parseProposal(line); got != want {
			t.Errorf("%q: got %+v, want %+v", line, got, want)
		}
	}
}

// The cells come from the numbers in wantSummary: turnarounds 5 and 2, gap
// 2.50 s, reply time 0.67 s and 2.00 s, connect 5.0 s, and the payload shares
// of time and of bytes.
func TestResultsTable(t *testing.T) {
	sessions, err := readSessions("testdata/sessions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var shared bytes.Buffer
	if err := writeShared(&shared, sessions); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "shared.jsonl")
	if err := os.WriteFile(path, shared.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := runResults([]string{path}, &got); err != nil {
		t.Fatal(err)
	}
	want := "| Link | Sessions | Messages | Turnarounds (median) | Gap (mean) | Reply time (mean) | Connect (median) | Payload share of time | Payload share of bytes |\n" +
		"|---|---|---|---|---|---|---|---|---|\n" +
		"| ardop | 1 | 1 | 2 | - | 2.00 s | - | 66.3% | 63.9% |\n" +
		"| varahf | 1 | 1 | 5 | 2.50 s | 0.67 s | 5.0 s | 91.0% | 81.8% |\n"
	if got.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", got.String(), want)
	}
}

// A session that never got to B2F has no turns, so there is nothing to average.
func TestResultRowNoTurns(t *testing.T) {
	got := resultRow("vara", []session{{Modem: "vara"}})
	want := "| vara | 1 | 0 | 0 | - | - | - | - | - |"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
