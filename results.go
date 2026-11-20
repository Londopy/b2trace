package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"slices"
)

func runResults(args []string, w io.Writer) error {
	flags := newFlags("results", "shared.jsonl...")
	flags.Parse(args)
	if flags.NArg() == 0 {
		return errors.New("results takes one or more files made by b2trace share, for example: b2trace results results/*.jsonl")
	}
	paths, err := expand(flags.Args())
	if err != nil {
		return err
	}
	groups := map[string][]session{}
	for _, path := range paths {
		sessions, err := readSessions(path)
		if err != nil {
			return err
		}
		for i, s := range sessions {
			if !s.Shared {
				return fmt.Errorf("%s: session %d has not been through b2trace share", path, i+1)
			}
			key := cmp.Or(s.Link, s.Modem)
			groups[key] = append(groups[key], s)
		}
	}
	fmt.Fprintln(w, "| Link | Sessions | Messages | Turnarounds (median) | Gap (mean) | Reply time (mean) | Connect (median) | Payload share of time | Payload share of bytes |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|")
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintln(w, resultRow(k, groups[k]))
	}
	return nil
}

func resultRow(link string, sessions []session) string {
	var all stats
	var turnarounds, connects []float64
	for _, s := range sessions {
		all = all.plus(sessionStats(s))
		turnarounds = append(turnarounds, float64(s.Turnarounds))
		if s.ConnectMs > 0 {
			connects = append(connects, float64(s.ConnectMs))
		}
	}
	gap := "-"
	if all.gaps > 0 {
		gap = fmt.Sprintf("%.2f s", float64(all.gapMs)/float64(all.gaps)/1000)
	}
	reply := "-"
	if all.replies > 0 {
		reply = fmt.Sprintf("%.2f s", all.replyMs/float64(all.replies)/1000)
	}
	connect := "-"
	if len(connects) > 0 {
		connect = fmt.Sprintf("%.1f s", median(connects)/1000)
	}
	bytes := all.out.total() + all.in.total()
	return fmt.Sprintf("| %s | %d | %d | %g | %s | %s | %s | %s | %s |", link, len(sessions), all.msgsOut+all.msgsIn,
		median(turnarounds), gap, reply, connect, shareF(all.time.phase[phasePayload], all.time.total()),
		share(all.out.Payload+all.in.Payload, bytes))
}
