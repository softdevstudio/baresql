// Command baresql is BareSQL Audit and BareSQL Portable in one binary.
//
// Exit codes: 0 when there is nothing to report, 1 when the command found
// something (lint problems, differing answers, reach that grew past a
// baseline), 2 when it could not run (bad arguments, no connection).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/softdevstudio/baresql"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintf(stdout, "baresql %s\n", baresql.Version)
		return 0
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "baresql: unknown command %q\n\n", args[0])
	usage(stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, `BareSQL: plain checks for SQL databases.

Usage:
  baresql version         print the version
  baresql help            show this help

Manual: https://github.com/softdevstudio/baresql#readme
`)
}
