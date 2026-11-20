package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func runProxy(args []string) error {
	flags := newFlags("proxy", "--modem vara|ardop|agwpe --target host:port [flags]")
	modem := flags.String("modem", "", "modem type: vara, ardop, or agwpe for packet")
	listen := flags.String("listen", "", "address Pat connects to; for vara and ardop the data port is the next one\n(default 127.0.0.1:8300 for vara, 127.0.0.1:8515 for ardop, 127.0.0.1:8000 for agwpe)")
	target := flags.String("target", "", "address the modem was moved to")
	logPath := flags.String("log", "sessions.jsonl", "file that gets one JSON line per session")
	link := flags.String("link", "", "label kept with each session to group results by, such as varahf, varafm or packet1200")
	flags.Parse(args)
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q; proxy only takes flags", flags.Arg(0))
	}

	switch *modem {
	case "vara":
		*listen = cmp.Or(*listen, "127.0.0.1:8300")
	case "ardop":
		*listen = cmp.Or(*listen, "127.0.0.1:8515")
	case "agwpe":
		*listen = cmp.Or(*listen, "127.0.0.1:8000")
	default:
		return errors.New("--modem must be vara, ardop or agwpe")
	}
	if *target == "" {
		return errors.New("--target is required: the address the modem now listens on, for example 127.0.0.1:18300")
	}
	if *target == *listen {
		return errors.New("--target and --listen are the same address; move the modem to other ports")
	}

	f, err := os.OpenFile(*logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	logger := log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
	p, err := newProxy(*modem, *link, *target, logger, appendSession(f))
	if err != nil {
		return err
	}
	var cmdLn, dataLn net.Listener
	if p.modem == "agwpe" {
		if cmdLn, err = net.Listen("tcp", *listen); err != nil {
			return fmt.Errorf("cannot listen on %s (is the TNC still using it?): %w", *listen, err)
		}
		logger.Printf("relaying AGWPE on %s to %s, sessions go to %s", cmdLn.Addr(), p.cmdTarget, *logPath)
	} else {
		if cmdLn, dataLn, err = listenPair(*listen); err != nil {
			return err
		}
		logger.Printf("relaying %s on %s and %s to %s and %s, sessions go to %s",
			p.name, cmdLn.Addr(), dataLn.Addr(), p.cmdTarget, p.dataTarget, *logPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return p.serve(ctx, cmdLn, dataLn)
}

func appendSession(f *os.File) func(*session) error {
	return func(s *session) error {
		b, err := json.Marshal(s)
		if err != nil {
			return err
		}
		_, err = f.Write(append(b, '\n'))
		return err
	}
}

// listenPair listens on addr and on the next port, like the modems do.
func listenPair(addr string) (cmd, data net.Listener, err error) {
	dataAddr, err := nextPort(addr)
	if err != nil {
		return nil, nil, err
	}
	cmd, err = net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot listen on %s (is the modem still using it?): %w", addr, err)
	}
	data, err = net.Listen("tcp", dataAddr)
	if err != nil {
		cmd.Close()
		return nil, nil, fmt.Errorf("cannot listen on %s (is the modem still using it?): %w", dataAddr, err)
	}
	return cmd, data, nil
}

// nextPort returns addr with the port after it, where modems put their data port.
func nextPort(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65534 {
		return "", fmt.Errorf("%s: the port must be a number from 1 to 65534", addr)
	}
	return net.JoinHostPort(host, strconv.Itoa(n+1)), nil
}

// proxy relays Pat's modem connections and shows a copy of everything to the
// tracker. It never changes the bytes it forwards. VARA and ARDOP use a
// command and a data connection; AGWPE carries both on one.
type proxy struct {
	name       string
	modem      string
	cmdTarget  string
	dataTarget string
	tr         *tracker
	log        *log.Logger
	// numbers the AGWPE connections, which the tracker tells apart
	conns atomic.Int64
}

func newProxy(modem, link, target string, logger *log.Logger, save func(*session) error) (*proxy, error) {
	p := &proxy{
		name:      strings.ToUpper(modem),
		modem:     modem,
		cmdTarget: target,
		tr:        newTracker(modem, link, logger, save),
		log:       logger,
	}
	if modem != "agwpe" {
		var err error
		if p.dataTarget, err = nextPort(target); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// serve runs until ctx is done or a listener fails, then writes out the
// session in progress. dataLn is nil for AGWPE.
func (p *proxy) serve(ctx context.Context, cmdLn, dataLn net.Listener) error {
	lns := []net.Listener{cmdLn}
	errc := make(chan error, 2)
	if p.modem == "agwpe" {
		go func() { errc <- p.accept(cmdLn, p.relayAGWPE) }()
	} else {
		lns = append(lns, dataLn)
		go func() { errc <- p.accept(cmdLn, p.relayCommand) }()
		go func() { errc <- p.accept(dataLn, p.relayData) }()
	}
	var err error
	select {
	case <-ctx.Done():
	case err = <-errc:
	}
	for _, ln := range lns {
		ln.Close()
	}
	p.tr.stop()
	return err
}

func (p *proxy) accept(ln net.Listener, relay func(net.Conn)) error {
	for {
		c, err := ln.Accept()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("accepting on %s: %w", ln.Addr(), err)
		}
		go relay(c)
	}
}

func (p *proxy) relayCommand(client net.Conn) {
	fromHost, fromModem := &lineSplitter{}, &lineSplitter{}
	p.relay(client, p.cmdTarget, "command", 0,
		func(b []byte) {
			for _, l := range fromHost.write(b) {
				p.tr.command(0, outbound, l)
			}
		},
		func(b []byte) {
			for _, l := range fromModem.write(b) {
				p.tr.command(0, inbound, l)
			}
		})
}

func (p *proxy) relayData(client net.Conn) {
	p.relay(client, p.dataTarget, "data", 0, p.dataObserver(outbound), p.dataObserver(inbound))
}

func (p *proxy) relayAGWPE(client net.Conn) {
	conn := int(p.conns.Add(1))
	p.relay(client, p.cmdTarget, "API", conn, p.agwObserver(conn, outbound), p.agwObserver(conn, inbound))
}

// agwObserver passes connected data to the B2F parser and every other frame
// to the tracker as a command line.
func (p *proxy) agwObserver(conn int, dir direction) func([]byte) {
	frames := &agwSplitter{}
	return func(b []byte) {
		fs, err := frames.write(b)
		if err != nil {
			side := "Pat to TNC"
			if dir == inbound {
				side = "TNC to Pat"
			}
			p.log.Printf("AGWPE %s: %v", side, err)
		}
		for _, f := range fs {
			if f.kind == 'D' {
				p.tr.data(conn, dir, f.data)
			} else {
				p.tr.command(conn, dir, f.line())
			}
		}
	}
}

func (p *proxy) dataObserver(dir direction) func([]byte) {
	if p.modem != "ardop" {
		return func(b []byte) { p.tr.data(0, dir, b) }
	}
	frames := &frameSplitter{}
	return func(b []byte) {
		for _, f := range frames.write(b) {
			if dir == inbound {
				var ok bool
				if f, ok = arqData(f); !ok {
					continue
				}
			}
			p.tr.data(0, dir, f)
		}
	}
}

// relay connects client to the modem port at addr and copies both ways until
// either side closes.
func (p *proxy) relay(client net.Conn, addr, port string, conn int, fromHost, fromModem func([]byte)) {
	modem, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		p.log.Printf("cannot reach the %s %s port at %s (is the modem running there?): %v", p.name, port, addr, err)
		client.Close()
		return
	}
	p.log.Printf("%s port: %s connected", port, client.RemoteAddr())
	done := make(chan struct{}, 2)
	go func() { copyObserved(modem, client, fromHost); done <- struct{}{} }()
	go func() { copyObserved(client, modem, fromModem); done <- struct{}{} }()
	<-done
	client.Close()
	modem.Close()
	<-done
	p.log.Printf("%s port: %s disconnected", port, client.RemoteAddr())
	p.tr.connClosed(conn, port+" connection")
}

// copyObserved copies src to dst. Each chunk goes to observe before it is
// forwarded, so the tracker has seen it before the other side can react.
func copyObserved(dst, src net.Conn, observe func([]byte)) {
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			observe(buf[:n])
			if _, err := dst.Write(buf[:n]); err != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// lineSplitter cuts a command port stream into lines. Both modems end every
// line with \r.
type lineSplitter struct {
	buf []byte
}

func (s *lineSplitter) write(b []byte) []string {
	var lines []string
	for _, c := range b {
		if c != '\r' {
			s.buf = append(s.buf, c)
			continue
		}
		if l := strings.TrimSpace(string(s.buf)); l != "" {
			lines = append(lines, l)
		}
		s.buf = s.buf[:0]
	}
	return lines
}
