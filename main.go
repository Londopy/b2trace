// b2trace measures where airtime goes in Winlink B2F sessions.
package main

import (
	"flag"
	"fmt"
	"os"
)

const usage = `b2trace measures where airtime goes in Winlink B2F sessions.

Usage:
  b2trace proxy --modem vara|ardop --target host:port [--listen host:port] [--log sessions.jsonl]
  b2trace summary sessions.jsonl
  b2trace compress [--dict-size 16384] [--csv] <file or dir>...

Run "b2trace <command> -h" for the flags of a command.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "proxy":
		err = runProxy(args)
	case "summary":
		err = runSummary(args, os.Stdout)
	case "compress":
		err = runCompress(args, os.Stdout, os.Stderr)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "b2trace: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "b2trace:", err)
		os.Exit(1)
	}
}

func newFlags(name, synopsis string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: b2trace %s %s\n\n", name, synopsis)
		fs.PrintDefaults()
	}
	return fs
}
