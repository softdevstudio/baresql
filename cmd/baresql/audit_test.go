package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/softdevstudio/baresql/internal/pgwire"
	"github.com/softdevstudio/baresql/internal/testenv"
)

// cliDatabase makes a small database for the CLI tests and returns a
// connection string for it.
func cliDatabase(t *testing.T) string {
	t.Helper()
	dsn := testenv.DSN(t, testenv.PG)
	cfg, err := pgwire.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	admin, err := pgwire.Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	exec := func(c *pgwire.Conn, sql string) {
		t.Helper()
		if err := c.Exec(ctx, sql); err != nil {
			t.Fatalf("%v\n%s", err, sql)
		}
	}
	cleanup := func(c *pgwire.Conn) {
		c.Exec(ctx, "DROP DATABASE IF EXISTS baresql_cli WITH (FORCE)")
		c.Exec(ctx, "DROP ROLE IF EXISTS cli_reader")
		c.Exec(ctx, "DROP ROLE IF EXISTS cli_owner")
	}
	cleanup(admin)
	exec(admin, "CREATE ROLE cli_owner NOLOGIN; CREATE ROLE cli_reader LOGIN")
	exec(admin, "CREATE DATABASE baresql_cli OWNER cli_owner")
	t.Cleanup(func() {
		c, err := pgwire.Connect(context.Background(), cfg)
		if err == nil {
			cleanup(c)
			c.Close()
		}
	})
	dbcfg := cfg
	dbcfg.Database = "baresql_cli"
	db, err := pgwire.Connect(ctx, dbcfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec(db, `CREATE TABLE people (id int, email text, nickname text);
		INSERT INTO people SELECT g, 'p' || g || '@example.com', 'nick' || g FROM generate_series(1, 20) g;
		ALTER TABLE people OWNER TO cli_owner;
		GRANT USAGE ON SCHEMA public TO cli_reader;
		GRANT SELECT (id, nickname) ON people TO cli_reader`)
	return strings.Replace(dsn, "/postgres?", "/baresql_cli?", 1)
}

func TestCLIAuditAndBaseline(t *testing.T) {
	dsn := cliDatabase(t)
	if !strings.Contains(dsn, "baresql_cli") {
		t.Skip("the test DSN doesn't name the postgres database; can't point it elsewhere")
	}
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := run(context.Background(), []string{"audit", dsn, "--out", filepath.Join(dir, "r1"), "--login", "cli_reader"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, out.String(), errb.String())
	}
	for _, f := range []string{"report.html", "audit.json"} {
		if st, err := os.Stat(filepath.Join(dir, "r1", f)); err != nil || st.Size() == 0 {
			t.Fatalf("%s not written: %v", f, err)
		}
	}
	if !strings.Contains(out.String(), "1 login, which cannot read personal data") {
		t.Fatalf("summary:\n%s", out.String())
	}

	// The same database again: nothing grew.
	out.Reset()
	code = run(context.Background(), []string{"audit", "--baseline", filepath.Join(dir, "r1", "audit.json"),
		"--out", filepath.Join(dir, "r2"), "--login", "cli_reader", dsn}, &out, &errb)
	if code != 0 {
		t.Fatalf("unchanged database: exit %d\n%s%s", code, out.String(), errb.String())
	}

	// Give the reader the email column: the baseline check fails.
	cfg, _ := pgwire.ParseDSN(dsn)
	c, err := pgwire.Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Exec(context.Background(), "GRANT SELECT (email) ON people TO cli_reader"); err != nil {
		t.Fatal(err)
	}
	c.Close()
	out.Reset()
	code = run(context.Background(), []string{"audit", dsn, "--baseline", filepath.Join(dir, "r1", "audit.json"),
		"--out", filepath.Join(dir, "r3"), "--login", "cli_reader"}, &out, &errb)
	if code != 1 {
		t.Fatalf("grown reach: exit %d, want 1\n%s%s", code, out.String(), errb.String())
	}
	if !strings.Contains(out.String(), "+ read cli_reader public.people.email") {
		t.Fatalf("growth not listed:\n%s", out.String())
	}

	// baresql diff says the same, and exits 1.
	out.Reset()
	code = run(context.Background(), []string{"diff", filepath.Join(dir, "r1", "audit.json"), filepath.Join(dir, "r3", "audit.json")}, &out, &errb)
	if code != 1 || !strings.Contains(out.String(), "Reach that grew: 1") {
		t.Fatalf("diff: exit %d\n%s", code, out.String())
	}
	out.Reset()
	code = run(context.Background(), []string{"diff", filepath.Join(dir, "r3", "audit.json"), filepath.Join(dir, "r1", "audit.json")}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "Reach that is gone: 1") {
		t.Fatalf("reverse diff: exit %d\n%s", code, out.String())
	}
}

func TestCLIAuditErrors(t *testing.T) {
	for _, args := range [][]string{
		{"audit", "--sample-rows", "0", "postgres://x@localhost/db"},
		{"audit", "--format", "xml", "postgres://x@localhost/db"},
		{"audit", "postgres://x@localhost/db", "extra"},
		{"audit", "postgres://x@localhost/db?bogus=1"},
		{"audit", "--baseline", "/does/not/exist.json", "postgres://x@localhost/db"},
		{"diff", "only-one.json"},
	} {
		var out, errb bytes.Buffer
		if code := run(context.Background(), args, &out, &errb); code != 2 {
			t.Errorf("%v: exit %d, want 2 (stderr %s)", args, code, errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := run(context.Background(), []string{"help", "audit"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "-baseline") {
		t.Errorf("help audit: exit %d\n%s", code, out.String())
	}
}
