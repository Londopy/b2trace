package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"
)

func runShare(args []string, stdout io.Writer) error {
	flags := newFlags("share", "[-o shared.jsonl] sessions.jsonl...")
	out := flags.String("o", "", "file to write, instead of standard output")
	flags.Parse(args)
	if flags.NArg() == 0 {
		return errors.New("share takes one or more files, for example: b2trace share -o shared.jsonl sessions.jsonl")
	}
	sessions, err := readAll(flags.Args())
	if err != nil {
		return err
	}
	if *out == "" {
		return writeShared(stdout, sessions)
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	if err := writeShared(f, sessions); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func writeShared(w io.Writer, sessions []session) error {
	enc := json.NewEncoder(w)
	for _, s := range sessions {
		strip(&s)
		if err := enc.Encode(s); err != nil {
			return err
		}
	}
	return nil
}

// quoted matches the message titles that parser errors quote with %q,
// escaped quotes inside them included.
var quoted = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// strip removes what a session says about who sent what to whom: callsigns,
// locators, addresses, message titles and MIDs. The numbers, the SIDs and
// the protocol keywords stay.
func strip(s *session) {
	calls := []string{s.LocalCall, s.RemoteCall}
	s.Shared = true
	s.LocalCall, s.RemoteCall = "", ""
	s.ConnectLine = scrubLine(s.ConnectLine, calls)
	for i := range s.States {
		s.States[i].Line = scrubLine(s.States[i].Line, calls)
	}
	for i := range s.Commands {
		s.Commands[i].Line = scrubLine(s.Commands[i].Line, calls)
	}
	for i := range s.Proposals {
		s.Proposals[i].MID = ""
	}
	for i := range s.Messages {
		s.Messages[i].Title = ""
	}
	for i := range s.Errors {
		s.Errors[i].Text = scrubLine(quoted.ReplaceAllString(s.Errors[i].Text, `""`), calls)
	}
}

// scrubLine drops the words of a line that can name a station or a place:
// the session's callsigns, and any word that mixes digits with letters,
// dots, colons, slashes or an @, which covers callsigns, locators, IP
// addresses and email addresses. Plain numbers and plain words stay.
func scrubLine(line string, calls []string) string {
	var kept []string
	for _, word := range strings.Fields(line) {
		if !revealing(word, calls) {
			kept = append(kept, word)
		}
	}
	return strings.Join(kept, " ")
}

func revealing(word string, calls []string) bool {
	for _, c := range calls {
		if c != "" && strings.EqualFold(strings.Trim(word, "()<>,;"), c) {
			return true
		}
	}
	if strings.ContainsRune(word, '@') {
		return true
	}
	if !strings.ContainsFunc(word, unicode.IsDigit) {
		return false
	}
	return strings.ContainsFunc(word, func(r rune) bool { return unicode.IsLetter(r) || strings.ContainsRune(".:/", r) })
}
