package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/la5nta/wl2k-go/lzhuf"
)

func writeMessages(t *testing.T, dir string, n int) [][]byte {
	t.Helper()
	var texts [][]byte
	for i := range n {
		text := []byte(b2Message(fmt.Sprintf("MID%09d", i), fmt.Sprintf("Net report %d", i), 80+10*i))
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("msg%d.b2f", i)), text, 0o644); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, text)
	}
	return texts
}

func compressCSV(t *testing.T, args ...string) (rows [][]string, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := runCompress(append([]string{"--csv"}, args...), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&out).ReadAll()
	if err != nil {
		t.Fatalf("output is not CSV: %v\n%s", err, out.String())
	}
	return rows, errOut.String()
}

func TestCompress(t *testing.T) {
	dir := t.TempDir()
	texts := writeMessages(t, dir, 8)
	// A copy of a message must not count twice or end up in both halves.
	if err := os.WriteFile(filepath.Join(dir, "copy.b2f"), texts[0], 0o644); err != nil {
		t.Fatal(err)
	}

	rows, stderr := compressCSV(t, dir)
	wantHeader := []string{"file", "raw", "lzhuf", "gzip", "brotli", "zstd", "zstd+dict"}
	if !slices.Equal(rows[0], wantHeader) {
		t.Fatalf("header %q, want %q", rows[0], wantHeader)
	}
	if !strings.Contains(stderr, "left out duplicates (same content as another file): 1") {
		t.Errorf("stderr does not report the duplicate: %q", stderr)
	}
	files := rows[1 : len(rows)-2]
	if len(files) != len(texts) {
		t.Fatalf("%d file rows, want %d", len(files), len(texts))
	}
	rawTotal := 0
	for _, row := range files {
		raw, _ := strconv.Atoi(row[1])
		rawTotal += raw
		for i, cell := range row[2:] {
			n, err := strconv.Atoi(cell)
			if err != nil || n <= 0 || n >= raw {
				t.Errorf("%s: %s is %q, want a size above 0 and below raw %d", row[0], wantHeader[i+2], cell, raw)
			}
		}
	}
	total, medians := rows[len(rows)-2], rows[len(rows)-1]
	if total[0] != "total" || total[1] != strconv.Itoa(rawTotal) {
		t.Errorf("total row %q, want raw total %d", total, rawTotal)
	}
	if medians[0] != "median_vs_lzhuf" || medians[2] != "1.000" {
		t.Errorf("median row %q, want lzhuf at 1.000", medians)
	}

	// The LZHUF column must be what B2F sends, so wl2k-go has to decode it.
	for i, text := range texts {
		enc, err := lzhufB2(text)
		if err != nil {
			t.Fatal(err)
		}
		r, err := lzhuf.NewB2Reader(bytes.NewReader(enc))
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Errorf("message %d: %v", i, err)
		}
		if !bytes.Equal(got, text) {
			t.Errorf("message %d does not round-trip through LZHUF", i)
		}
	}
}

func TestCompressTooFewFilesForDictionary(t *testing.T) {
	dir := t.TempDir()
	writeMessages(t, dir, 1)
	rows, stderr := compressCSV(t, filepath.Join(dir, "msg0.b2f"))
	if want := []string{"file", "raw", "lzhuf", "gzip", "brotli", "zstd"}; !slices.Equal(rows[0], want) {
		t.Errorf("header %q, want %q", rows[0], want)
	}
	if !strings.Contains(stderr, "zstd+dict skipped") {
		t.Errorf("stderr does not say why the column is missing: %q", stderr)
	}
}
