package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func press(m uiModel, keys ...tea.KeyPressMsg) uiModel {
	for _, k := range keys {
		next, _ := m.Update(k)
		m = next.(uiModel)
	}
	return m
}

func send(m uiModel, msg tea.Msg) (uiModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(uiModel), cmd
}

func resized(m uiModel, w, h int) uiModel {
	m, _ = send(m, tea.WindowSizeMsg{Width: w, Height: h})
	return m
}

var (
	keyDown     = tea.KeyPressMsg{Code: tea.KeyDown}
	keyUp       = tea.KeyPressMsg{Code: tea.KeyUp}
	keyTab      = tea.KeyPressMsg{Code: tea.KeyTab}
	keyShiftTab = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	keyEnter    = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc      = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyPgDown   = tea.KeyPressMsg{Code: tea.KeyPgDown}
	keyCtrlC    = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
)

func letter(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

var ansiCodes = regexp.MustCompile("\x1b\\[[0-9;]*m")

// screen is the text on screen, without the colour codes.
func screen(m uiModel) string { return ansiCodes.ReplaceAllString(m.View().Content, "") }

// words is the screen with all runs of spaces and line breaks made one
// space, to find a sentence that wraps.
func words(m uiModel) string { return strings.Join(strings.Fields(screen(m)), " ") }

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// fits checks that nothing on screen is wider than the window, where the
// terminal would cut it off, and that the screen is no taller than it.
func fits(t *testing.T, what string, m uiModel) {
	t.Helper()
	lines := strings.Split(m.View().Content, "\n")
	if len(lines) > m.height {
		t.Errorf("%s at %dx%d: %d lines", what, m.width, m.height, len(lines))
	}
	for _, l := range lines {
		if w := lipgloss.Width(l); w > m.width {
			t.Errorf("%s at %dx%d: a line is %d wide: %q", what, m.width, m.height, w, ansiCodes.ReplaceAllString(l, ""))
		}
	}
}

func TestUIEmptyAndBrokenLogs(t *testing.T) {
	dir := t.TempDir()
	m := newUI(filepath.Join(dir, "none.jsonl"), nil, nil, "")
	for _, want := range []string{"No log at", "Sessions appear here once b2trace proxy has recorded one", "b2trace ui path/to/sessions.jsonl"} {
		if !strings.Contains(words(m), want) {
			t.Errorf("missing log: no %q in:\n%s", want, screen(m))
		}
	}
	empty := filepath.Join(dir, "empty.jsonl")
	os.WriteFile(empty, nil, 0o644)
	if got := words(newUI(empty, nil, nil, "")); !strings.Contains(got, "has no sessions yet. Press r to read it again.") {
		t.Errorf("empty log: %s", got)
	}
	ps5 := filepath.Join(dir, "ps5.jsonl")
	os.WriteFile(ps5, []byte{0xff, 0xfe, '{', 0}, 0o644)
	if got := words(newUI(ps5, nil, nil, "")); !strings.Contains(got, "Cannot read") || !strings.Contains(got, "make it with b2trace share -o instead") {
		t.Errorf("UTF-16 log, the fix must be on screen: %s", got)
	}
	m = press(newUI(empty, nil, nil, ""), letter('s'))
	if m.sharing || !m.statusBad || !strings.Contains(words(m), "There are no sessions to share yet.") {
		t.Errorf("share with nothing to share: sharing %v, status %q", m.sharing, m.status)
	}
}

func TestUIChoosesSessions(t *testing.T) {
	m := resized(newUI("testdata/sessions.jsonl", nil, nil, ""), 80, 20)
	if m.tab != tabSessions {
		t.Fatalf("without the proxy the UI should open on Sessions, got tab %d", m.tab)
	}
	if got := words(m); !strings.Contains(got, "session 2 of 2: ardop") || !strings.Contains(got, "remote N0RMTE") {
		t.Errorf("should open on the newest session: %s", got)
	}
	m = press(m, keyUp, keyUp)
	if m.cursor != 0 || !strings.Contains(m.detail.GetContent(), "session 1 of 2: vara") {
		t.Errorf("after two ups: cursor %d", m.cursor)
	}
	if m = press(m, letter('G')); m.cursor != 1 {
		t.Errorf("G: cursor %d, want the newest", m.cursor)
	}
	if m = press(m, letter('g')); m.cursor != 0 {
		t.Errorf("g: cursor %d, want the oldest", m.cursor)
	}
	if m = press(m, letter('j')); m.cursor != 1 {
		t.Errorf("j: cursor %d", m.cursor)
	}
	if m = press(m, keyPgDown); m.detail.YOffset() == 0 {
		t.Error("pgdown did not scroll the summary")
	}
	m = press(m, letter('r'))
	if m.statusBad || m.status != "Read 2 sessions from testdata/sessions.jsonl." {
		t.Errorf("r: status %q", m.status)
	}
}

func TestUITabsAndTyping(t *testing.T) {
	m := newUI("testdata/sessions.jsonl", nil, nil, "")
	if m = press(m, keyTab); m.tab != tabCompress || !m.compressIn.Focused() {
		t.Fatalf("tab from Sessions: tab %d, input focused %v", m.tab, m.compressIn.Focused())
	}
	// what would be view keys elsewhere are typed into the path
	m = press(m, letter('1'), letter('q'), letter('t'))
	if m.tab != tabCompress || m.compressIn.Value() != "1qt" {
		t.Errorf("typing in Compress: tab %d, value %q", m.tab, m.compressIn.Value())
	}
	if m = press(m, keyEsc); m.compressIn.Value() != "" || m.tab != tabCompress {
		t.Errorf("esc should clear the path: %q", m.compressIn.Value())
	}
	m, _ = send(m, tea.PasteMsg{Content: "pasted/folder"})
	if m.compressIn.Value() != "pasted/folder" {
		t.Errorf("a paste reached the path as %q", m.compressIn.Value())
	}
	if m = press(m, keyShiftTab); m.tab != tabSessions || m.compressIn.Focused() {
		t.Errorf("shift+tab from Compress: tab %d", m.tab)
	}
	if m = press(m, keyShiftTab); m.tab != tabLive || !strings.Contains(words(m), "Not recording.") {
		t.Errorf("Live without the proxy: %s", words(m))
	}
	if m = press(m, letter('3')); m.tab != tabCompress {
		t.Errorf("3 should open Compress, got tab %d", m.tab)
	}
	if _, cmd := send(press(m, keyShiftTab), letter('q')); !quits(cmd) {
		t.Error("q in Sessions without the proxy should quit")
	}
	if _, cmd := send(m, keyCtrlC); !quits(cmd) {
		t.Error("ctrl+c in Compress should quit")
	}
}

func TestUIShare(t *testing.T) {
	dir := t.TempDir()
	m := press(newUI("testdata/sessions.jsonl", nil, nil, ""), letter('s'))
	if !m.sharing || m.shareTo.Value() != "testdata/sessions-shared.jsonl" {
		t.Fatalf("share prompt: sharing %v, suggested %q", m.sharing, m.shareTo.Value())
	}
	if m = press(m, keyEsc); m.sharing {
		t.Fatal("esc did not cancel")
	}

	m = press(m, letter('s'))
	m.shareTo.SetValue("")
	if m = press(m, keyEnter); !m.sharing || !strings.Contains(m.status, "Give a file") {
		t.Errorf("empty name: sharing %v, status %q", m.sharing, m.status)
	}
	m.shareTo.SetValue("testdata/sessions.jsonl")
	if m = press(m, keyEnter); !m.sharing || !strings.Contains(m.status, "That is the log itself") {
		t.Errorf("sharing over the log: sharing %v, status %q", m.sharing, m.status)
	}
	bad := filepath.Join(dir, "no such dir", "x.jsonl")
	m.shareTo.SetValue(bad)
	if m = press(m, keyEnter); !m.sharing || !m.statusBad || !strings.Contains(m.status, "Could not share") || m.shareTo.Value() != bad {
		t.Errorf("unwritable path: sharing %v, status %q, value %q", m.sharing, m.status, m.shareTo.Value())
	}

	to := filepath.Join(dir, "shared.jsonl")
	os.WriteFile(to, []byte("keep me\n"), 0o644)
	m.shareTo.SetValue(to)
	m = press(m, keyEnter)
	if kept, _ := os.ReadFile(to); string(kept) != "keep me\n" || !strings.Contains(m.status, "already exists") {
		t.Fatalf("an existing file was replaced without asking; status %q", m.status)
	}
	m = press(m, keyEnter)
	if m.sharing || m.statusBad || !strings.Contains(m.status, "Wrote 2 sessions") {
		t.Fatalf("second enter: sharing %v, status %q", m.sharing, m.status)
	}
	shared, err := readSessions(to)
	if err != nil || len(shared) != 2 || !shared[0].Shared || shared[0].RemoteCall != "" {
		t.Errorf("shared file: %d sessions, err %v", len(shared), err)
	}
}

func TestUICompress(t *testing.T) {
	dir := t.TempDir()
	for i := range 4 {
		body := fmt.Sprintf("Subject: test %d\r\n\r\nShelter count is %d, water and power are fine, no traffic.\r\n", i, 10*i)
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("m%d.b2f", i)), []byte(body), 0o644)
	}
	m := press(newUI("testdata/sessions.jsonl", nil, nil, ""), keyTab)
	m.compressIn.SetValue(dir)
	m, cmd := send(m, keyEnter)
	if !m.compressing || cmd == nil || !strings.Contains(words(m), "Compressing.") {
		t.Fatal("enter did not start compress")
	}
	if _, again := send(m, keyEnter); again != nil {
		t.Error("enter while compressing started a second run")
	}
	m, _ = send(m, cmd())
	got := words(m)
	if m.compressing || m.compressErr != nil || !strings.Contains(got, "median vs lzhuf") || !strings.Contains(got, "total") {
		t.Errorf("after compress: err %v, screen %s", m.compressErr, got)
	}

	// a path that looks like a flag must not end the program
	for _, path := range []string{"-h", "-x", filepath.Join(dir, "missing")} {
		m.compressIn.SetValue(path)
		m, cmd = send(m, keyEnter)
		m, _ = send(m, cmd())
		if !strings.Contains(words(m), "Could not compress:") {
			t.Errorf("path %q: %s", path, words(m))
		}
	}
}

func TestUITheme(t *testing.T) {
	m := newUI("testdata/sessions.jsonl", nil, nil, "")
	if m.bg != bgUnknown {
		t.Errorf("before the terminal answers, the background should be unknown, got %d", m.bg)
	}
	if m, _ = send(m, tea.BackgroundColorMsg{Color: color.White}); m.bg != bgLight {
		t.Error("a white background should switch to the light colours")
	}
	if m, _ = send(m, tea.BackgroundColorMsg{Color: color.Black}); m.bg != bgDark {
		t.Error("a black background should switch to the dark colours")
	}
	// ANSI white, bubbles' default for the prompt, is near invisible on light backgrounds
	for _, bg := range []color.Color{nil, color.White} {
		m := newUI("testdata/sessions.jsonl", nil, nil, "")
		if bg != nil {
			m, _ = send(m, tea.BackgroundColorMsg{Color: bg})
		}
		raw := press(m, keyTab).View().Content
		if strings.Contains(raw, "[37m") || strings.Contains(raw, ";37m") {
			t.Errorf("the Compress prompt is drawn in ANSI white on background %v: %q", bg, raw)
		}
	}
}

func TestUIFollowsNewSessions(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "sessions.jsonl")
	data, err := os.ReadFile("testdata/sessions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(logPath, data, 0o644)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := newTracker("vara", "", log.New(io.Discard, "", 0), appendSession(f))
	m := press(newUI(logPath, tr, newLineLog(10), ""), letter('2'))
	older := press(m, keyUp)
	tr.command(0, inbound, "CONNECTED N0CALL N0GATE 2300")
	tr.stop()
	if m = tick1(m); m.cursor != 2 {
		t.Errorf("on the newest session, a new one should be selected; cursor %d of %d", m.cursor, len(m.sessions))
	}
	if older = tick1(older); older.cursor != 0 || len(older.sessions) != 3 {
		t.Errorf("reading an older session, a new one should not move the cursor; cursor %d of %d", older.cursor, len(older.sessions))
	}
}

// liveUI is a UI on a tracker with no proxy around it, as runLive makes it.
func liveUI(t *testing.T, modem string, save func(*session) error) (uiModel, *tracker, *lineLog, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "sessions.jsonl")
	if save == nil {
		f, err := os.Create(logPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		save = appendSession(f)
	}
	logs := newLineLog(50)
	tr := newTracker(modem, "", log.New(logs, "", 0), save)
	tr.grace = time.Hour
	return newUI(logPath, tr, logs, "relaying VARA on 127.0.0.1:8300"), tr, logs, logPath
}

func tick1(m uiModel) uiModel {
	m, _ = send(m, tickMsg(time.Now()))
	return m
}

func TestUILive(t *testing.T) {
	m, tr, _, _ := liveUI(t, "vara", nil)
	if m.tab != tabLive {
		t.Fatalf("with the proxy the UI should open on Live, got tab %d", m.tab)
	}
	m = tick1(resized(m, 120, 50))
	if got := words(m); !strings.Contains(got, "Waiting for a connection.") || !strings.Contains(got, "No sessions saved yet") {
		t.Errorf("before a connection: %s", got)
	}
	if _, cmd := send(m, letter('q')); !quits(cmd) {
		t.Error("q with no session in progress should quit at once")
	}

	tr.command(0, outbound, "MYCALL N0CALL")
	tr.command(0, inbound, "CONNECTED N0CALL N0GATE 2300")
	tr.data(0, inbound, []byte("[WL2K-5.0-B2FWIHJM$]\r"))
	tr.command(0, inbound, "PTT ON")
	tr.data(0, outbound, []byte("FS +\r"))
	time.Sleep(30 * time.Millisecond)
	m = tick1(m)
	got := words(m)
	for _, want := range []string{"Connected to N0GATE as N0CALL", "PTT ON transmitting", "Now: answer, your station sending",
		"Turns 2, turnarounds 1", "handshake", "idle", "CONNECTED N0CALL N0GATE 2300"} {
		if !strings.Contains(got, want) {
			t.Errorf("live view lacks %q: %s", want, got)
		}
	}
	if m.live.cur.LocalTXMs < 30 {
		t.Errorf("local TX %d ms while PTT has been on for 30 ms", m.live.cur.LocalTXMs)
	}

	// 80x24 has room for one table at a time, and still the log
	m = resized(m, 80, 24)
	got = words(m)
	for _, want := range []string{"Connected to N0GATE", "PTT ON", "Now: answer", "time seconds share", "idle", "Log"} {
		if !strings.Contains(got, want) {
			t.Errorf("80x24 live view lacks %q:\n%s", want, screen(m))
		}
	}
	if m = press(m, letter('t')); !strings.Contains(words(m), "bytes out in both share") || strings.Contains(words(m), "time seconds share") {
		t.Errorf("t should switch to the byte table:\n%s", screen(m))
	}

	// quitting would cut the session off, so it asks first
	m, cmd := send(m, letter('q'))
	if quits(cmd) || !strings.Contains(words(m), "Press q again to stop the proxy") {
		t.Fatalf("first q during a session: %s", words(m))
	}
	if m, cmd = send(m, letter('t')); quits(cmd) || m.confirmQuit {
		t.Error("another key should keep the session")
	}
	m, _ = send(m, keyCtrlC)
	if _, cmd = send(m, keyCtrlC); !quits(cmd) {
		t.Error("ctrl+c twice should quit")
	}

	tr.command(0, inbound, "PTT OFF")
	tr.stop()
	m = tick1(resized(m, 120, 50))
	got = words(m)
	if !strings.Contains(got, "PTT off") || !strings.Contains(got, "Saved 1 this run") || len(m.sessions) != 1 {
		t.Errorf("after the session was saved (%d read): %s", len(m.sessions), got)
	}

	m, _ = send(m, proxyErrMsg{errors.New("accepting on 127.0.0.1:8300: too many open files")})
	if !strings.Contains(words(resized(m, 60, 30)), "The proxy stopped: accepting on 127.0.0.1:8300: too many open files") {
		t.Errorf("the whole proxy error should be on screen:\n%s", screen(m))
	}
}

func TestUILiveAGWPEAndSaveErrors(t *testing.T) {
	m, tr, _, _ := liveUI(t, "agwpe", func(*session) error { return errors.New("disk full") })
	tr.command(1, inbound, "C 0 N0GATE N0CALL *** CONNECTED With Station N0GATE")
	m = tick1(m)
	if got := words(m); !strings.Contains(got, "PTT not reported over AGWPE") || strings.Contains(got, "PTT off") || strings.Contains(got, "local TX") {
		t.Errorf("AGWPE live view claims a PTT state: %s", got)
	}
	tr.stop()
	if got := words(tick1(m)); !strings.Contains(got, "Could not write the last session to the log: disk full") {
		t.Errorf("a failed write is not shown: %s", got)
	}
}

// Every view, in every state, at sizes from small to large.
func TestUIFitsTheWindow(t *testing.T) {
	dir := t.TempDir()
	long := filepath.Join(dir, strings.Repeat("a-long-folder-name/", 5), "sessions.jsonl")
	states := map[string]uiModel{
		"missing log": newUI(long, nil, nil, ""),
		"sessions":    newUI("testdata/sessions.jsonl", nil, nil, ""),
		"sharing":     press(newUI("testdata/sessions.jsonl", nil, nil, ""), letter('s')),
		"compress":    press(newUI("testdata/sessions.jsonl", nil, nil, ""), keyTab),
		"status":      press(newUI("testdata/sessions.jsonl", nil, nil, ""), letter('r')),
	}
	m := states["compress"]
	m, _ = send(m, compressDoneMsg{err: errors.New("open " + long + ": The system cannot find the path specified.")})
	states["compress error"] = m
	live, tr, logs, _ := liveUI(t, "vara", nil)
	states["live waiting"] = tick1(live)
	io.WriteString(logs, "a log line that is long enough to wrap twice on a narrow window, and then some more words\n")
	tr.command(0, inbound, "CONNECTED N0CALL N0GATE 2300")
	tr.data(0, inbound, []byte("[WL2K-5.0-B2FWIHJM$]\r"))
	states["live session"] = tick1(live)
	m, _ = send(tick1(live), proxyErrMsg{errors.New("accepting on " + long + ": too many open files")})
	states["live error"] = m
	states["live quitting"] = press(tick1(live), letter('q'))

	for name, m := range states {
		for _, size := range [][2]int{{60, 16}, {80, 24}, {100, 30}, {140, 40}} {
			fits(t, name, resized(m, size[0], size[1]))
		}
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	tr, _ := newTestTracker("vara", time.Hour)
	tr.command(0, inbound, "CONNECTED N0CALL N0GATE 2300")
	tr.command(0, inbound, "PTT ON")
	tr.command(0, inbound, "PTT OFF")
	tr.data(0, inbound, []byte("[WL2K-5.0-B2FWIHJM$]\r"))
	snap := tr.snapshot(time.Now())
	tr.data(0, outbound, []byte("FF\r"))
	tr.command(0, inbound, "PTT ON")
	tr.command(0, inbound, "PTT OFF")
	if len(snap.cur.Turns) != 1 || snap.cur.TurnCount != 1 || len(snap.cur.PTT) != 1 || snap.dir != inbound || snap.phase != phaseHandshake {
		t.Errorf("the snapshot changed or is wrong: %d turns, count %d, %d PTT, %s %s",
			len(snap.cur.Turns), snap.cur.TurnCount, len(snap.cur.PTT), snap.dir, snap.phase)
	}
	if now := tr.snapshot(time.Now()); now.cur.TurnCount != 2 || len(now.cur.PTT) != 2 || now.phase != phaseEnd || now.dir != outbound {
		t.Errorf("second snapshot: %d turns, %d PTT, %s %s", now.cur.TurnCount, len(now.cur.PTT), now.dir, now.phase)
	}
}

// The UI reads snapshots while the relay goroutines write; CI runs this with
// the race detector.
func TestSnapshotWhileRelaying(t *testing.T) {
	tr, _ := newTestTracker("vara", time.Hour)
	tr.command(0, inbound, "CONNECTED N0CALL N0GATE 2300")
	var wg sync.WaitGroup
	for _, dir := range []direction{inbound, outbound} {
		wg.Go(func() {
			for i := range 3000 {
				tr.data(0, dir, []byte(fmt.Sprintf("; line %d\r", i)))
				tr.command(0, inbound, []string{"PTT ON", "PTT OFF", "BUFFER 10"}[i%3])
			}
		})
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	m := newUI(filepath.Join(t.TempDir(), "s.jsonl"), tr, newLineLog(10), "")
	for ticks := 0; ; ticks++ {
		m = tick1(m)
		_ = m.View()
		select {
		case <-done:
			if ticks > 0 {
				return
			}
		default:
		}
	}
}

// runLive stops the proxy when it is told to stop, and the session in
// progress is written.
func TestRunLiveStops(t *testing.T) {
	r := startLive(t, nil, newLineLog(50))
	time.Sleep(100 * time.Millisecond)
	r.stop()
	if err := r.wait(t); err != nil {
		t.Errorf("runLive returned %v when stopped from outside", err)
	}
	if c, err := net.DialTimeout("tcp", r.cmdLn.Addr().String(), time.Second); err == nil {
		c.Close()
		t.Error("the proxy still listens after runLive returned")
	}
	s, err := readSessions(r.log)
	if err != nil || len(s) != 1 || s[0].EndReason != "proxy stopped" {
		t.Errorf("log after stopping: %d sessions, err %v", len(s), err)
	}
}

func TestLineLog(t *testing.T) {
	l := newLineLog(3)
	io.WriteString(l, "one\ntw")
	io.WriteString(l, "o\r\nthree\nfour\nfive")
	if got := strings.Join(l.last(10), ","); got != "two,three,four" {
		t.Errorf("got %q, want the last three whole lines", got)
	}
	if got := strings.Join(l.last(1), ","); got != "four" {
		t.Errorf("last(1) = %q", got)
	}
}

func TestSnapshotKeepsItsTurns(t *testing.T) {
	tr, _ := newTestTracker("vara", time.Hour)
	tr.command(0, inbound, "CONNECTED N0CALL N0GATE 2300")
	tr.data(0, inbound, []byte("[WL2K-5.0-B2FWIHJM$]\r"))
	snap := tr.snapshot(time.Now())
	was := snap.cur.Turns[0].Bytes.total()
	// more bytes the same way add to the last turn in place
	tr.data(0, inbound, []byte("; more handshake text\r"))
	if got := snap.cur.Turns[0].Bytes.total(); got != was {
		t.Errorf("a snapshot's turn grew from %d to %d bytes after it was taken", was, got)
	}
}

func liveSession(t *testing.T) (uiModel, *lineLog) {
	m, tr, logs, _ := liveUI(t, "vara", nil)
	tr.command(0, inbound, "CONNECTED N0CALL N0GATE 2300")
	tr.data(0, inbound, []byte("[WL2K-5.0-B2FWIHJM$]\r"))
	return tick1(m), logs
}

func TestLiveTablesAtEveryWidth(t *testing.T) {
	m, logs := liveSession(t)
	for i := range 40 {
		fmt.Fprintf(logs, "12:00:00.000000 modem line %s\n", strings.Repeat("x", i))
	}
	for w := 60; w <= 140; w++ {
		for _, h := range []int{24, 40} {
			fits(t, "live session", resized(m, w, h))
		}
	}
	if got := words(resized(m, 140, 40)); !strings.Contains(got, "bytes out") || !strings.Contains(got, "time seconds") {
		t.Errorf("a wide window should show both tables: %s", got)
	}
	if got := screen(resized(m, 140, 40)); strings.Contains(got, "t bytes or time") {
		t.Error("the footer offers t while both tables are on screen")
	}
}

func TestLiveShowsNewestLogLine(t *testing.T) {
	m, logs := liveSession(t)
	for i := range 60 {
		fmt.Fprintf(logs, "log line %s #%d\n", strings.Repeat("x", i%7), i)
	}
	io.WriteString(logs, "NEWEST-LINE\n")
	m = tick1(resized(m, 80, 24))
	lines := strings.Split(screen(m), "\n")
	if len(lines) != 24 {
		t.Errorf("screen is %d lines, want the window's 24", len(lines))
	}
	if !strings.Contains(screen(m), "NEWEST-LINE") || !strings.Contains(screen(m), "#59") {
		t.Errorf("the newest log lines are not on screen:\n%s", screen(m))
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "quit") {
		t.Errorf("the footer is not on the last row: %q", last)
	}
}

func TestUIFooterOnLastRow(t *testing.T) {
	m := resized(newUI(filepath.Join(t.TempDir(), "none.jsonl"), nil, nil, ""), 80, 24)
	lines := strings.Split(screen(m), "\n")
	if len(lines) != 24 || !strings.Contains(lines[23], "quit") {
		t.Errorf("a short body leaves the footer on row %d of 24", len(lines))
	}
}

func TestCompressSummaryOnScreen(t *testing.T) {
	m := press(newUI("testdata/sessions.jsonl", nil, nil, ""), keyTab)
	var out strings.Builder
	out.WriteString("     raw   lzhuf  file\n")
	for i := range 80 {
		fmt.Fprintf(&out, "     100     90  f%d.b2f\n", i)
	}
	out.WriteString("    8000    7200  total\n  100.0%  90.0%  median vs lzhuf\n\n" +
		"zstd+dict skipped: dictionary training needs more samples, and no content is repeated more than average across inputs\n")
	m, _ = send(m, compressDoneMsg{out: out.String()})
	for _, size := range [][2]int{{60, 16}, {80, 24}, {140, 40}} {
		m = resized(m, size[0], size[1])
		fits(t, "compress done", m)
		got := words(m)
		for _, want := range []string{"total", "median vs lzhuf", "repeated more than average across inputs", "f0.b2f"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q is not on screen at %dx%d:\n%s", want, size[0], size[1], screen(m))
			}
		}
		seen := map[string]bool{}
		for range 40 {
			for _, l := range strings.Split(screen(m), "\n") {
				seen[strings.TrimSpace(l)] = true
			}
			m = press(m, keyPgDown)
		}
		for i := range 80 {
			if row := fmt.Sprintf("100     90  f%d.b2f", i); !seen[row] {
				t.Errorf("paging at %dx%d never shows %q", size[0], size[1], row)
			}
		}
		m = press(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
		for m.compressOut.YOffset() > 0 {
			m = press(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
		}
	}
}

// Paging through a session must reach every line of its summary, also after
// a reload makes the list above it taller.
func TestUIPagesReachEveryLine(t *testing.T) {
	data, err := os.ReadFile("testdata/sessions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(data), "\n")
	logPath := filepath.Join(t.TempDir(), "sessions.jsonl")
	// at 60 columns the key help and the status line wrap to three lines
	for _, size := range [][2]int{{80, 24}, {60, 20}} {
		os.WriteFile(logPath, []byte(first+"\n"), 0o644)
		m := resized(newUI(logPath, nil, nil, ""), size[0], size[1])
		os.WriteFile(logPath, []byte(strings.Repeat(first+"\n", 10)), 0o644)
		m = press(m, letter('r'))

		seen := screen(m)
		for range 30 {
			m = press(m, keyPgDown)
			seen += screen(m)
		}
		for _, l := range strings.Split(m.detail.GetContent(), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.Contains(seen, l) {
				t.Errorf("paging at %dx%d never shows %q", size[0], size[1], l)
			}
		}
		if !strings.Contains(seen, "connect") || !strings.Contains(seen, "possible retries") {
			t.Errorf("paging at %dx%d misses the summary's rows", size[0], size[1])
		}
	}
}

func TestUIQuitQuestionTakesTheKey(t *testing.T) {
	m, _ := liveSession(t)
	m = press(m, letter('3'), letter('a'), letter('b'))
	m = press(m, keyCtrlC, keyEsc)
	if m.confirmQuit || m.compressIn.Value() != "ab" {
		t.Errorf("esc after ctrl+c: asking %v, path %q", m.confirmQuit, m.compressIn.Value())
	}
	m = press(m, keyCtrlC, letter('x'))
	if m.compressIn.Value() != "ab" {
		t.Errorf("the key that kept the session was also typed: %q", m.compressIn.Value())
	}
}

// bubbles v2.2.1 panicked here, which took the proxy down with the UI.
func TestUIDeleteWordAtTheEnd(t *testing.T) {
	m := press(newUI("testdata/sessions.jsonl", nil, nil, ""), keyTab)
	for _, k := range []tea.KeyPressMsg{{Code: 'd', Mod: tea.ModAlt}, {Code: tea.KeyDelete, Mod: tea.ModCtrl}} {
		m.compressIn.SetValue("abc")
		m = press(m, tea.KeyPressMsg{Code: tea.KeyLeft}, k)
		if got := m.compressIn.Value(); !strings.HasPrefix("abc", got) || got == "abc" {
			t.Errorf("%s with the cursor on the last letter left %q", k, got)
		}
	}
}

func TestUISharePastedAndOddPaths(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("testdata/sessions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "sessions [old].jsonl")
	os.WriteFile(logPath, data, 0o644)
	m := press(newUI(logPath, nil, nil, ""), letter('s'))
	m.shareTo.SetValue("")
	to := filepath.Join(dir, "out.jsonl")
	m, _ = send(m, tea.PasteMsg{Content: `"` + to + `"`})
	m = press(m, keyEnter)
	if shared, err := readSessions(to); err != nil || len(shared) != 2 {
		t.Errorf("sharing a log named with brackets to a pasted, quoted path: status %q, err %v", m.status, err)
	}
}

type liveRun struct {
	cmdLn net.Listener
	log   string
	stop  context.CancelFunc
	ran   chan error
}

// startLive runs the proxy and its UI with no terminal, with a session
// connected, until stop.
func startLive(t *testing.T, save func(*session) error, logs *lineLog) liveRun {
	t.Helper()
	r := liveRun{log: filepath.Join(t.TempDir(), "sessions.jsonl"), ran: make(chan error, 1)}
	if save == nil {
		f, err := os.Create(r.log)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		save = appendSession(f)
	}
	p, err := newProxy("vara", "", "127.0.0.1:1", log.New(io.Discard, "", 0), save)
	if err != nil {
		t.Fatal(err)
	}
	cmdLn, dataLn := listenAdjacent(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.cmdLn, r.stop = cmdLn, cancel
	go func() {
		r.ran <- runLive(ctx, p, cmdLn, dataLn, r.log, logs, "relaying", tea.WithInput(nil), tea.WithOutput(io.Discard))
	}()
	p.tr.command(0, inbound, "CONNECTED N0CALL N0GATE 2300")
	return r
}

func (r liveRun) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.ran:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("runLive did not return")
	}
	return nil
}

func TestRunLiveReportsALostSession(t *testing.T) {
	r := startLive(t, func(*session) error { return errors.New("disk full") }, newLineLog(50))
	time.Sleep(100 * time.Millisecond)
	r.stop()
	if err := r.wait(t); err == nil || !strings.Contains(err.Error(), "not written") || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("a session lost at quit was not reported: %v", err)
	}
}

func TestRunLiveAfterAPanic(t *testing.T) {
	// with no log to read, the live view panics on its first frame
	r := startLive(t, nil, nil)
	if err := r.wait(t); !errors.Is(err, tea.ErrProgramPanic) {
		t.Errorf("a UI panic was hidden: %v", err)
	}
	if s, err := readSessions(r.log); err != nil || len(s) != 1 {
		t.Errorf("the session was not saved after the UI panicked: %d sessions, err %v", len(s), err)
	}
}
