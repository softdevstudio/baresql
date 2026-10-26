package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/softdevstudio/baresql/internal/mysqlwire"
	"github.com/softdevstudio/baresql/internal/pgwire"
	"github.com/softdevstudio/baresql/portable"
)

// readScripts reads SQL files; "-" is standard input.
func readScripts(paths []string, stdin io.Reader) ([]portable.Script, error) {
	var out []portable.Script
	for _, p := range paths {
		var b []byte
		var err error
		if p == "-" {
			b, err = io.ReadAll(stdin)
			p = "stdin"
		} else {
			b, err = os.ReadFile(p)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, portable.Script{Name: p, SQL: string(b)})
	}
	return out, nil
}

var stdin io.Reader = os.Stdin

// engineList checks --engines.
func engineNames(l listFlag) ([]string, error) {
	var out []string
	for _, e := range l {
		switch strings.ToLower(e) {
		case "sqlite", "sqlite3":
			out = append(out, portable.SQLite)
		case "postgres", "postgresql", "pg":
			out = append(out, portable.Postgres)
		case "mysql":
			out = append(out, portable.MySQL)
		case "mariadb":
			out = append(out, portable.MariaDB)
		default:
			return nil, fmt.Errorf("unknown engine %q (sqlite, postgres, mysql or mariadb)", e)
		}
	}
	return out, nil
}

func runLint(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("lint", "[flags] file.sql ...",
		"Reports SQL that answers differently, or fails, on SQLite, PostgreSQL, MySQL or\n"+
			"MariaDB, with what to write instead. Reads the files (- for standard input) and\n"+
			"connects to nothing. Exits 1 when it reports a problem.\n\n"+
			"A comment -- baresql:ignore code on a line, or on the line before it, silences\n"+
			"that rule there; without a code it silences every rule.", stderr)
	all := fs.Bool("all", false, "also report notes: differences that depend on the data (letter case, NULL order ...)")
	format := fs.String("format", "text", "text or json")
	var engines, ignore listFlag
	fs.Var(&engines, "engines", "only differences among these engines matter, e.g. sqlite,postgres")
	fs.Var(&ignore, "ignore", "rule codes to leave out (repeat or separate with commas)")
	pos, err := parse(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if len(pos) == 0 {
		fmt.Fprintln(stderr, "baresql lint: name at least one SQL file (- for standard input)")
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintln(stderr, "baresql lint: --format is text or json")
		return 2
	}
	names, err := engineNames(engines)
	if err != nil {
		fmt.Fprintf(stderr, "baresql lint: %v\n", err)
		return 2
	}
	if len(names) == 1 {
		fmt.Fprintln(stderr, "baresql lint: --engines needs at least two engines")
		return 2
	}
	for _, code := range ignore {
		if portable.Lookup(code) == nil {
			fmt.Fprintf(stderr, "baresql lint: unknown rule %q (baresql rules lists them)\n", code)
			return 2
		}
	}
	scripts, err := readScripts(pos, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "baresql lint: %v\n", err)
		return 2
	}
	var problems []portable.Problem
	hidden := 0
	for _, p := range portable.FilterEngines(portable.LintFiles(scripts), names) {
		switch {
		case contains(ignore, p.Code):
		case p.Severity == "info" && !*all:
			hidden++
		default:
			problems = append(problems, p)
		}
	}
	if *format == "json" {
		if problems == nil {
			problems = []portable.Problem{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(problems)
	} else {
		portable.WriteProblems(stdout, problems)
		if len(problems) > 0 {
			fmt.Fprintln(stdout)
		}
		fmt.Fprintln(stdout, portable.LintSummary(problems, hidden))
	}
	if len(problems) > 0 {
		return 1
	}
	return 0
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// sqliteFlag is --sqlite (a new, empty database) or --sqlite=file.db (a
// copy of that database).
type sqliteFlag struct {
	set  bool
	path string
}

func (f *sqliteFlag) String() string   { return f.path }
func (f *sqliteFlag) IsBoolFlag() bool { return true }
func (f *sqliteFlag) Set(v string) error {
	f.set = true
	switch v {
	case "true":
		f.path = ""
	case "false":
		f.set = false
	default:
		f.path = v
	}
	return nil
}

func runCompare(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("compare", "[engines] [flags] file.sql ...",
		"Runs the scripts on two or more engines and reports every statement whose\n"+
			"answers differ. Answers are compared by meaning: 3.5 equals 3.5000, true equals\n"+
			"1, and row order counts only under ORDER BY. Each engine works in a throwaway\n"+
			"space: a transaction rolled back at the end on PostgreSQL, a scratch database\n"+
			"dropped at the end on MySQL and MariaDB, a temporary file on SQLite (sqlite3\n"+
			"in its safe mode). Give it logins on servers meant for tests. Exits 1 when an\n"+
			"answer differs.\n\n"+
			"Connections are URLs: postgres://user@host/db, mysql://user@host:3306/,\n"+
			"mariadb://user@host:3307/. Leave passwords out of them and set PGPASSWORD or\n"+
			"MYSQL_PWD (or use ~/.pgpass).", stderr)
	var sq sqliteFlag
	fs.Var(&sq, "sqlite", "run on SQLite: --sqlite for a new database, --sqlite=file.db to start from a copy of one")
	sqliteProgram := fs.String("sqlite-program", "", "the sqlite3 program (default: sqlite3 on PATH)")
	pg := fs.String("postgres", "", "run on PostgreSQL at this connection URL")
	my := fs.String("mysql", "", "run on MySQL at this connection URL")
	maria := fs.String("mariadb", "", "run on MariaDB at this connection URL")
	all := fs.Bool("all", false, "list every statement, also those answered alike")
	format := fs.String("format", "text", "text or json")
	pos, err := parse(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintln(stderr, "baresql compare: --format is text or json")
		return 2
	}
	if len(pos) == 0 {
		fmt.Fprintln(stderr, "baresql compare: name at least one SQL file (- for standard input)")
		return 2
	}
	type opener struct {
		name string
		open func() (portable.Engine, error)
	}
	var ops []opener
	if sq.set {
		ops = append(ops, opener{"SQLite", func() (portable.Engine, error) { return portable.OpenSQLite(ctx, *sqliteProgram, sq.path) }})
	}
	if *pg != "" {
		cfg, err := pgwire.ParseDSN(*pg)
		if err != nil {
			fmt.Fprintf(stderr, "baresql compare: --postgres: %v\n", err)
			return 2
		}
		ops = append(ops, opener{"PostgreSQL", func() (portable.Engine, error) { return portable.OpenPostgres(ctx, cfg) }})
	}
	for _, m := range []struct {
		dsn, name, title string
	}{{*my, portable.MySQL, "MySQL"}, {*maria, portable.MariaDB, "MariaDB"}} {
		if m.dsn == "" {
			continue
		}
		cfg, err := mysqlwire.ParseDSN(m.dsn)
		if err != nil {
			fmt.Fprintf(stderr, "baresql compare: --%s: %v\n", m.name, err)
			return 2
		}
		name := m.name
		ops = append(ops, opener{m.title, func() (portable.Engine, error) { return portable.OpenMySQL(ctx, cfg, name) }})
	}
	if len(ops) < 2 {
		fmt.Fprintln(stderr, "baresql compare: give at least two engines (--sqlite, --postgres, --mysql, --mariadb)")
		return 2
	}
	scripts, err := readScripts(pos, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "baresql compare: %v\n", err)
		return 2
	}
	engines := make([]portable.Engine, len(ops))
	errs := make([]error, len(ops))
	var wg sync.WaitGroup
	for i, o := range ops {
		wg.Add(1)
		go func(i int, o opener) {
			defer wg.Done()
			engines[i], errs[i] = o.open()
		}(i, o)
	}
	wg.Wait()
	defer func() {
		for _, e := range engines {
			if e != nil {
				e.Close()
			}
		}
	}()
	for i, err := range errs {
		if err != nil {
			fmt.Fprintf(stderr, "baresql compare: %s: %v\n", ops[i].name, err)
			return 2
		}
	}
	cmp, err := portable.Compare(ctx, engines, scripts)
	if err != nil {
		fmt.Fprintf(stderr, "baresql compare: %v\n", err)
		return 2
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(cmp)
	} else {
		portable.WriteComparison(stdout, cmp, *all)
	}
	if cmp.Summary.Differ > 0 {
		return 1
	}
	return 0
}

func runRules(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("rules", "[flags] [code]",
		"Lists the differences BareSQL Portable knows, or shows one: a minimal example,\n"+
			"how each engine answered it, and a form that answers the same everywhere.", stderr)
	format := fs.String("format", "text", "text or json")
	pos, err := parse(fs, args)
	if err != nil {
		return flagExit(err)
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintln(stderr, "baresql rules: --format is text or json")
		return 2
	}
	if len(pos) > 1 {
		fmt.Fprintln(stderr, "baresql rules: give one rule code")
		return 2
	}
	if len(pos) == 1 {
		e := portable.Lookup(pos[0])
		if e == nil {
			fmt.Fprintf(stderr, "baresql rules: unknown rule %q (baresql rules lists them)\n", pos[0])
			return 2
		}
		if *format == "json" {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			enc.Encode(portable.EntryJSON{Entry: e, Answers: portable.RecordedAnswers(e.Code)})
			return 0
		}
		portable.WriteEntry(stdout, e)
		return 0
	}
	if *format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(portable.CatalogWithAnswers())
		return 0
	}
	portable.WriteCatalog(stdout)
	return 0
}
