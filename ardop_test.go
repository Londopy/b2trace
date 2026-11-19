package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// tncFrame is a frame from the TNC to the host: length (counting the type),
// a 3-letter type, then the data.
func tncFrame(kind string, data []byte) []byte {
	b := binary.BigEndian.AppendUint16(nil, uint16(3+len(data)))
	return append(append(b, kind...), data...)
}

// hostFrame is a frame from the host to the TNC: length, then the data.
func hostFrame(data []byte) []byte {
	return append(binary.BigEndian.AppendUint16(nil, uint16(len(data))), data...)
}

func TestFrameSplitterFromTNC(t *testing.T) {
	// 300 bytes needs both length bytes.
	big := bytes.Repeat([]byte{0x02, 0x00, 0x7d}, 100)
	stream := bytes.Join([][]byte{
		tncFrame("ARQ", []byte("FS +\r")),
		tncFrame("IDF", []byte(" LA1B:[JP20QE] ")),
		tncFrame("ARQ", big),
		tncFrame("FEC", []byte("CQ CQ de LA1B")),
		tncFrame("ARQ", []byte("FQ\r")),
	}, nil)
	want := append(append([]byte("FS +\r"), big...), "FQ\r"...)

	for how, chunks := range splits(stream) {
		var s frameSplitter
		var got []byte
		for _, c := range chunks {
			for _, f := range s.write(c) {
				if d, ok := arqData(f); ok {
					got = append(got, d...)
				}
			}
		}
		if !bytes.Equal(got, want) {
			t.Errorf("read %s: got %q, want %q", how, got, want)
		}
	}
}

func TestFrameSplitterFromHost(t *testing.T) {
	payloads := [][]byte{[]byte("FF\r"), {}, bytes.Repeat([]byte{0xff}, 700)}
	var stream []byte
	for _, p := range payloads {
		stream = append(stream, hostFrame(p)...)
	}

	for how, chunks := range splits(stream) {
		var s frameSplitter
		var got [][]byte
		for _, c := range chunks {
			got = append(got, s.write(c)...)
		}
		if len(got) != len(payloads) {
			t.Fatalf("read %s: got %d frames, want %d", how, len(got), len(payloads))
		}
		for i := range payloads {
			if !bytes.Equal(got[i], payloads[i]) {
				t.Errorf("read %s: frame %d is %q, want %q", how, i, got[i], payloads[i])
			}
		}
	}
}
