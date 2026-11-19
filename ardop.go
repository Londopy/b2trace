package main

import "encoding/binary"

// frameSplitter cuts an ARDOP TCP data stream into frames. Each frame is a
// 2-byte big-endian length followed by that many bytes, in both directions.
type frameSplitter struct {
	buf []byte
}

// write returns the bodies of the frames that b completes. Later writes only
// append past them, so the returned slices stay valid.
func (s *frameSplitter) write(b []byte) [][]byte {
	s.buf = append(s.buf, b...)
	var frames [][]byte
	for len(s.buf) >= 2 {
		end := 2 + int(binary.BigEndian.Uint16(s.buf))
		if len(s.buf) < end {
			break
		}
		frames = append(frames, s.buf[2:end])
		s.buf = s.buf[end:]
	}
	return frames
}

// arqData returns the session data in a frame from the TNC. Those frames
// start with a 3-letter type, and only ARQ frames carry the B2F session.
func arqData(frame []byte) ([]byte, bool) {
	if len(frame) < 3 || string(frame[:3]) != "ARQ" {
		return nil, false
	}
	return frame[3:], true
}
