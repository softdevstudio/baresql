// Package testenv tells tests which database engines they may use.
//
// Engines come from the environment: BARESQL_TEST_PG, BARESQL_TEST_MYSQL and
// BARESQL_TEST_MARIADB hold connection strings, and SQLite is the sqlite3
// program (BARESQL_TEST_SQLITE, or sqlite3 on PATH). A test whose engine is
// missing is skipped, unless BARESQL_REQUIRE_ENGINES lists that engine (pg,
// mysql, mariadb, sqlite, separated by commas, or "all"): then it fails, so
// a CI job can't pass by quietly testing nothing.
package testenv

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Engine names, as used in BARESQL_REQUIRE_ENGINES.
const (
	PG      = "pg"
	MySQL   = "mysql"
	MariaDB = "mariadb"
	SQLite  = "sqlite"
)

var vars = map[string]string{
	PG:      "BARESQL_TEST_PG",
	MySQL:   "BARESQL_TEST_MYSQL",
	MariaDB: "BARESQL_TEST_MARIADB",
}

// Required reports whether BARESQL_REQUIRE_ENGINES lists engine.
func Required(engine string) bool {
	for _, e := range strings.Split(os.Getenv("BARESQL_REQUIRE_ENGINES"), ",") {
		e = strings.TrimSpace(e)
		if e == "all" || e == engine {
			return true
		}
	}
	return false
}

// DSN returns the connection string of a server engine (PG, MySQL or
// MariaDB). It skips the test when none is set, or fails it when the engine
// is required.
func DSN(t testing.TB, engine string) string {
	t.Helper()
	name, ok := vars[engine]
	if !ok {
		t.Fatalf("testenv: unknown engine %q", engine)
	}
	dsn := os.Getenv(name)
	if dsn == "" {
		missing(t, engine, name+" is not set")
	}
	return dsn
}

// SQLiteProgram returns the path of the sqlite3 program, skipping or
// failing the test like DSN when there is none.
func SQLiteProgram(t testing.TB) string {
	t.Helper()
	if p := os.Getenv("BARESQL_TEST_SQLITE"); p != "" {
		return p
	}
	p, err := exec.LookPath("sqlite3")
	if err != nil {
		missing(t, SQLite, "no sqlite3 program on PATH and BARESQL_TEST_SQLITE is not set")
	}
	return p
}

func missing(t testing.TB, engine, why string) {
	t.Helper()
	if Required(engine) {
		t.Fatalf("%s, and BARESQL_REQUIRE_ENGINES requires %s", why, engine)
	}
	t.Skip(why)
}
