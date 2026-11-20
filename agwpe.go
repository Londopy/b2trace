package main

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"fmt"
	"strings"
)

// agwFrame is one frame of the AGWPE TCP API that Direwolf and other packet
// TNCs offer: a 36-byte little-endian header, then the data.
type agwFrame struct {
	port     byte
	kind     byte
	from, to string
	data     []byte
}

// agwSplitter cuts an AGWPE TCP stream into frames.
type agwSplitter struct {
	buf    []byte
	broken bool
}

// write returns the frames that b completes. Like frameSplitter, later
// writes only append past the returned data. A length no AGWPE frame has
// means the stream is something else, such as a KISS port; the splitter
// then returns an error once and nothing after it.
func (s *agwSplitter) write(b []byte) ([]agwFrame, error) {
	if s.broken {
		return nil, nil
	}
	s.buf = append(s.buf, b...)
	var frames []agwFrame
	for len(s.buf) >= 36 {
		n := binary.LittleEndian.Uint32(s.buf[28:32])
		// TNCs send a few KB at most
		if n > 64<<10 {
			s.broken, s.buf = true, nil
			return frames, fmt.Errorf("a frame claims %d bytes of data, so this is not AGWPE; relaying it without recording", n)
		}
		end := 36 + int(n)
		if len(s.buf) < end {
			break
		}
		frames = append(frames, agwFrame{
			port: s.buf[0],
			kind: s.buf[4],
			from: agwCall(s.buf[8:18]),
			to:   agwCall(s.buf[18:28]),
			data: s.buf[36:end],
		})
		s.buf = s.buf[end:]
	}
	return frames, nil
}

func agwCall(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return strings.TrimSpace(string(b))
}

// line writes a frame other than connected data as a command line:
// "<kind> <port> <from> <to>", then the frame count of a "Y" or "y" answer,
// or the text of a "C" or "d" frame. Other data is only counted, since a
// login frame carries a password.
func (f agwFrame) line() string {
	l := fmt.Sprintf("%c %d %s %s", f.kind, f.port, cmp.Or(f.from, "-"), cmp.Or(f.to, "-"))
	switch {
	case (f.kind == 'Y' || f.kind == 'y') && len(f.data) >= 4:
		return fmt.Sprintf("%s %d", l, binary.LittleEndian.Uint32(f.data))
	case f.kind == 'C' || f.kind == 'd':
		if text := strings.Join(strings.Fields(strings.ReplaceAll(string(f.data), "\x00", " ")), " "); text != "" {
			return l + " " + text
		}
	case len(f.data) > 0:
		return fmt.Sprintf("%s (data length %d)", l, len(f.data))
	}
	return l
}
