package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"text/tabwriter"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/dict"
	"github.com/klauspost/compress/zstd"
	"github.com/la5nta/wl2k-go/lzhuf"
)

func runCompress(args []string, stdout, stderr io.Writer) error {
	flags := newFlags("compress", "[flags] <file or dir>...")
	dictSize := flags.Int("dict-size", 16384, "largest zstd dictionary to train, in bytes")
	asCSV := flags.Bool("csv", false, "write CSV instead of a table")
	flags.Parse(args)
	if flags.NArg() == 0 {
		return errors.New("compress needs at least one file or directory")
	}

	paths, err := findFiles(flags.Args())
	if err != nil {
		return err
	}
	names, files, dups, err := readUnique(paths)
	if err != nil {
		return err
	}
	if dups > 0 {
		fmt.Fprintf(stderr, "left out duplicates (same content as another file): %d\n", dups)
	}

	r, err := measure(names, files)
	if err != nil {
		return err
	}
	if sizes, dicts, err := dictSizes(files, *dictSize); err != nil {
		fmt.Fprintf(stderr, "zstd+dict skipped: %v\n", err)
	} else {
		// the builder stops when the samples run out of shared phrases, so
		// this can be well under --dict-size
		fmt.Fprintf(stderr, "zstd+dict: trained dictionaries of %d and %d bytes\n", dicts[0], dicts[1])
		r.addColumn("zstd+dict", sizes)
	}

	if *asCSV {
		return r.writeCSV(stdout)
	}
	return r.writeTable(stdout)
}

// findFiles lists the files given and the files under the directories given,
// sorted and without repeats.
func findFiles(args []string) ([]string, error) {
	var paths []string
	for _, arg := range args {
		err := filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no files in %v", args)
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

// readUnique reads the files, leaving out any with the same content as an
// earlier one: a copy in the other half would let the dictionary train on the
// file it is then tested on.
func readUnique(paths []string) (names []string, files [][]byte, dups int, err error) {
	seen := make(map[[sha256.Size]byte]bool)
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, 0, err
		}
		sum := sha256.Sum256(b)
		if seen[sum] {
			dups++
			continue
		}
		seen[sum] = true
		names = append(names, path)
		files = append(files, b)
	}
	return names, files, dups, nil
}

type compressor struct {
	name string
	run  func([]byte) ([]byte, error)
}

// measure compresses every file with every compressor except the dictionary one.
func measure(names []string, files [][]byte) (*report, error) {
	enc, err := newZstd(nil)
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	comps := []compressor{
		{"lzhuf", lzhufB2},
		{"gzip", gzipBest},
		{"brotli", brotliBest},
		{"zstd", func(b []byte) ([]byte, error) { return enc.EncodeAll(b, nil), nil }},
	}

	r := &report{cols: []string{"raw"}, names: names}
	for _, c := range comps {
		r.cols = append(r.cols, c.name)
	}
	for i, data := range files {
		row := []int{len(data)}
		for _, c := range comps {
			out, err := c.run(data)
			if err != nil {
				return nil, fmt.Errorf("%s with %s: %w", names[i], c.name, err)
			}
			row = append(row, len(out))
		}
		r.sizes = append(r.sizes, row)
	}
	return r, nil
}

// dictSizes compresses each file with a zstd dictionary trained on the other
// half of the files (split by sorted index), so no file is compressed with a
// dictionary built from itself.
func dictSizes(files [][]byte, maxSize int) (sizes []int, dicts [2]int, err error) {
	var halves [2][][]byte
	for i, f := range files {
		// The dictionary builder panics on very large samples, and a few KB of
		// each file is what it learns shared phrases from anyway.
		halves[i%2] = append(halves[i%2], f[:min(len(f), 64<<10)])
	}
	sizes = make([]int, len(files))
	for test := range 2 {
		train := halves[1-test]
		d, err := dict.BuildZstdDict(train, dict.Options{
			MaxDictSize: maxSize,
			HashBytes:   6,
			// A fixed ID, as large as the ones zstd itself picks, keeps the
			// frame header the same size on every run.
			ZstdDictID: 1 << 16,
			ZstdLevel:  zstd.EncoderLevelFromZstd(19),
		})
		if err != nil {
			return nil, dicts, fmt.Errorf("training a dictionary on %d files: %w", len(train), err)
		}
		dicts[test] = len(d)
		enc, err := newZstd(d)
		if err != nil {
			return nil, dicts, err
		}
		for i := test; i < len(files); i += 2 {
			sizes[i] = len(enc.EncodeAll(files[i], nil))
		}
		enc.Close()
	}
	return sizes, dicts, nil
}

// newZstd returns an encoder at zstd level 19, which klauspost/compress maps
// to its best level.
func newZstd(d []byte) (*zstd.Encoder, error) {
	opts := []zstd.EOption{zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(19))}
	if d != nil {
		opts = append(opts, zstd.WithEncoderDict(d))
	}
	return zstd.NewWriter(nil, opts...)
}

// lzhufB2 compresses the way B2F does, including the 6-byte CRC and size header.
func lzhufB2(data []byte) ([]byte, error) {
	return compressWith(data, func(w io.Writer) io.WriteCloser { return lzhuf.NewB2Writer(w) })
}

func gzipBest(data []byte) ([]byte, error) {
	return compressWith(data, func(w io.Writer) io.WriteCloser {
		zw, _ := gzip.NewWriterLevel(w, gzip.BestCompression) // fails only for an invalid level
		return zw
	})
}

func brotliBest(data []byte) ([]byte, error) {
	return compressWith(data, func(w io.Writer) io.WriteCloser {
		return brotli.NewWriterOptions(w, brotli.WriterOptions{Quality: 11, LGWin: 22})
	})
}

func compressWith(data []byte, newWriter func(io.Writer) io.WriteCloser) ([]byte, error) {
	var buf bytes.Buffer
	w := newWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// report holds the compressed size of every file in every column. Column 0 is
// the raw size and column 1 is LZHUF, which the medians compare against.
type report struct {
	cols  []string
	names []string
	sizes [][]int
}

func (r *report) addColumn(name string, sizes []int) {
	r.cols = append(r.cols, name)
	for i := range r.sizes {
		r.sizes[i] = append(r.sizes[i], sizes[i])
	}
}

func (r *report) totals() []int {
	t := make([]int, len(r.cols))
	for _, row := range r.sizes {
		for c, n := range row {
			t[c] += n
		}
	}
	return t
}

// medians returns, per column, the median over files of size / LZHUF size.
func (r *report) medians() []float64 {
	m := make([]float64, len(r.cols))
	for c := range r.cols {
		ratios := make([]float64, len(r.sizes))
		for i, row := range r.sizes {
			ratios[i] = float64(row[c]) / float64(row[1])
		}
		m[c] = median(ratios)
	}
	return m
}

func median(v []float64) float64 {
	slices.Sort(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

func (r *report) writeTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	line := func(cells []string, label string) {
		for _, c := range cells {
			fmt.Fprintf(tw, "%s\t", c)
		}
		fmt.Fprintf(tw, "  %s\n", label)
	}
	line(r.cols, "file")
	for i, row := range r.sizes {
		line(itoas(row), r.names[i])
	}
	line(itoas(r.totals()), "total")
	var med []string
	for _, m := range r.medians() {
		med = append(med, fmt.Sprintf("%.1f%%", 100*m))
	}
	line(med, "median vs lzhuf")
	return tw.Flush()
}

func (r *report) writeCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	cw.Write(append([]string{"file"}, r.cols...))
	for i, row := range r.sizes {
		cw.Write(append([]string{r.names[i]}, itoas(row)...))
	}
	cw.Write(append([]string{"total"}, itoas(r.totals())...))
	med := []string{"median_vs_lzhuf"}
	for _, m := range r.medians() {
		med = append(med, strconv.FormatFloat(m, 'f', 3, 64))
	}
	cw.Write(med)
	cw.Flush()
	return cw.Error()
}

func itoas(v []int) []string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return s
}
