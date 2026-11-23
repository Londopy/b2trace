package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/la5nta/wl2k-go/fbb"
)

// COMPRESSION.md says each corpus file is what wl2k-go, the library Pat uses,
// writes for that message: reading it and writing it back gives the same bytes.
func TestCorpusIsWhatWl2kGoWrites(t *testing.T) {
	paths, err := filepath.Glob("corpus/*.b2f")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 74 {
		t.Fatalf("corpus has %d messages, COMPRESSION.md describes 74", len(paths))
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var m fbb.Message
		if err := m.ReadFrom(bytes.NewReader(data)); err != nil {
			t.Errorf("%s: wl2k-go cannot read it: %v", p, err)
			continue
		}
		if err := m.Validate(); err != nil {
			t.Errorf("%s: %v", p, err)
		}
		out, err := m.Bytes()
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if !bytes.Equal(out, data) {
			at := 0
			for at < min(len(out), len(data)) && out[at] == data[at] {
				at++
			}
			t.Errorf("%s: wl2k-go writes %d bytes for this message, the file has %d; they differ from byte %d",
				p, len(out), len(data), at)
		}
	}
}

// The totals COMPRESSION.md quotes for every column but zstd+dict come out
// the same on every run, so a change to the corpus or a compressor that moves
// them has to come with new numbers in the document.
func TestCompressionTotalsMatchTheDocument(t *testing.T) {
	doc, err := os.ReadFile("COMPRESSION.md")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(string(doc)), " ")
	m := regexp.MustCompile(`(\d+) raw, (\d+) with LZHUF, (\d+) with gzip, (\d+) with zstd, (\d+) with brotli`).FindStringSubmatch(text)
	if m == nil {
		t.Fatal("COMPRESSION.md no longer has the sentence with the total bytes")
	}
	want := map[string]string{"raw": m[1], "lzhuf": m[2], "gzip": m[3], "zstd": m[4], "brotli": m[5]}

	paths, err := findFiles([]string{"corpus"})
	if err != nil {
		t.Fatal(err)
	}
	names, files, _, err := readUnique(paths)
	if err != nil {
		t.Fatal(err)
	}
	r, err := measure(names, files)
	if err != nil {
		t.Fatal(err)
	}
	for i, total := range r.totals() {
		if got := strconv.Itoa(total); got != want[r.cols[i]] {
			t.Errorf("%s: corpus totals %s bytes, COMPRESSION.md says %s", r.cols[i], got, want[r.cols[i]])
		}
	}
}
