package main

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"mime"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/la5nta/wl2k-go/lzhuf"
)

// transfer frames data the way B2F sends a compressed message: an SOH header
// with title and offset, STX blocks of up to blockSize bytes (256 goes on the
// wire as length 0), then EOT and the checksum.
func transfer(title string, offset int, data []byte, blockSize int) []byte {
	off := strconv.Itoa(offset)
	b := []byte{soh, byte(len(title) + len(off) + 2)}
	b = append(b, title...)
	b = append(b, nul)
	b = append(b, off...)
	b = append(b, nul)
	var sum byte
	for len(data) > 0 {
		n := min(blockSize, len(data))
		b = append(b, stx, byte(n))
		b = append(b, data[:n]...)
		for _, c := range data[:n] {
			sum += c
		}
		data = data[n:]
	}
	return append(b, eot, -sum)
}

// proposalBlock writes proposal lines and the F> line with their checksum.
func proposalBlock(lines ...string) string {
	var s strings.Builder
	sum := 0
	for _, l := range lines {
		s.WriteString(l + "\r")
		for _, c := range l + "\r" {
			sum += int(c)
		}
	}
	fmt.Fprintf(&s, "F> %02X\r", -sum&0xff)
	return s.String()
}

func compressed(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := lzhuf.NewB2Writer(&buf)
	w.Write([]byte(text))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// b2Message makes a Winlink message with a body of seeded random words, which
// compresses about as badly as real prose.
func b2Message(mid, subject string, words int) string {
	vocab := strings.Fields("the net will meet on 3.585 at 1900 local check in with your call " +
		"and location antenna battery power solar field day station operator traffic " +
		"emergency drill weather report road closed shelter open water radio relay")
	rng := rand.New(rand.NewPCG(uint64(len(mid)), uint64(words)))
	var body strings.Builder
	for i := range words {
		body.WriteString(vocab[rng.IntN(len(vocab))])
		if i%12 == 11 {
			body.WriteString(".\r\n")
		} else {
			body.WriteByte(' ')
		}
	}
	return "Mid: " + mid + "\r\n" +
		"Body: " + strconv.Itoa(body.Len()) + "\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Date: 2026/11/19 18:02\r\n" +
		"From: N0CALL\r\n" +
		"Mbo: N0CALL\r\n" +
		"Subject: " + subject + "\r\n" +
		"To: LA1B\r\n" +
		"Type: Private\r\n\r\n" + body.String()
}

type parsed struct {
	bytes    phaseBytes
	lines    []string
	messages []message
	errors   []string
}

// parse feeds chunks to one parser, then ends the session, and collects what
// it reported.
func parse(chunks [][]byte) parsed {
	var p parser
	var r parsed
	collect := func(els []element) {
		for _, e := range els {
			r.bytes.add(e.phase, e.bytes)
			if e.line != "" {
				r.lines = append(r.lines, fmt.Sprintf("%d %s", e.phase, e.line))
			}
			if e.msg != nil {
				r.messages = append(r.messages, *e.msg)
			}
			if e.err != "" {
				r.errors = append(r.errors, e.err)
			}
		}
	}
	for _, c := range chunks {
		collect(p.write(c))
	}
	collect(p.flush())
	return r
}

// splits returns ways to cut b into reads: whole, byte by byte, and random
// sizes from a few seeds.
func splits(b []byte) map[string][][]byte {
	ways := map[string][][]byte{"whole": {b}}
	var single [][]byte
	for i := range b {
		single = append(single, b[i:i+1])
	}
	ways["bytes"] = single
	for seed := range uint64(5) {
		rng := rand.New(rand.NewPCG(seed, 99))
		var chunks [][]byte
		for rest := b; len(rest) > 0; {
			n := min(1+rng.IntN(40), len(rest))
			chunks = append(chunks, rest[:n])
			rest = rest[n:]
		}
		ways[fmt.Sprintf("seed %d", seed)] = chunks
	}
	return ways
}

func TestParserSession(t *testing.T) {
	// Pat calls a gateway, sends one message and gets one, resumed at an offset.
	patTitle := "Rapport fra Bodø"
	patData := compressed(t, b2Message("4HQ8TW3BTMBE", patTitle, 200))
	cmsData := compressed(t, b2Message("WX7Q2LZPK9HD", "Field day plans", 600))
	const resumeAt = 200

	cmsHello := "[WL2K-5.0-B2FWIHJM$]\r;PQ: 61245892\rFairbanks AK gateway\rCMS via LA1B >\r"
	patHello := ";FW: N0CALL\r[PAT-0.16.0-B2FHM$]\r;PR: 27315468\r; LA1B DE N0CALL (JO59JS)\r"
	patProposal := fmt.Sprintf("FC EM 4HQ8TW3BTMBE %d %d 0", 1500, len(patData))
	patProposals := proposalBlock(patProposal)
	patTransfer := transfer(mime.QEncoding.Encode("utf-8", patTitle), 0, patData, 125)
	pm := ";PM: N0CALL WX7Q2LZPK9HD 4100 sender@example.com Field day plans\r"
	cmsProposal := fmt.Sprintf("FC EM WX7Q2LZPK9HD %d %d 0", 4100, len(cmsData))
	cmsProposals := proposalBlock(cmsProposal)
	cmsTransfer := transfer("Field day plans", resumeAt, cmsData[resumeAt:], 256)
	warning := ";WARNING: gateway restarts at 0300z\r"

	out := slices.Concat([]byte(patHello), []byte(patProposals), patTransfer,
		[]byte("FS !200\r"), []byte("FF\r"))
	in := slices.Concat([]byte(cmsHello), []byte("FS +\r"), []byte(pm), []byte(cmsProposals),
		cmsTransfer, []byte(warning), []byte("FQ\r"))

	for _, c := range []byte{nul, soh, stx, eot, '\r'} {
		if !bytes.Contains(patData, []byte{c}) || !bytes.Contains(cmsData[resumeAt:], []byte{c}) {
			t.Fatalf("both payloads should contain byte 0x%02x, to show it does not confuse the parser", c)
		}
	}

	wantOut := parsed{
		bytes: phaseBytes{
			Handshake: int64(len(patHello)),
			Proposals: int64(len(patProposals)),
			Payload:   int64(len(patTransfer)),
			Answer:    int64(len("FS !200\r")),
			End:       int64(len("FF\r")),
		},
		lines: []string{
			"0 ;FW: N0CALL", "0 [PAT-0.16.0-B2FHM$]", "0 ;PR: 27315468", "0 ; LA1B DE N0CALL (JO59JS)",
			"2 " + patProposal, "2 " + strings.Split(patProposals, "\r")[1],
			"3 FS !200", "5 FF",
		},
		messages: []message{{Title: patTitle, Offset: 0, CompressedSize: len(patData), Complete: true}},
	}
	wantIn := parsed{
		bytes: phaseBytes{
			Handshake: int64(len(cmsHello)),
			Answer:    int64(len("FS +\r")),
			Pending:   int64(len(pm)),
			Proposals: int64(len(cmsProposals)),
			Payload:   int64(len(cmsTransfer)),
			Other:     int64(len(warning)),
			End:       int64(len("FQ\r")),
		},
		lines: []string{
			"0 [WL2K-5.0-B2FWIHJM$]", "0 ;PQ: 61245892", "0 Fairbanks AK gateway", "0 CMS via LA1B >",
			"3 FS +", "1 " + strings.TrimSuffix(pm, "\r"), "2 " + cmsProposal,
			"2 " + strings.Split(cmsProposals, "\r")[1], "7 ;WARNING: gateway restarts at 0300z", "5 FQ",
		},
		messages: []message{{Title: "Field day plans", Offset: resumeAt, CompressedSize: len(cmsData) - resumeAt, Complete: true}},
	}

	for name, stream := range map[string][]byte{"out": out, "in": in} {
		want := wantOut
		if name == "in" {
			want = wantIn
		}
		for how, chunks := range splits(stream) {
			got := parse(chunks)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s stream read %s:\ngot  %+v\nwant %+v", name, how, got, want)
			}
		}
	}
}

func TestParserBrokenTransfers(t *testing.T) {
	data := bytes.Repeat([]byte{0x5a, 0x0d, 0x01}, 100)

	badSum := transfer("Bad sum", 0, data, 125)
	badSum[len(badSum)-1]++

	// The gateway gives up after the first block and sends an error line.
	interrupted := transfer("Interrupted", 0, data[:125], 125)
	interrupted = interrupted[:len(interrupted)-2]
	errLine := "*** Timeout waiting for data\r"

	// The session ends 26 bytes into the first block, after the 12-byte
	// header and the 2-byte block start.
	cutOff := transfer("Cut off", 0, data, 125)[:40]

	stream := slices.Concat(badSum, interrupted, []byte(errLine), cutOff)
	want := parsed{
		bytes: phaseBytes{
			Payload: int64(len(badSum) + len(interrupted) + len(cutOff)),
			Error:   int64(len(errLine)),
		},
		lines: []string{"6 *** Timeout waiting for data"},
		messages: []message{
			{Title: "Bad sum", CompressedSize: len(data)},
			{Title: "Interrupted", CompressedSize: 125},
			{Title: "Cut off", CompressedSize: 26},
		},
		errors: []string{
			`message "Bad sum": bad checksum`,
			`message "Interrupted": unexpected byte 0x2a between data blocks`,
			"*** Timeout waiting for data",
			`message "Cut off": cut off before the end`,
		},
	}
	for how, chunks := range splits(stream) {
		if got := parse(chunks); !reflect.DeepEqual(got, want) {
			t.Errorf("read %s:\ngot  %+v\nwant %+v", how, got, want)
		}
	}
}
