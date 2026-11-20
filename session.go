package main

import (
	"fmt"
	"time"
)

// direction says which way bytes went through the proxy: outbound is host to
// modem (sent by this station), inbound is modem to host (sent by the remote).
type direction int

const (
	outbound direction = iota
	inbound
)

func (d direction) String() string {
	if d == outbound {
		return "out"
	}
	return "in"
}

func (d direction) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *direction) UnmarshalText(b []byte) error {
	switch string(b) {
	case "out":
		*d = outbound
	case "in":
		*d = inbound
	default:
		return fmt.Errorf("unknown direction %q", b)
	}
	return nil
}

// phase is the part of the B2F protocol a byte belongs to. See classify.
type phase int

const (
	phaseHandshake phase = iota
	phasePending
	phaseProposals
	phaseAnswer
	phasePayload
	phaseEnd
	phaseError
	phaseOther
)

type phaseBytes struct {
	Handshake int64 `json:"handshake"`
	Pending   int64 `json:"pending"`
	Proposals int64 `json:"proposals"`
	Answer    int64 `json:"answer"`
	Payload   int64 `json:"payload"`
	End       int64 `json:"end"`
	Error     int64 `json:"error"`
	Other     int64 `json:"other"`
}

func (b *phaseBytes) field(p phase) *int64 {
	switch p {
	case phaseHandshake:
		return &b.Handshake
	case phasePending:
		return &b.Pending
	case phaseProposals:
		return &b.Proposals
	case phaseAnswer:
		return &b.Answer
	case phasePayload:
		return &b.Payload
	case phaseEnd:
		return &b.End
	case phaseError:
		return &b.Error
	}
	return &b.Other
}

func (b *phaseBytes) add(p phase, n int) { *b.field(p) += int64(n) }

func (b phaseBytes) plus(o phaseBytes) phaseBytes {
	for p := phaseHandshake; p <= phaseOther; p++ {
		*b.field(p) += *o.field(p)
	}
	return b
}

func (b phaseBytes) total() int64 {
	return b.Handshake + b.Pending + b.Proposals + b.Answer + b.Payload + b.End + b.Error + b.Other
}

// session is one modem connection, CONNECTED to DISCONNECTED, as written to
// the log. Only local PTT times are exact; GapMs is an estimate of remote
// transmission plus turnaround dead time.
type session struct {
	Modem       string         `json:"modem"`
	Link        string         `json:"link,omitempty"`
	Shared      bool           `json:"shared,omitempty"`
	Start       time.Time      `json:"start"`
	End         time.Time      `json:"end"`
	EndReason   string         `json:"end_reason"`
	ConnectLine string         `json:"connect_line"`
	ConnectMs   int64          `json:"connect_ms,omitempty"`
	LocalCall   string         `json:"local_call,omitempty"`
	RemoteCall  string         `json:"remote_call,omitempty"`
	SIDOut      string         `json:"sid_out,omitempty"`
	SIDIn       string         `json:"sid_in,omitempty"`
	BytesOut    phaseBytes     `json:"bytes_out"`
	BytesIn     phaseBytes     `json:"bytes_in"`
	TurnCount   int            `json:"turn_count"`
	Turnarounds int            `json:"turnarounds"`
	Turns       []turn         `json:"turns"`
	LocalTXMs   int64          `json:"local_tx_ms"`
	PTT         []pttInterval  `json:"ptt"`
	GapMs       []int64        `json:"gap_ms"`
	Buffer      []bufferSample `json:"buffer"`
	BufferUnit  string         `json:"buffer_unit,omitempty"`
	States      []stateLine    `json:"states"`
	Commands    []commandLine  `json:"commands"`
	Proposals   []proposal     `json:"proposals"`
	Messages    []message      `json:"messages"`
	Errors      []streamError  `json:"errors"`
}

// turn is a run of B2F data in one direction. Its times are when the bytes
// passed the proxy, not when they were on the air.
type turn struct {
	Dir       direction  `json:"dir"`
	FirstByte time.Time  `json:"first_byte"`
	LastByte  time.Time  `json:"last_byte"`
	Bytes     phaseBytes `json:"bytes"`
}

type pttInterval struct {
	On        time.Time `json:"on"`
	Off       time.Time `json:"off"`
	LocalTXMs int64     `json:"local_tx_ms"`
}

type bufferSample struct {
	T     time.Time `json:"t"`
	Bytes int       `json:"bytes"`
}

type stateLine struct {
	T    time.Time `json:"t"`
	Line string    `json:"line"`
}

// commandLine is one command-port line, or one AGWPE frame other than
// connected data.
type commandLine struct {
	T    time.Time `json:"t"`
	Dir  direction `json:"dir"`
	Line string    `json:"line"`
}

// proposal is one FA to FE line and the answer the other side gave it.
// Offset is the resume offset of an accepted "!" answer.
type proposal struct {
	Dir            direction `json:"dir"`
	Code           string    `json:"code"`
	MID            string    `json:"mid,omitempty"`
	Size           int       `json:"size,omitempty"`
	CompressedSize int       `json:"compressed_size,omitempty"`
	Optimistic     bool      `json:"optimistic,omitempty"`
	Answer         string    `json:"answer,omitempty"`
	Offset         int       `json:"offset,omitempty"`
}

// message is one binary transfer. CompressedSize counts the data bytes in
// this transfer, which start at Offset when the transfer is a resume.
type message struct {
	Dir            direction `json:"dir"`
	Title          string    `json:"title"`
	Offset         int       `json:"offset"`
	CompressedSize int       `json:"compressed_size"`
	Complete       bool      `json:"complete"`
}

type streamError struct {
	T    time.Time `json:"t"`
	Dir  direction `json:"dir"`
	Text string    `json:"text"`
}

// stamp trims a time to what the log keeps. Durations are taken before this,
// from the monotonic clock.
func stamp(t time.Time) time.Time { return t.Truncate(time.Millisecond) }
