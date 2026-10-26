package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/softdevstudio/baresql/audit"
	"github.com/softdevstudio/baresql/internal/pgwire"
)

func runAudit(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("audit", "[flags] [connection]",
		"Lists, for every login, each column it can read, how it gets there, and whether the\n"+
			"column holds personal data. The connection is a postgres:// URL or key=value\n"+
			"string; anything left out comes from the PG* variables and ~/.pgpass, as in psql.\n"+
			"Writes report.html and audit.json. Only reads, inside a read-only transaction.", stderr)
	out := fs.String("out", "baresql-report", "directory for report.html and audit.json")
	var logins, schemas listFlag
	fs.Var(&logins, "login", "audit only this login (repeat or separate with commas)")
	fs.Var(&schemas, "schema", "audit only this schema (repeat or separate with commas)")
	noSample := fs.Bool("no-sample", false, "read no table data: find personal data by column names only")
	rows := fs.Int("sample-rows", 200, "rows to read per table when sampling (at most 10000)")
	baseline := fs.String("baseline", "", "an earlier audit.json: exit 1 if any login can now reach more")
	timeout := fs.Duration("timeout", 60*time.Second, "time limit for each query")
	format := fs.String("format", "summary", "what to print: summary, lines or json")
	pos, err := parse(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if len(pos) > 1 {
		fmt.Fprintln(stderr, "baresql audit: give one connection string (quote it if it has spaces)")
		return 2
	}
	if *rows < 1 || *rows > 10000 {
		fmt.Fprintln(stderr, "baresql audit: --sample-rows must be between 1 and 10000")
		return 2
	}
	if *format != "summary" && *format != "lines" && *format != "json" {
		fmt.Fprintln(stderr, "baresql audit: --format is summary, lines or json")
		return 2
	}
	var old *audit.Result
	if *baseline != "" {
		if old, err = audit.ReadResult(*baseline); err != nil {
			fmt.Fprintf(stderr, "baresql audit: %v\n", err)
			return 2
		}
	}
	dsn := ""
	if len(pos) == 1 {
		dsn = pos[0]
	}
	cfg, err := pgwire.ParseDSN(dsn)
	if err != nil {
		fmt.Fprintf(stderr, "baresql audit: %v\n", err)
		return 2
	}
	res, err := audit.Run(ctx, cfg, audit.RunOptions{Schemas: schemas, Logins: logins, Sample: !*noSample,
		SampleRows: *rows, StatementTimeout: *timeout})
	if err != nil {
		fmt.Fprintf(stderr, "baresql audit: %v\n", audit.CleanText(err.Error()))
		return 2
	}
	if err := audit.WriteFiles(*out, res); err != nil {
		fmt.Fprintf(stderr, "baresql audit: writing the report: %v\n", err)
		return 2
	}
	switch *format {
	case "summary":
		audit.WriteSummary(stdout, res)
		fmt.Fprintf(stdout, "Wrote %s and %s\n", filepath.Join(*out, "report.html"), filepath.Join(*out, "audit.json"))
	case "lines":
		fmt.Fprintln(stdout, strings.Join(res.Lines(), "\n"))
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(res)
	}
	if old != nil {
		cmp := audit.Compare(old, res)
		w := stdout
		if *format != "summary" {
			w = stderr
		}
		cmp.Write(w)
		if cmp.GrewAny() {
			return 1
		}
	}
	return 0
}

func runDiff(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("diff", "old.json new.json",
		"Compares two audit.json files and lists the reach that grew, the reach that is\n"+
			"gone and other changes. Exits 1 when any login can reach more than before.", stderr)
	format := fs.String("format", "text", "text or json")
	pos, err := parse(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if len(pos) != 2 {
		fs.Usage()
		return 2
	}
	old, err := audit.ReadResult(pos[0])
	if err != nil {
		fmt.Fprintf(stderr, "baresql diff: %v\n", err)
		return 2
	}
	cur, err := audit.ReadResult(pos[1])
	if err != nil {
		fmt.Fprintf(stderr, "baresql diff: %v\n", err)
		return 2
	}
	cmp := audit.Compare(old, cur)
	switch *format {
	case "text":
		cmp.Write(stdout)
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(cmp)
	default:
		fmt.Fprintln(stderr, "baresql diff: --format is text or json")
		return 2
	}
	if cmp.GrewAny() {
		return 1
	}
	return 0
}
