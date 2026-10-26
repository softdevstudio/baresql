package portable

import (
	"context"
	"strings"
	"testing"

	"github.com/softdevstudio/baresql/internal/sqltext"
)

// fakeEngine answers from a table: statement text -> answer.
type fakeEngine struct {
	name    string
	answers map[string]*Answer
	ran     []string
}

func (f *fakeEngine) Name() string    { return f.name }
func (f *fakeEngine) Version() string { return "1.0" }
func (f *fakeEngine) Close() error    { return nil }
func (f *fakeEngine) Run(_ context.Context, stmt string) (*Answer, error) {
	f.ran = append(f.ran, stmt)
	if a, ok := f.answers[stmt]; ok {
		return a, nil
	}
	return &Answer{}, nil
}

func cells(kind Kind, texts ...string) []Cell {
	var out []Cell
	for _, t := range texts {
		if t == "NULL" {
			out = append(out, Cell{Null: true})
			continue
		}
		out = append(out, Cell{Text: t, Kind: kind})
	}
	return out
}

func rows(kind Kind, texts ...string) [][]Cell {
	var out [][]Cell
	for _, t := range texts {
		out = append(out, cells(kind, t))
	}
	return out
}

func TestCompareJudge(t *testing.T) {
	a := &fakeEngine{name: SQLite, answers: map[string]*Answer{
		"SELECT 7 / 2":                 {Columns: []string{"x"}, Rows: rows(KindInt, "3")},
		"SELECT 3.5":                   {Columns: []string{"x"}, Rows: rows(KindFloat, "3.5")},
		"SELECT x FROM t ORDER BY x":   {Columns: []string{"x"}, Rows: rows(KindInt, "NULL", "1", "2")},
		"SELECT x FROM t":              {Columns: []string{"x"}, Rows: rows(KindInt, "2", "1")},
		"SELECT 1 / 0":                 {Columns: []string{"x"}, Rows: rows(KindInt, "NULL")},
		"SELECT ''":                    {Columns: []string{"x"}, Rows: rows(KindText, "")},
		"SELECT 'Abc'":                 {Columns: []string{"x"}, Rows: rows(KindText, "Abc")},
		"SELECT 1 AS Total":            {Columns: []string{"Total"}, Rows: rows(KindInt, "1")},
		"SELECT bad":                   {Err: "no such column: bad"},
		"SELECT COUNT(*) FROM nothing": {Err: "no such table: nothing"},
	}}
	b := &fakeEngine{name: Postgres, answers: map[string]*Answer{
		"SELECT 7 / 2":                 {Columns: []string{"x"}, Rows: rows(KindInt, "3")},
		"SELECT 3.5":                   {Columns: []string{"x"}, Rows: rows(KindNumeric, "3.5000")},
		"SELECT x FROM t ORDER BY x":   {Columns: []string{"x"}, Rows: rows(KindInt, "1", "2", "NULL")},
		"SELECT x FROM t":              {Columns: []string{"x"}, Rows: rows(KindInt, "1", "2")},
		"SELECT 1 / 0":                 {Err: "division by zero"},
		"SELECT ''":                    {Columns: []string{"x"}, Rows: rows(KindText, "NULL")},
		"SELECT 'Abc'":                 {Columns: []string{"x"}, Rows: rows(KindText, "abc")},
		"SELECT 1 AS Total":            {Columns: []string{"total"}, Rows: rows(KindInt, "1")},
		"SELECT bad":                   {Err: "column \"bad\" does not exist"},
		"SELECT COUNT(*) FROM nothing": {Err: "relation \"nothing\" does not exist"},
	}}
	c := &fakeEngine{name: MySQL, answers: map[string]*Answer{
		"SELECT 7 / 2": {Columns: []string{"x"}, Rows: rows(KindNumeric, "3.5000")},
	}}
	script := strings.Join([]string{"SELECT 7 / 2", "SELECT 3.5", "SELECT x FROM t ORDER BY x", "SELECT x FROM t",
		"SELECT 1 / 0", "SELECT ''", "SELECT 'Abc'", "SELECT 1 AS Total", "SELECT bad", "SELECT COUNT(*) FROM nothing",
		"BEGIN", "USE production", "GRANT ALL ON t TO u", "CREATE DATABASE x"}, ";\n")
	cmp, err := Compare(context.Background(), []Engine{a, b}, []Script{{Name: "s.sql", SQL: script}})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		differs bool
		says    string
		skipped bool
	}{
		{false, "", false}, // 3 = 3
		{false, "", false}, // 3.5 = 3.5000
		{true, "the same rows in a different order", false}, // ORDER BY counts
		{false, "", false}, // no ORDER BY: order does not count
		{true, "refused by PostgreSQL, answered by SQLite", false},
		{true, "NULL on one side, empty text on the other", false},
		{true, "the same text in different case", false},
		{true, "column 1 is named Total on SQLite and total on PostgreSQL", false},
		{false, "", false}, // refused by both
		{false, "", false},
		{false, "", true}, {false, "", true}, {false, "", true}, {false, "", true},
	}
	if len(cmp.Statements) != len(want) {
		t.Fatalf("%d statements", len(cmp.Statements))
	}
	for i, w := range want {
		sr := cmp.Statements[i]
		if sr.Differs() != w.differs || (sr.Skipped != "") != w.skipped {
			t.Errorf("%s: differs %v skipped %q", sr.SQL, sr.Differs(), sr.Skipped)
		}
		if w.says != "" && !strings.Contains(strings.Join(sr.Differences, "|"), w.says) {
			t.Errorf("%s: differences %q, want %q", sr.SQL, sr.Differences, w.says)
		}
	}
	if s := cmp.Summary; s.Statements != 14 || s.Differ != 5 || s.Same != 3 || s.Failed != 2 || s.Skipped != 4 || s.Queries != 10 {
		t.Errorf("summary %+v", s)
	}
	// Skipped statements never reach an engine.
	for _, ran := range a.ran {
		if strings.HasPrefix(ran, "BEGIN") || strings.HasPrefix(ran, "USE") || strings.HasPrefix(ran, "GRANT") || strings.HasPrefix(ran, "CREATE DATABASE") {
			t.Errorf("ran %q", ran)
		}
	}
	// Lint codes are attached.
	if !contains(cmp.Statements[0].Rules, "integer-division") {
		t.Errorf("rules %v", cmp.Statements[0].Rules)
	}
	if _, err := Compare(context.Background(), []Engine{a}, nil); err == nil {
		t.Error("compare with one engine")
	}
	// Three engines: MySQL's 3.5000 is its own group.
	cmp, err = Compare(context.Background(), []Engine{a, b, c}, []Script{{Name: "s.sql", SQL: "SELECT 7 / 2"}})
	if err != nil {
		t.Fatal(err)
	}
	if g := cmp.Statements[0].Groups; len(g) != 2 || strings.Join(g[0], ",") != "sqlite,postgres" || strings.Join(g[1], ",") != "mysql" {
		t.Errorf("groups %v", g)
	}
	var b2 strings.Builder
	WriteComparison(&b2, cmp, false)
	if out := b2.String(); !strings.Contains(out, "SQLite and PostgreSQL: 3") || !strings.Contains(out, "MySQL: 3.5000") || !strings.Contains(out, "1 differ") {
		t.Errorf("report:\n%s", out)
	}
}

func TestSkipReason(t *testing.T) {
	for sql, skip := range map[string]bool{
		"BEGIN": true, "COMMIT": true, "START TRANSACTION": true, "SAVEPOINT a": true, "USE shop": true,
		"GRANT SELECT ON t TO u": true, "CREATE DATABASE x": true, "DROP SCHEMA x": true, "CREATE USER u": true,
		"CREATE OR REPLACE ROLE r": true, "SET GLOBAL max_connections = 1": true, "SET ROLE admin": true,
		"LOAD DATA INFILE 'x' INTO TABLE t": true, "COPY t TO PROGRAM 'id'": true, "ALTER SYSTEM SET x = 1": true,
		"SELECT 1 INTO OUTFILE '/tmp/x'": true, "VACUUM": true, "SELECT 'unterminated": true,
		"SELECT 1": false, "CREATE TABLE t (x INT)": false, "CREATE TEMPORARY TABLE t (x INT)": false,
		"DROP TABLE IF EXISTS t": false, "SET @x = 1": false, "INSERT INTO t VALUES (1)": false,
		"CREATE INDEX i ON t (x)": false, "CREATE VIEW v AS SELECT 1": false,
		// Found by the review: forms that reach past the scratch space.
		"CREATE /*! DATABASE */ made_by_compare": true, "SET @x = 1, @@global.max_connect_errors = 101": true,
		"SET sql_mode = DEFAULT, GLOBAL max_connect_errors = 102": true, "/*!40101 SET GLOBAL x = 1 */": true,
		"SELECT 1 /*! INTO OUTFILE '/tmp/x' */": true, "PREPARE TRANSACTION 'x'": true, "XA START 'x'": true,
		"END": true, "ABORT": true, "PREPARE s FROM 'DROP TABLE t'": true, "EXECUTE IMMEDIATE 'DROP TABLE t'": true,
		"CREATE PROCEDURE p() BEGIN PREPARE s FROM @q; EXECUTE s; END": true,
		"SELECT CAST(data AS TEXT) FROM fsdir('/etc/hostname')":        true, "SELECT LOAD_FILE('/etc/passwd')": true,
		"SELECT * FROM \"FsDir\"('/')": true, "SET DEFAULT ROLE ALL TO u": true, "SET RESOURCE GROUP g FOR 7": true,
		"CREATE TABLE t (x INT) ENGINE=FEDERATED CONNECTION='mysql://u@h/db/t'": true,
		"SELECT 1 /*!40101 , @@CHARACTER_SET_CLIENT */":                         false, "CREATE TABLE t (x INT) /*!50100 PARTITION BY HASH (x) */": false,
		"PREPARE p (int) AS SELECT $1": false, "SELECT upload_file FROM t": false, "SET NAMES utf8mb4": false,
	} {
		st := sqltext.Split(sql)
		if len(st) == 0 && skip {
			continue // a statement of comments only never runs
		}
		if len(st) != 1 {
			t.Fatalf("%s: %d statements", sql, len(st))
		}
		if got := skipReason(st[0]); (got != "") != skip {
			t.Errorf("%s: skip reason %q", sql, got)
		}
	}
	others := map[string]bool{"prod": true, "mysql": true}
	for sql, want := range map[string]string{
		"INSERT INTO prod.users VALUES (1)":                  "prod",
		"UPDATE prod.users SET x = 1":                        "prod",
		"DELETE FROM prod.users":                             "prod",
		"DROP TABLE IF EXISTS t, prod.users":                 "prod",
		"CREATE INDEX i ON prod.users (x)":                   "prod",
		"TRUNCATE prod.users":                                "prod",
		"INSERT prod.t VALUES (100)":                         "prod",
		"REPLACE prod.t VALUES (200)":                        "prod",
		"UPDATE local_t, prod.t SET prod.t.v = prod.t.v + 1": "prod",
		"RENAME TABLE a TO b, prod.t4 TO moved":              "prod",
		"INSERT /*! INTO prod.t */ VALUES (300)":             "prod",
		"INSERT INTO `prod` . `t` VALUES (1)":                "prod",
		"SELECT * FROM PROD.users":                           "PROD",
		"INSERT INTO t SELECT * FROM prod.u":                 "prod",
		"UPDATE mysql.user SET plugin = ''":                  "mysql",
		"UPDATE t SET x = t.y":                               "",
		"SELECT table_name FROM information_schema.tables":   "",
		"SELECT 'prod.users', 1.5":                           "",
		"SELECT x FROM t -- prod.users":                      "",
	} {
		got := otherDatabase(significant(sqltext.Tokenize(sqltext.MySQLCode(sql))), others)
		if got != want {
			t.Errorf("%s: other database %q, want %q", sql, got, want)
		}
	}
}
