package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type tab int

const (
	tabLive tab = iota
	tabSessions
	tabCompress
)

var tabNames = []string{"Live", "Sessions", "Compress"}

// background is the terminal's background, as far as the UI knows it.
type background int

const (
	bgUnknown background = iota
	bgLight
	bgDark
)

type tickMsg time.Time

type compressDoneMsg struct {
	out string
	err error
}

type proxyErrMsg struct{ err error }

// uiModel is the terminal UI: a live view of the proxy, a browser for the
// logged sessions with share, and compress.
type uiModel struct {
	tab           tab
	width, height int
	bg            background
	st            styles

	// The live view. tr is nil when the UI runs without the proxy.
	tr       *tracker
	logs     *lineLog
	relay    string
	live     liveState
	proxyErr error
	// bytes instead of time, when the window has room for one table only
	showBytes   bool
	confirmQuit bool

	logPath  string
	sessions []session
	loadErr  error
	cursor   int
	detail   viewport.Model
	// the selected session's summary, before wrapping to the pane
	detailText string
	// live.saved when the log was last read, to read it again after a save
	loadedAt int

	sharing bool
	shareTo textinput.Model
	// a file that exists and that enter, pressed again, will replace
	replace   string
	status    string
	statusBad bool

	compressIn  textinput.Model
	compressOut viewport.Model
	// the per-file table, and the header and total rows shown above it
	compressText, compressSum string
	compressing               bool
	compressErr               error
	compressed                bool
}

func newUI(logPath string, tr *tracker, logs *lineLog, relay string) uiModel {
	m := uiModel{tr: tr, logs: logs, relay: relay, logPath: logPath, width: 80, height: 24}
	m.shareTo = textinput.New()
	m.shareTo.Prompt = "Write to: "
	m.compressIn = textinput.New()
	m.compressIn.Prompt = "File or folder: "
	m.compressIn.Placeholder = "for example a Pat mailbox folder"
	m.detail = viewport.New()
	m.compressOut = viewport.New()
	m.setTheme(bgUnknown)
	if tr == nil {
		m.tab = tabSessions
	}
	m.load()
	m.newest()
	m.resize()
	return m
}

func (m uiModel) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, tick())
}

func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// load reads the session log again and keeps the selection in range.
func (m *uiModel) load() {
	m.sessions, m.loadErr = readSessions(m.logPath)
	m.cursor = min(max(m.cursor, 0), max(len(m.sessions)-1, 0))
	m.showSession()
}

func (m *uiModel) newest() {
	m.cursor = max(len(m.sessions)-1, 0)
	m.showSession()
}

func (m *uiModel) showSession() {
	m.detailText = ""
	if len(m.sessions) > 0 {
		var b bytes.Buffer
		printSession(&b, fmt.Sprintf("session %d of %d", m.cursor+1, len(m.sessions)), m.sessions[m.cursor])
		m.detailText = b.String()
	}
	m.detail.SetContent(wrap(m.detailText, m.detail.Width()))
	m.detail.GotoTop()
}

// wrap breaks long lines between words to fit width, so nothing is cut off
// on a narrow terminal.
func wrap(s string, width int) string {
	return lipgloss.NewStyle().Width(max(width, 1)).Render(s)
}

// para wraps s to the window and styles each line on its own, so a style's
// padding never runs into the text after it.
func (m uiModel) para(st lipgloss.Style, s string) string {
	lines := strings.Split(wrap(s, m.width), "\n")
	for i, l := range lines {
		lines[i] = st.Render(strings.TrimRight(l, " "))
	}
	return strings.Join(lines, "\n")
}

// setTheme picks colours for a light or dark background, or for an unknown
// one when the terminal has not said.
func (m *uiModel) setTheme(bg background) {
	m.bg = bg
	m.st = newStyles(bg)
	// bubbles' defaults draw the prompt in ANSI white, which vanishes on a
	// light background
	in := textinput.DefaultStyles(bg != bgLight)
	in.Focused.Prompt, in.Blurred.Prompt = lipgloss.NewStyle(), lipgloss.NewStyle()
	in.Focused.Placeholder, in.Blurred.Placeholder = m.st.muted, m.st.muted
	in.Cursor.Color = nil
	m.shareTo.SetStyles(in)
	m.compressIn.SetStyles(in)
}

// resize fits the scrolling panes into what the window has left once the
// tabs, the key help and the lines above each pane are drawn.
func (m *uiModel) resize() {
	m.shareTo.SetWidth(max(m.width-12, 10))
	m.compressIn.SetWidth(max(m.width-18, 10))
	body := max(m.height-2-lipgloss.Height(m.footer()), 3)
	listW := 0
	if m.width >= 100 {
		listW = 40
	}
	m.detail.SetWidth(max(m.width-listW-2, 20))
	above := 0
	if listW == 0 {
		above = min(len(m.sessions), 6)
	}
	m.detail.SetHeight(max(body-above-1, 3))
	m.compressOut.SetWidth(m.width)
	m.compressOut.SetHeight(max(body-3-lipgloss.Height(m.compressSummary()), 3))
	m.detail.SetContent(wrap(m.detailText, m.detail.Width()))
	m.compressOut.SetContent(wrap(m.compressText, m.compressOut.Width()))
}

// Update handles msg, then sizes the panes again, since a reload, a status
// line or a compress result can change what is around them.
func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	n := next.(uiModel)
	n.resize()
	return n, cmd
}

func (m uiModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.BackgroundColorMsg:
		if msg.IsDark() {
			m.setTheme(bgDark)
		} else {
			m.setTheme(bgLight)
		}
	case tickMsg:
		if m.tr != nil {
			m.live = m.tr.snapshot(time.Time(msg))
			if m.live.saved != m.loadedAt {
				m.loadedAt = m.live.saved
				// follow new sessions, unless an older one is being read
				onNewest, off := m.cursor >= len(m.sessions)-1, m.detail.YOffset()
				m.load()
				if onNewest {
					m.newest()
				} else {
					m.detail.SetYOffset(off)
				}
			}
		}
		return m, tick()
	case proxyErrMsg:
		m.proxyErr = msg.err
	case compressDoneMsg:
		m.compressDone(msg)
	case tea.KeyPressMsg:
		return m.key(msg)
	default:
		// pastes and the inputs' own messages go to the input in use
		var cmd tea.Cmd
		switch {
		case m.sharing:
			m.shareTo, cmd = m.shareTo.Update(msg)
		case m.tab == tabCompress:
			m.compressIn, cmd = m.compressIn.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

func (m uiModel) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if m.confirmQuit {
		m.confirmQuit, m.status = false, ""
		if key == "q" || key == "ctrl+c" {
			return m, tea.Quit
		}
		// the key only answered the question
		return m, nil
	}
	if key == "ctrl+c" {
		return m.quit()
	}
	if m.sharing {
		return m.shareKey(k)
	}
	switch key {
	case "tab":
		m.switchTo((m.tab + 1) % 3)
		return m, nil
	case "shift+tab":
		m.switchTo((m.tab + 2) % 3)
		return m, nil
	}
	if m.tab == tabCompress {
		return m.compressKey(k)
	}
	switch key {
	case "q":
		return m.quit()
	case "1", "2", "3":
		m.switchTo(tab(key[0] - '1'))
	case "t":
		if m.tab == tabLive {
			m.showBytes = !m.showBytes
		}
	}
	if m.tab == tabSessions {
		m.sessionsKey(key)
	}
	return m, nil
}

// quit ends the UI, after asking first when that would cut off a session.
func (m uiModel) quit() (tea.Model, tea.Cmd) {
	if m.tr != nil && m.live.cur != nil {
		m.confirmQuit = true
		m.status, m.statusBad = "A session is in progress. Press q again to stop the proxy and cut it off, or any other key to keep it.", true
		return m, nil
	}
	return m, tea.Quit
}

func (m *uiModel) switchTo(t tab) {
	m.tab = t
	m.status = ""
	if t == tabCompress {
		m.compressIn.Focus()
	} else {
		m.compressIn.Blur()
	}
}

func (m *uiModel) sessionsKey(key string) {
	switch key {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
			m.showSession()
		}
	case "down", "j":
		if m.cursor < len(m.sessions)-1 {
			m.cursor++
			m.showSession()
		}
	case "home", "g":
		m.cursor = 0
		m.showSession()
	case "end", "G":
		m.newest()
	case "pgdown", "space":
		m.detail.PageDown()
	case "pgup":
		m.detail.PageUp()
	case "r":
		m.load()
		m.status, m.statusBad = fmt.Sprintf("Read %d sessions from %s.", len(m.sessions), m.logPath), false
	case "s":
		if len(m.sessions) == 0 {
			m.status, m.statusBad = "There are no sessions to share yet.", true
			return
		}
		m.sharing, m.replace, m.status = true, "", ""
		m.shareTo.SetValue(sharedName(m.logPath))
		m.shareTo.CursorEnd()
		m.shareTo.Focus()
	}
}

// sharedName suggests where the shared copy of a log goes: next to it, with
// -shared before the extension.
func sharedName(logPath string) string {
	ext := filepath.Ext(logPath)
	return strings.TrimSuffix(logPath, ext) + "-shared" + ext
}

func (m uiModel) shareKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.sharing = false
		m.shareTo.Blur()
		m.status, m.statusBad = "Share cancelled.", false
		return m, nil
	case "enter":
		m.share(cleanPath(m.shareTo.Value()))
		return m, nil
	}
	var cmd tea.Cmd
	m.shareTo, cmd = m.shareTo.Update(k)
	m.replace = ""
	return m, cmd
}

// share writes the shared copy to to. The prompt stays open until it has,
// so a mistyped path can be fixed.
func (m *uiModel) share(to string) {
	if to == "" {
		m.status, m.statusBad = "Give a file to write the shared copy to.", true
		return
	}
	if dst, err := os.Stat(to); err == nil {
		if src, err := os.Stat(m.logPath); err == nil && os.SameFile(src, dst) {
			m.status, m.statusBad = "That is the log itself. Write the shared copy to another file.", true
			return
		}
		if m.replace != to {
			m.replace = to
			m.status, m.statusBad = to+" already exists. Press enter again to replace it.", true
			return
		}
	}
	// read again rather than share m.sessions: strip changes the slices in
	// place, and the log is not a pattern the way share's arguments are
	sessions, err := readSessions(m.logPath)
	if err == nil {
		err = createShared(to, sessions)
	}
	if err != nil {
		m.status, m.statusBad = "Could not share: "+err.Error(), true
		return
	}
	m.sharing, m.replace = false, ""
	m.shareTo.Blur()
	m.status, m.statusBad = fmt.Sprintf("Wrote %d sessions to %s. Read it before you share it.", len(sessions), to), false
}

// cleanPath trims what a pasted path brings along: spaces, and the quotes
// Windows Explorer's "Copy as path" adds.
func cleanPath(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"`)
}

func (m uiModel) compressKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		path := cleanPath(m.compressIn.Value())
		if m.compressing || path == "" {
			return m, nil
		}
		m.compressing = true
		m.compressErr = nil
		return m, compressCmd(path)
	case "pgdown":
		m.compressOut.PageDown()
		return m, nil
	case "pgup":
		m.compressOut.PageUp()
		return m, nil
	case "esc":
		m.compressIn.SetValue("")
		return m, nil
	}
	var cmd tea.Cmd
	m.compressIn, cmd = m.compressIn.Update(k)
	return m, cmd
}

// compressCmd runs b2trace compress on path off the UI's goroutine. The --
// keeps a path that starts with - from being read as a flag.
func compressCmd(path string) tea.Cmd {
	return func() tea.Msg {
		var out, notes bytes.Buffer
		if err := runCompress([]string{"--", path}, &out, &notes); err != nil {
			return compressDoneMsg{err: err}
		}
		return compressDoneMsg{out: strings.TrimRight(out.String(), "\n") + "\n\n" + notes.String()}
	}
}

// compressDone shows a compress run: its header and total rows above, the
// row for each file in the scrolling pane below.
func (m *uiModel) compressDone(msg compressDoneMsg) {
	m.compressing, m.compressed, m.compressErr = false, true, msg.err
	m.compressText, m.compressSum = msg.out, ""
	table, notes, _ := strings.Cut(msg.out, "\n\n")
	if lines := strings.Split(table, "\n"); len(lines) >= 3 {
		m.compressSum = lines[0] + "\n" + strings.Join(lines[len(lines)-2:], "\n")
		if notes != "" {
			m.compressSum += "\n" + strings.TrimRight(notes, "\n")
		}
	}
	m.compressOut.SetContent(wrap(m.compressText, m.compressOut.Width()))
	m.compressOut.GotoTop()
}

func (m uiModel) compressSummary() string {
	if m.compressSum == "" {
		return ""
	}
	return m.para(lipgloss.NewStyle(), m.compressSum)
}

func (m uiModel) View() tea.View {
	footer := m.footer()
	body := []string{m.header(), ""}
	switch m.tab {
	case tabLive:
		body = append(body, strings.Split(m.liveView(), "\n")...)
	case tabSessions:
		body = append(body, strings.Split(m.sessionsView(), "\n")...)
	case tabCompress:
		body = append(body, strings.Split(m.compressView(), "\n")...)
	}
	// keep the help on screen and on the last rows, whatever the body's length
	limit := max(m.height-lipgloss.Height(footer), 1)
	if len(body) > limit {
		body = body[:limit]
	}
	for len(body) < limit {
		body = append(body, "")
	}
	v := tea.NewView(strings.Join(body, "\n") + "\n" + footer)
	v.AltScreen = true
	v.WindowTitle = "b2trace"
	return v
}

func (m uiModel) header() string {
	parts := []string{m.st.title.Render("b2trace")}
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if tab(i) == m.tab {
			parts = append(parts, m.st.tabOn.Render("["+label+"]"))
		} else {
			parts = append(parts, m.st.tabOff.Render(" "+label+" "))
		}
	}
	return strings.Join(parts, " ")
}

func (m uiModel) footer() string {
	quit := "q quit"
	if m.tr != nil {
		quit = "q quit and stop the proxy"
	}
	var keys string
	switch {
	case m.sharing:
		keys = "enter write  esc cancel"
	case m.tab == tabSessions:
		keys = "up/down pick  pgup/pgdn scroll  s share  r reload  tab view  " + quit
	case m.tab == tabCompress:
		keys = "enter run  esc clear  pgup/pgdn scroll  tab view  ctrl+c quit"
	case m.tr != nil && m.live.cur != nil && !m.bothTables():
		keys = "t bytes or time  tab view  " + quit
	default:
		keys = "tab view  " + quit
	}
	line := m.para(m.st.muted, keys)
	if m.status != "" {
		s := m.st.good
		if m.statusBad {
			s = m.st.bad
		}
		line = m.para(s, m.status) + "\n" + line
	}
	return line
}

func (m uiModel) liveView() string {
	if m.tr == nil {
		return m.para(m.st.muted, "Not recording. To watch sessions as they happen, start the proxy with --ui:") +
			"\n\n" + m.para(m.st.label, "b2trace proxy --modem vara --target 127.0.0.1:18300 --ui") +
			"\n\n" + m.para(m.st.muted, "The Sessions view shows what is already in the log.")
	}
	var lines []string
	add := func(s string) { lines = append(lines, s) }
	if m.proxyErr != nil {
		add(m.para(m.st.bad, "The proxy stopped: "+m.proxyErr.Error()))
	}
	l := m.live
	if l.saveErr != nil {
		add(m.para(m.st.bad, "Could not write the last session to the log: "+l.saveErr.Error()))
	}
	if l.cur == nil {
		add(m.para(m.st.muted, m.relay))
		add(m.st.label.Render("Waiting for a connection."))
	} else {
		s := l.cur
		who := cmp.Or(s.RemoteCall, "an unknown station")
		if s.LocalCall != "" {
			who += " as " + s.LocalCall
		}
		add(m.para(m.st.good, fmt.Sprintf("Connected to %s, %s", who, s.End.Sub(s.Start).Round(time.Second))))
	}
	switch {
	case m.tr.modem == "agwpe":
		add(m.st.muted.Render("PTT not reported over AGWPE"))
	case l.ptt:
		add(m.st.onAir.Render("PTT ON") + "  transmitting")
	default:
		add(m.st.label.Render("PTT off"))
	}
	if l.cur != nil {
		if l.data {
			who := "remote sending"
			if l.dir == outbound {
				who = "your station sending"
			}
			add(fmt.Sprintf("Now: %s, %s", l.phase, who))
		}
		st := sessionStats(*l.cur)
		tx := fmt.Sprintf(", local TX %.1f s", float64(st.localTXMs)/1000)
		if m.tr.modem == "agwpe" {
			tx = ""
		}
		add(m.para(lipgloss.NewStyle(), fmt.Sprintf("Turns %d, turnarounds %d%s, messages %d out and %d in, errors %d",
			st.turns, st.turnarounds, tx, st.msgsOut, st.msgsIn, st.errors)))
	}
	if l.saved == 0 {
		add(m.st.muted.Render("No sessions saved yet in this run."))
	} else {
		ls := l.lastSaved
		add(m.para(lipgloss.NewStyle(), fmt.Sprintf("Saved %d this run; the last with %s, %s, ended: %s", l.saved,
			cmp.Or(ls.RemoteCall, "an unknown station"), ls.End.Sub(ls.Start).Round(time.Second), ls.EndReason)))
	}
	if l.cur != nil {
		add("")
		add(m.liveTables())
	}
	text := strings.Join(lines, "\n")
	// the newest log lines that fit, each wrapped rather than cut off
	room := m.height - lipgloss.Height(m.footer()) - 2 - lipgloss.Height(text) - 2
	if room < 1 {
		return text
	}
	var shown []string
	recent := m.logs.last(room)
	for i := len(recent) - 1; i >= 0 && len(shown) < room; i-- {
		shown = append(strings.Split(m.para(m.st.muted, recent[i]), "\n"), shown...)
	}
	shown = shown[max(len(shown)-room, 0):]
	return text + "\n\n" + m.st.label.Render("Log") + "\n" + strings.Join(shown, "\n")
}

// liveTables shows the byte and time tables side by side, or the one picked
// with t when the window is narrower than both.
func (m uiModel) liveTables() string {
	b, t := m.tables()
	switch {
	case m.bothTables():
		return lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(lipgloss.Width(b)+2).Render(b), t)
	case m.showBytes:
		return b
	}
	return t
}

func (m uiModel) bothTables() bool {
	b, t := m.tables()
	return lipgloss.Width(b)+2+lipgloss.Width(t) <= m.width
}

// tables prints the byte and time tables of the session in progress. The
// time so far since the last byte is idle, not a disconnect yet.
func (m uiModel) tables() (string, string) {
	st := sessionStats(*m.live.cur)
	var bt, tt bytes.Buffer
	st.printBytes(&bt)
	st.time.print(&tt, "idle")
	return strings.TrimRight(bt.String(), "\n"), strings.TrimRight(tt.String(), "\n")
}

func (m uiModel) sessionsView() string {
	if m.loadErr != nil {
		if errors.Is(m.loadErr, fs.ErrNotExist) {
			return m.para(m.st.muted, fmt.Sprintf("No log at %s yet.", m.logPath)) + "\n\n" +
				m.para(lipgloss.NewStyle(), "Sessions appear here once b2trace proxy has recorded one. To open another log:") +
				"\n\n" + m.para(m.st.label, "b2trace ui path/to/sessions.jsonl")
		}
		return m.para(m.st.bad, fmt.Sprintf("Cannot read %s: %v", m.logPath, m.loadErr))
	}
	if len(m.sessions) == 0 {
		return m.para(m.st.muted, fmt.Sprintf("%s has no sessions yet. Press r to read it again.", m.logPath))
	}
	var top string
	if m.sharing {
		// above the list, so a short window never hides what is typed
		top = m.shareTo.View() + "\n" + m.para(m.st.muted, "Removes callsigns, locators, addresses, titles and MIDs.") + "\n\n"
	}
	list := m.sessionList()
	if m.width >= 100 {
		left := lipgloss.NewStyle().Width(40).Render(list)
		return top + lipgloss.JoinHorizontal(lipgloss.Top, left, " ", m.detail.View())
	}
	return top + list + "\n" + m.detail.View()
}

// sessionList shows the sessions around the selected one, newest last.
func (m uiModel) sessionList() string {
	rows := 6
	if m.width >= 100 {
		rows = max(m.height-6, 3)
	}
	first := min(max(m.cursor-rows/2, 0), max(len(m.sessions)-rows, 0))
	var b strings.Builder
	for i := first; i < min(first+rows, len(m.sessions)); i++ {
		s := m.sessions[i]
		line := fmt.Sprintf("%3d %s %-8s %s", i+1, s.Start.Format("01-02 15:04"),
			cmp.Or(s.RemoteCall, cmp.Or(s.Link, s.Modem)), s.End.Sub(s.Start).Round(time.Second))
		if i == m.cursor {
			b.WriteString(m.st.selected.Render("> "+line) + "\n")
		} else {
			b.WriteString("  " + line + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (m uiModel) compressView() string {
	parts := []string{m.compressIn.View()}
	switch {
	case m.compressing:
		parts = append(parts, m.para(m.st.label, "Compressing. brotli at quality 11 takes a moment on large folders."))
	case m.compressErr != nil:
		parts = append(parts, m.para(m.st.bad, "Could not compress: "+m.compressErr.Error()))
	case !m.compressed:
		parts = append(parts, m.para(m.st.muted, "Compares LZHUF, gzip, brotli and zstd, with and without a trained dictionary, "+
			"on every file under the path. Sizes include each format's header."))
	default:
		parts = append(parts, "", m.compressSummary(), "", m.compressOut.View())
	}
	return strings.Join(parts, "\n")
}

// styles are the UI's colours, picked for the terminal's background. Text
// keeps the terminal's own colour, and every state also has a word, so
// nothing depends on colour alone.
type styles struct {
	title, tabOn, tabOff, label, muted, good, bad, onAir, selected lipgloss.Style
}

// newStyles picks colours for bg, or mid tones that read on light and dark
// alike when the terminal has not said what its background is.
func newStyles(bg background) styles {
	pick := func(light, dark, unknown string) color.Color {
		switch bg {
		case bgLight:
			return lipgloss.Color(light)
		case bgDark:
			return lipgloss.Color(dark)
		}
		return lipgloss.Color(unknown)
	}
	amber := pick("#9A3412", "#FBBF24", "#D97706")
	green := pick("#166534", "#4ADE80", "#16A34A")
	red := pick("#B91C1C", "#F87171", "#DC2626")
	muted := pick("#57534E", "#A8A29E", "#808080")
	return styles{
		title:    lipgloss.NewStyle().Bold(true).Foreground(amber),
		tabOn:    lipgloss.NewStyle().Bold(true).Underline(true),
		tabOff:   lipgloss.NewStyle().Foreground(muted),
		label:    lipgloss.NewStyle().Bold(true),
		muted:    lipgloss.NewStyle().Foreground(muted),
		good:     lipgloss.NewStyle().Foreground(green),
		bad:      lipgloss.NewStyle().Bold(true).Foreground(red),
		onAir:    lipgloss.NewStyle().Bold(true).Foreground(amber),
		selected: lipgloss.NewStyle().Bold(true).Foreground(amber),
	}
}

// runLive runs the proxy with the UI in front of it. Quitting the UI stops
// the proxy, and a proxy that stops on its own says why in the UI.
func runLive(ctx context.Context, p *proxy, cmdLn, dataLn net.Listener, logPath string, logs *lineLog, relaying string, opts ...tea.ProgramOption) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	prog := tea.NewProgram(newUI(logPath, p.tr, logs, relaying), append(opts, tea.WithContext(ctx))...)
	served := make(chan error, 1)
	go func() {
		err := p.serve(ctx, cmdLn, dataLn)
		if err != nil {
			prog.Send(proxyErrMsg{err})
		}
		served <- err
	}()
	_, uiErr := prog.Run()
	// stopped from outside, by Ctrl+C on the shell or the caller's context
	if ctx.Err() != nil && errors.Is(uiErr, tea.ErrProgramKilled) {
		uiErr = nil
	}
	cancel()
	err := errors.Join(<-served, uiErr)
	// the UI that would have shown a failed last write is gone by now
	if saveErr := p.tr.snapshot(time.Now()).saveErr; saveErr != nil {
		err = errors.Join(err, fmt.Errorf("the last session was not written to %s: %w", logPath, saveErr))
	}
	return err
}

func runUI(args []string) error {
	flags := newFlags("ui", "[sessions.jsonl]")
	flags.Parse(args)
	if flags.NArg() > 1 {
		return errors.New("ui takes at most one log file")
	}
	logPath := "sessions.jsonl"
	if flags.NArg() == 1 {
		logPath = flags.Arg(0)
	}
	_, err := tea.NewProgram(newUI(logPath, nil, nil, "")).Run()
	if errors.Is(err, tea.ErrInterrupted) {
		return nil
	}
	return err
}
