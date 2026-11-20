package main

import (
	"fmt"
	"mime"
	"strconv"
	"strings"
)

const (
	nul = 0x00
	soh = 0x01
	stx = 0x02
	eot = 0x04
)

// A line this long is not B2F. It is still counted, but its text is not kept.
const maxText = 512

type parseState int

const (
	atElement parseState = iota
	inLine
	inHeaderLen
	inTitle
	inOffset
	atBlock
	inBlockLen
	inBlock
	inChecksum
)

// element is a classified piece of the stream: a text line, a count of
// payload bytes, the end of a binary transfer, or a protocol problem.
type element struct {
	phase phase
	bytes int
	line  string
	msg   *message
	err   string
}

// parser follows one direction of a B2F stream without taking part in it.
// It works byte by byte, so where the reads split the stream does not matter.
type parser struct {
	state parseState
	sawF  bool

	text []byte
	raw  int

	hdrLen int
	title  []byte
	offset []byte
	off    int
	size   int
	left   int
	sum    byte

	out     []element
	payload int
}

// write takes the next bytes of the stream and returns what they completed.
func (p *parser) write(b []byte) []element {
	for _, c := range b {
		p.step(c)
	}
	if p.payload > 0 {
		p.out = append(p.out, element{phase: phasePayload, bytes: p.payload})
	}
	return p.take()
}

// flush ends an element that the end of the session cut off.
func (p *parser) flush() []element {
	switch p.state {
	case atElement:
	case inLine:
		p.endLine()
	default:
		p.endTransfer("cut off before the end")
	}
	return p.take()
}

func (p *parser) take() []element {
	els := p.out
	p.out, p.payload = nil, 0
	return els
}

func (p *parser) step(c byte) {
	switch p.state {
	case atElement:
		if c == soh {
			p.payload++
			p.state = inHeaderLen
			p.title, p.offset = p.title[:0], p.offset[:0]
			p.off, p.size, p.sum = 0, 0, 0
			return
		}
		p.state = inLine
		p.lineByte(c)
	case inLine:
		p.lineByte(c)
	case atBlock:
		switch c {
		case stx:
			p.state = inBlockLen
		case eot:
			p.state = inChecksum
		default:
			p.endTransfer(fmt.Sprintf("unexpected byte 0x%02x between data blocks", c))
			p.step(c) // it starts the next element
			return
		}
		p.payload++
	default:
		p.payload++
		p.transferByte(c)
	}
}

func (p *parser) transferByte(c byte) {
	switch p.state {
	case inHeaderLen:
		p.hdrLen = int(c)
		p.state = inTitle
	case inTitle:
		if c == nul {
			p.state = inOffset
		} else if len(p.title) < maxText {
			p.title = append(p.title, c)
		}
	case inOffset:
		if c == nul {
			p.endHeader()
		} else if len(p.offset) < maxText {
			p.offset = append(p.offset, c)
		}
	case inBlockLen:
		p.left = int(c)
		if p.left == 0 {
			p.left = 256
		}
		p.state = inBlock
	case inBlock:
		p.size++
		p.sum += c
		p.left--
		if p.left == 0 {
			p.state = atBlock
		}
	case inChecksum:
		if p.sum+c != 0 {
			p.endTransfer("bad checksum")
		} else {
			p.endTransfer("")
		}
	}
}

func (p *parser) endHeader() {
	p.state = atBlock
	if n := len(p.title) + len(p.offset) + 2; n != p.hdrLen {
		p.problem(fmt.Sprintf("message %q: header length byte says %d, header has %d", p.title, p.hdrLen, n))
	}
	off, err := strconv.Atoi(string(p.offset))
	if err != nil {
		p.problem(fmt.Sprintf("message %q: offset %q is not a number", p.title, p.offset))
	}
	p.off = off
}

func (p *parser) problem(text string) {
	p.out = append(p.out, element{phase: phasePayload, err: text})
}

// endTransfer closes the current binary transfer. problem is empty when it
// ended with a good checksum.
func (p *parser) endTransfer(problem string) {
	m := &message{Title: decodeTitle(p.title), Offset: p.off, CompressedSize: p.size, Complete: problem == ""}
	el := element{phase: phasePayload, msg: m}
	if problem != "" {
		el.err = fmt.Sprintf("message %q: %s", m.Title, problem)
	}
	p.out = append(p.out, el)
	p.state = atElement
}

func (p *parser) lineByte(c byte) {
	p.raw++
	switch {
	case c == '\r':
		p.endLine()
	case c == '\n' || c == nul:
	case len(p.text) < maxText:
		p.text = append(p.text, c)
	}
}

func (p *parser) endLine() {
	line := string(p.text)
	ph := classify(line, p.sawF)
	switch ph {
	case phaseProposals, phaseAnswer, phaseEnd:
		p.sawF = true
	}
	el := element{phase: ph, bytes: p.raw, line: line}
	if ph == phaseError {
		el.err = line
	}
	p.out = append(p.out, el)
	p.text, p.raw = p.text[:0], 0
	p.state = atElement
}

// classify puts a text line in its protocol phase. sawF says whether this
// side has sent an F-command yet: before that, unknown lines are handshake
// (SID, ;FW, ;PQ, ;PR, MOTD and prompt lines), after it they are other.
func classify(line string, sawF bool) phase {
	switch {
	case strings.HasPrefix(line, "***"):
		return phaseError
	case strings.HasPrefix(line, ";PM"):
		return phasePending
	case len(line) >= 2 && line[0] == 'F' && (len(line) == 2 || line[2] == ' '):
		switch line[1] {
		case 'A', 'B', 'C', 'D', 'E', '>':
			return phaseProposals
		case 'S':
			return phaseAnswer
		case 'F', 'Q':
			return phaseEnd
		}
	}
	if sawF {
		return phaseOther
	}
	return phaseHandshake
}

// decodeTitle undoes the MIME word encoding Pat puts on titles. CMS sends
// raw subjects, which are kept as they are.
func decodeTitle(raw []byte) string {
	s := string(raw)
	if d, err := new(mime.WordDecoder).DecodeHeader(s); err == nil {
		return d
	}
	return s
}

// isSID reports whether a handshake line is a SID, like [PAT-0.17.0-B2FHM$].
// Every SID ends in "$]", which tells it apart from a bracketed banner line.
func isSID(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "$]")
}

// parseProposal reads an FA to FE line. FC, FD and FE carry
// "type MID size compressed-size flag"; the older FA and FB forms keep
// only their code. A flag of 1 marks an optimistic send in the Z extension.
func parseProposal(line string) proposal {
	f := strings.Fields(line)
	p := proposal{Code: f[0]}
	if len(f) < 5 || !strings.ContainsRune("CDE", rune(line[1])) {
		return p
	}
	p.MID = f[2]
	p.Size, _ = strconv.Atoi(f[3])
	p.CompressedSize, _ = strconv.Atoi(f[4])
	p.Optimistic = len(f) > 5 && f[5] == "1"
	return p
}

type answer struct {
	kind   string
	offset int
}

// parseAnswers reads an FS line, one answer per proposal: + accept,
// - reject, = defer, !<offset> accept from that offset, with the letter
// forms that older clients send. Anything else still takes its proposal's
// place, as "other".
func parseAnswers(line string) []answer {
	s := strings.TrimSpace(strings.TrimPrefix(line, "FS"))
	var answers []answer
	for len(s) > 0 {
		c := s[0]
		s = s[1:]
		switch c {
		case '+', 'Y', 'y':
			answers = append(answers, answer{kind: "accept"})
		case '-', 'N', 'n', 'R', 'r':
			answers = append(answers, answer{kind: "reject"})
		case '=', 'L', 'l', 'H', 'h':
			answers = append(answers, answer{kind: "defer"})
		case '!', 'A', 'a':
			n := 0
			for n < len(s) && s[n] >= '0' && s[n] <= '9' {
				n++
			}
			off, _ := strconv.Atoi(s[:n])
			s = s[n:]
			answers = append(answers, answer{kind: "resume", offset: off})
		case ' ':
		default:
			answers = append(answers, answer{kind: "other"})
		}
	}
	return answers
}
