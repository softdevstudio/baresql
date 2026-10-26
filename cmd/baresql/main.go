// Command baresql is BareSQL Audit and BareSQL Portable in one binary.
//
// Exit codes: 0 when there is nothing to report, 1 when the command found
// something (lint problems, differing answers, reach that grew past a
// baseline), 2 when it could not run (bad arguments, no connection).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/softdevstudio/baresql"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

type command struct {
	name, args, about string
	run               func(ctx context.Context, args []string, stdout, stderr io.Writer) int
}

func commands() []command {
	return []command{
		{"audit", "[flags] [connection]", "who can read what in a PostgreSQL database", runAudit},
		{"diff", "old.json new.json", "compare two audits and list what changed", runDiff},
		{"version", "", "print the version", nil},
		{"help", "[command]", "show help", nil},
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintf(stdout, "baresql %s\n", baresql.Version)
		return 0
	case "help", "-h", "--help":
		if len(args) > 1 {
			for _, c := range commands() {
				if c.name == args[1] && c.run != nil {
					return c.run(ctx, []string{"-h"}, stdout, stdout)
				}
			}
		}
		usage(stdout)
		return 0
	}
	for _, c := range commands() {
		if c.name == args[0] && c.run != nil {
			return c.run(ctx, args[1:], stdout, stderr)
		}
	}
	fmt.Fprintf(stderr, "baresql: unknown command %q\n\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, "BareSQL: plain checks for SQL databases.\n\nUsage:\n")
	for _, c := range commands() {
		fmt.Fprintf(w, "  baresql %-28s %s\n", strings.TrimSpace(c.name+" "+c.args), c.about)
	}
	fmt.Fprint(w, "\nRun baresql help <command> for its flags. Manual: https://github.com/softdevstudio/baresql#readme\n")
}

// newFlags makes a flag set that prints its usage the BareSQL way.
func newFlags(name, args, about string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: baresql %s %s\n\n%s\n\nFlags:\n", name, args, about)
		fs.PrintDefaults()
	}
	return fs
}

// parse reads flags that may come before or after the positional
// arguments: baresql audit postgres://db --out report works.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// listFlag collects a flag given several times or with commas.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*l = append(*l, p)
		}
	}
	return nil
}

// flagExit maps a flag parsing error to an exit code: -h is not an error.
func flagExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}
