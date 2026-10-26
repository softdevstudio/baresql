package portable

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func mustRun(t *testing.T, e Engine, stmt string) *Answer {
	t.Helper()
	a, err := e.Run(testCtx(t), stmt)
	if err != nil {
		t.Fatalf("%s: %s: %v", e.Name(), stmt, err)
	}
	return a
}

func TestEngines(t *testing.T) {
	for _, o := range availableOpeners(t) {
		t.Run(o.name, func(t *testing.T) {
			e := openFresh(t, []opener{o})[0]
			if e.Name() != o.name || cleanVersion(e.Version()) == "" {
				t.Fatalf("name %s version %q", e.Name(), e.Version())
			}
			mustRun(t, e, "CREATE TABLE t (x INTEGER, s VARCHAR(10))")
			if a := mustRun(t, e, "INSERT INTO t VALUES (1, 'a'), (2, NULL)"); a.Err != "" {
				t.Fatal(a.Err)
			}
			a := mustRun(t, e, "SELECT x, s FROM t ORDER BY x")
			if a.Err != "" || len(a.Rows) != 2 || a.Rows[0][0].Text != "1" || a.Rows[0][0].Kind != KindInt || a.Rows[0][1].Text != "a" || !a.Rows[1][1].Null {
				t.Fatalf("select: %+v", a)
			}
			if a := mustRun(t, e, "SELECT nosuch FROM t"); a.Err == "" {
				t.Fatal("no error for a missing column")
			}
			// Still usable after a refusal, inside the same throwaway space.
			if a := mustRun(t, e, "SELECT COUNT(*) FROM t"); a.Err != "" || a.Rows[0][0].Text != "2" {
				t.Fatalf("after an error: %+v", a)
			}
			a = mustRun(t, e, "SELECT 1.5, 'x', NULL, 7")
			if a.Err != "" || len(a.Rows) != 1 || a.Rows[0][1].Kind != KindText || !a.Rows[0][2].Null || !cellsEqual(a.Rows[0][0], Cell{Text: "1.5", Kind: KindNumeric}) {
				t.Fatalf("literals: %+v", a)
			}
			if a := mustRun(t, e, "SELECT 'two\nlines', 'it''s'"); a.Err != "" || a.Rows[0][0].Text != "two\nlines" || a.Rows[0][1].Text != "it's" {
				t.Fatalf("text: %+v", a)
			}
		})
	}
}

// TestEnginesLeaveNothing: what a script creates is gone after Close.
func TestEnginesLeaveNothing(t *testing.T) {
	for _, o := range availableOpeners(t) {
		t.Run(o.name, func(t *testing.T) {
			ctx := testCtx(t)
			e, err := o.open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mustRun(t, e, "CREATE TABLE leftover (x INTEGER)")
			mustRun(t, e, "INSERT INTO leftover VALUES (1)")
			var scratch string
			switch x := e.(type) {
			case *myEngine:
				scratch = x.db
			case *sqliteEngine:
				scratch = x.dir
			}
			e.Close()
			e2 := openFresh(t, []opener{o})[0]
			if a := mustRun(t, e2, "SELECT COUNT(*) FROM leftover"); a.Err == "" {
				t.Fatalf("the table survived Close: %+v", a)
			}
			switch x := e2.(type) {
			case *myEngine:
				a := mustRun(t, x, "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = '"+scratch+"'")
				if a.Err != "" || a.Rows[0][0].Text != "0" {
					t.Fatalf("scratch database %s still there: %+v", scratch, a)
				}
			case *sqliteEngine:
				if _, err := os.Stat(scratch); !os.IsNotExist(err) {
					t.Fatalf("temporary directory %s still there", scratch)
				}
			}
		})
	}
}

func TestSQLiteSafeMode(t *testing.T) {
	o, ok := engineOpener(t, SQLite)
	if !ok {
		t.Skip("no sqlite3")
	}
	e := openFresh(t, []opener{o})[0]
	mustRun(t, e, "CREATE TABLE kept (x INTEGER)")
	mustRun(t, e, "INSERT INTO kept VALUES (42)")
	target := filepath.Join(t.TempDir(), "written.txt")
	for _, stmt := range []string{"SELECT writefile('" + target + "', 'x')", "ATTACH '" + target + "' AS other", "VACUUM INTO '" + target + "'"} {
		a := mustRun(t, e, stmt)
		if !strings.Contains(a.Err, "safe mode") {
			t.Fatalf("%s: %+v", stmt, a)
		}
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("the script wrote a file")
	}
	// sqlite3 was started again on the same database.
	if a := mustRun(t, e, "SELECT x FROM kept"); a.Err != "" || a.Rows[0][0].Text != "42" {
		t.Fatalf("after the restart: %+v", a)
	}
	for _, stmt := range []string{".shell echo hi", "SELECT 'it\\'s'", "SELECT 1 # ;\n.shell echo hi"} {
		if a := mustRun(t, e, stmt); a.Err == "" {
			t.Fatalf("%q ran: %+v", stmt, a)
		}
	}
	if a := mustRun(t, e, "SELECT 1"); a.Err != "" {
		t.Fatalf("after refusals: %+v", a)
	}
}

func TestSQLiteSource(t *testing.T) {
	prog, err := FindSQLite(os.Getenv("BARESQL_TEST_SQLITE"))
	if err != nil {
		t.Skip("no sqlite3")
	}
	src := filepath.Join(t.TempDir(), "source.db")
	cmd := exec.Command(prog, src, "CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (7);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	before, _ := os.ReadFile(src)
	e, err := OpenSQLite(testCtx(t), prog, src)
	if err != nil {
		t.Fatal(err)
	}
	if a := mustRun(t, e, "SELECT x FROM t"); a.Err != "" || a.Rows[0][0].Text != "7" {
		t.Fatalf("copy: %+v", a)
	}
	mustRun(t, e, "DELETE FROM t")
	e.Close()
	after, _ := os.ReadFile(src)
	if string(before) != string(after) {
		t.Fatal("the source database changed")
	}
}

func TestMySQLStaysInScratch(t *testing.T) {
	for _, name := range []string{MySQL, MariaDB} {
		o, ok := engineOpener(t, name)
		if !ok {
			continue
		}
		e := openFresh(t, []opener{o})[0]
		for _, stmt := range []string{"CREATE TABLE mysql.baresql_test (x INT)", "DROP TABLE IF EXISTS mysql.user", "INSERT INTO mysql.user VALUES (1)"} {
			if a := mustRun(t, e, stmt); !strings.Contains(a.Err, "refuses") {
				t.Fatalf("%s: %s: %+v", name, stmt, a)
			}
		}
		db := e.(*myEngine).db
		if a := mustRun(t, e, "CREATE TABLE "+db+".ok (x INT)"); a.Err != "" {
			t.Fatalf("%s: own database: %+v", name, a)
		}
	}
}

// TestCompareEngines runs a short script on every engine available.
func TestCompareEngines(t *testing.T) {
	ops := availableOpeners(t)
	if len(ops) < 2 {
		t.Skip("compare needs two engines")
	}
	engines := openFresh(t, ops)
	script := "CREATE TABLE t (x INTEGER);\nINSERT INTO t VALUES (1), (2);\nSELECT SUM(x) FROM t;\nSELECT x FROM t;\nSELECT 'a' || 'b';\nBEGIN"
	cmp, err := Compare(testCtx(t), engines, []Script{{Name: "s.sql", SQL: script}})
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Summary.Statements != 6 || cmp.Summary.Skipped != 1 || len(cmp.Engines) != len(ops) {
		t.Fatalf("summary %+v", cmp.Summary)
	}
	for _, i := range []int{0, 1, 2, 3} {
		if sr := cmp.Statements[i]; sr.Differs() {
			t.Errorf("%s differs: %v", sr.SQL, sr.Differences)
		}
	}
	mysqlLike := false
	for _, e := range engines {
		if e.Name() == MySQL || e.Name() == MariaDB {
			mysqlLike = true
		}
	}
	if pipes := cmp.Statements[4]; pipes.Differs() != (mysqlLike && len(engines) > 1 && !allMySQL(engines)) {
		t.Errorf("'a' || 'b': differs %v on %d engines", pipes.Differs(), len(engines))
	}
}

func allMySQL(es []Engine) bool {
	for _, e := range es {
		if e.Name() != MySQL && e.Name() != MariaDB {
			return false
		}
	}
	return true
}
