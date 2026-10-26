// Package portable is BareSQL Portable: lint rules for SQL that has to run
// on SQLite, PostgreSQL, MySQL and MariaDB, and a comparison that runs the
// same script on real engines and reports where the answers differ.
package portable

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// Engine names.
const (
	SQLite   = "sqlite"
	Postgres = "postgres"
	MySQL    = "mysql"
	MariaDB  = "mariadb"
)

// EngineOrder is the order engines are listed in.
var EngineOrder = []string{SQLite, Postgres, MySQL, MariaDB}

// EngineTitle is the product name of an engine.
func EngineTitle(name string) string {
	switch name {
	case SQLite:
		return "SQLite"
	case Postgres:
		return "PostgreSQL"
	case MySQL:
		return "MySQL"
	case MariaDB:
		return "MariaDB"
	}
	return name
}

// Kind is what a value is, as far as comparing by meaning goes.
type Kind int

const (
	KindText Kind = iota
	KindInt
	KindNumeric // exact decimal
	KindFloat   // binary floating point
	KindBool
	KindDate
	KindTime
	KindTimestamp
	KindJSON
	KindBytes
)

// Cell is one value of an answer.
type Cell struct {
	Null bool
	Text string // the engine's text; hex digits for bytes
	Kind Kind
}

// Answer is what an engine said to one statement.
type Answer struct {
	Columns []string
	Rows    [][]Cell
	Err     string   // the engine refused the statement
	Notes   []string // warnings the engine gave
}

// Engine runs statements in a throwaway space: a transaction rolled back
// at the end (PostgreSQL), a scratch database dropped at the end (MySQL and
// MariaDB) or a temporary database file (SQLite).
type Engine interface {
	Name() string
	Version() string
	// Run executes one statement. A statement the engine refuses is an
	// answer with Err set; the error return is for a lost connection or a
	// crashed program, after which the engine is unusable.
	Run(ctx context.Context, stmt string) (*Answer, error)
	Close() error
}

// Label is "PostgreSQL 16.15".
func Label(e Engine) string {
	v := e.Version()
	if i := strings.IndexAny(v, " ("); i > 0 {
		v = v[:i]
	}
	return EngineTitle(e.Name()) + " " + v
}

func scratchName() string {
	b := make([]byte, 5)
	rand.Read(b)
	return "baresql_compare_" + hex.EncodeToString(b)
}

// transactionControl reports statements that would end the engines'
// throwaway transactions; compare skips them everywhere.
func transactionControl(first string) bool {
	switch first {
	case "BEGIN", "START", "COMMIT", "ROLLBACK", "END", "SAVEPOINT", "RELEASE", "ABORT":
		return true
	}
	return false
}

func errorf(format string, args ...any) *Answer {
	return &Answer{Err: fmt.Sprintf(format, args...)}
}
