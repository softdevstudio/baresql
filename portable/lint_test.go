package portable

import (
	"strings"
	"testing"
)

// lintCase: lint of sql reports the codes in want (as "code" or
// "code:severity") and none of the codes in not.
type lintCase struct {
	sql  string
	want []string
	not  []string
}

func TestLintRules(t *testing.T) {
	cases := []lintCase{
		// integer-division
		{"SELECT 7 / 2", []string{"integer-division:warning"}, nil},
		{"SELECT COUNT(*) / 2 FROM t", []string{"integer-division"}, nil},
		{"CREATE TABLE t (a INTEGER, b INTEGER); SELECT a / b FROM t", []string{"integer-division"}, nil},
		{"CREATE TABLE t (a INTEGER); SELECT SUM(a) / COUNT(*) FROM t", []string{"integer-division"}, nil},
		{"SELECT (1 + 2) / 2", []string{"integer-division"}, nil},
		{"SELECT 7.0 / 2; SELECT 7 / 2.0; SELECT 7 * 1.0 / 2; SELECT 1.0 * 7 / 2", nil, []string{"integer-division"}},
		{"CREATE TABLE t (a DECIMAL(5, 2)); SELECT a / 2 FROM t", nil, []string{"integer-division"}},
		{"SELECT a / b FROM t; SELECT '7' / 2", nil, []string{"integer-division"}},
		// pipes-concat, concat-null
		{"SELECT a || b FROM t", []string{"pipes-concat"}, nil},
		{"SELECT CONCAT('a', NULL)", []string{"concat-null:warning"}, nil},
		{"CREATE TABLE p (a VARCHAR(5)); SELECT CONCAT(a, 'x') FROM p", []string{"concat-null:warning"}, nil},
		{"SELECT CONCAT(a, 'x') FROM p", []string{"concat-null:info"}, nil},
		{"CREATE TABLE p (a VARCHAR(5) NOT NULL); SELECT CONCAT(a, 'x') FROM p", nil, []string{"concat-null"}},
		{"SELECT CONCAT('a', 'b'); SELECT CONCAT(COALESCE(a, ''), 'x') FROM p", nil, []string{"concat-null", "pipes-concat"}},
		// text-compare-case, like-case
		{"SELECT * FROM t WHERE status = 'active'", []string{"text-compare-case:info"}, nil},
		{"SELECT * FROM t WHERE status IN ('a', 'b')", []string{"text-compare-case"}, nil},
		{"SELECT * FROM t WHERE LOWER(status) = 'active'; SELECT * FROM t WHERE id = 5", nil, []string{"text-compare-case"}},
		{"SELECT * FROM t WHERE d = '2024-01-01'; SELECT * FROM t WHERE LOWER(a) IN ('x')", nil, []string{"text-compare-case"}},
		{"SELECT * FROM t WHERE name LIKE 'A%'", []string{"like-case:info"}, nil},
		{"SELECT * FROM t WHERE name NOT LIKE 'A%'", []string{"like-case"}, nil},
		{"SELECT * FROM t WHERE LOWER(name) LIKE 'a%'; SELECT * FROM t WHERE code LIKE '12%'", nil, []string{"like-case"}},
		// text-number-compare
		{"SELECT '1' = 1", []string{"text-number-compare"}, nil},
		{"CREATE TABLE t (code VARCHAR(5)); SELECT * FROM t WHERE code = 0", []string{"text-number-compare"}, nil},
		{"CREATE TABLE t (n INTEGER); SELECT * FROM t WHERE n = 'abc'", []string{"text-number-compare"}, nil},
		{"CREATE TABLE t (n INTEGER); SELECT * FROM t WHERE n = '5'", nil, []string{"text-number-compare"}},
		{"CREATE TABLE t (code VARCHAR(5)); SELECT * FROM t WHERE code = '0'", nil, []string{"text-number-compare"}},
		{"SELECT * FROM t WHERE a = 1", nil, []string{"text-number-compare"}},
		// quotes
		{`SELECT "name" FROM t`, []string{"double-quotes"}, nil},
		{"SELECT `name` FROM t", []string{"backticks:error"}, nil},
		{"SELECT 'name' FROM t", nil, []string{"double-quotes", "backticks"}},
		// null-order, nulls-first-last
		{"SELECT x FROM t ORDER BY x", []string{"null-order:info"}, nil},
		{"SELECT x FROM t ORDER BY x DESC, y", []string{"null-order"}, nil},
		{"SELECT x FROM t ORDER BY x IS NULL, x", nil, []string{"null-order"}},
		{"SELECT x FROM t ORDER BY x NULLS LAST", []string{"nulls-first-last"}, []string{"null-order"}},
		{"CREATE TABLE t (x INTEGER NOT NULL); SELECT x FROM t ORDER BY x", nil, []string{"null-order"}},
		{"CREATE TABLE t (id INTEGER PRIMARY KEY); SELECT id FROM t ORDER BY id", nil, []string{"null-order"}},
		{"SELECT x FROM t ORDER BY 1; SELECT a, COUNT(*) FROM t GROUP BY a ORDER BY COUNT(*)", nil, []string{"null-order"}},
		// ungrouped-column
		{"SELECT a, b FROM t GROUP BY a", []string{"ungrouped-column"}, nil},
		{"SELECT * FROM t GROUP BY a", []string{"ungrouped-column"}, nil},
		{"SELECT a, COUNT(*) FROM t GROUP BY a", nil, []string{"ungrouped-column"}},
		{"SELECT a, b FROM t GROUP BY a, b; SELECT a, b FROM t GROUP BY 1, 2", nil, []string{"ungrouped-column"}},
		{"SELECT t.a, MAX(t.b) FROM t GROUP BY t.a; SELECT a AS x, COUNT(*) FROM t GROUP BY x", nil, []string{"ungrouped-column"}},
		{"SELECT LOWER(a), COUNT(*) FROM t GROUP BY LOWER(a); SELECT a, 'x' FROM t GROUP BY a", nil, []string{"ungrouped-column"}},
		{"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT); SELECT id, name FROM t GROUP BY id", nil, []string{"ungrouped-column"}},
		{"CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT); SELECT x.id, x.name FROM t x GROUP BY x.id", nil, []string{"ungrouped-column"}},
		{"SELECT a, (SELECT MAX(b) FROM u) FROM t GROUP BY a", nil, []string{"ungrouped-column"}},
		// casts
		{"SELECT CAST(x AS CHAR) FROM t", []string{"cast-to-text:warning"}, nil},
		{"SELECT CAST(x AS VARCHAR(10)) FROM t", []string{"cast-to-text:error"}, []string{"varchar-length"}},
		{"SELECT CAST(x AS TEXT) FROM t", []string{"cast-to-text:error"}, nil},
		{"SELECT CAST(x AS INTEGER) FROM t", []string{"cast-to-number:error"}, nil},
		{"SELECT CAST(x AS SIGNED) FROM t", []string{"cast-to-number:error"}, nil},
		{"SELECT CAST(x AS DECIMAL) FROM t", []string{"cast-to-number:warning"}, nil},
		{"SELECT CAST(x AS DECIMAL(10, 2)) FROM t", []string{"cast-to-number:warning"}, nil},
		{"SELECT CAST(x AS FLOAT) FROM t; SELECT CAST(x AS DECIMAL(10)) FROM t", nil, []string{"cast-to-number"}},
		{"SELECT CAST(x AS DATE) FROM t", []string{"cast-to-date:warning"}, nil},
		{"SELECT CAST(x AS DATETIME) FROM t", []string{"cast-to-date:error"}, nil},
		{"SELECT CAST(x AS TIMESTAMP) FROM t", []string{"cast-to-date:error"}, nil},
		// date-arithmetic
		{"SELECT CURRENT_DATE + 1", []string{"date-arithmetic"}, nil},
		{"CREATE TABLE t (d DATE); SELECT d - 7 FROM t", []string{"date-arithmetic"}, nil},
		{"SELECT DATE '2024-01-01' + INTERVAL '1' DAY", []string{"date-arithmetic"}, nil},
		{"SELECT NOW() - INTERVAL 1 DAY", []string{"date-arithmetic"}, nil},
		{"SELECT 1 + 1; SELECT -1; CREATE TABLE t (d DATE); SELECT d FROM t WHERE d > '2024-01-01'", nil, []string{"date-arithmetic"}},
		// division-by-zero
		{"SELECT 1 / 0", []string{"division-by-zero:error"}, nil},
		{"SELECT a / b FROM t", []string{"division-by-zero:info"}, nil},
		{"SELECT a / 2 FROM t; SELECT a / NULLIF(b, 0) FROM t; SELECT a % 3 FROM t", nil, []string{"division-by-zero"}},
		// decimal-as-float, float-precision, char-padding
		{"CREATE TABLE t (p DECIMAL(10, 2))", []string{"decimal-as-float:info"}, nil},
		{"SELECT 0.1 + 0.2 = 0.3", []string{"decimal-as-float"}, nil},
		{"SELECT * FROM t WHERE p = 9.99; SELECT ROUND(0.1 + 0.2, 2) = 0.3", nil, []string{"decimal-as-float"}},
		{"CREATE TABLE t (x REAL)", []string{"float-precision"}, nil},
		{"CREATE TABLE t (x FLOAT)", []string{"float-precision"}, nil},
		{"CREATE TABLE t (x DOUBLE PRECISION, y FLOAT(53))", nil, []string{"float-precision"}},
		{"CREATE TABLE t (c CHAR(5))", []string{"char-padding:info"}, nil},
		{"CREATE TABLE t (c VARCHAR(5), d CHARACTER VARYING(5))", nil, []string{"char-padding"}},
		// length-bytes, lower-non-ascii, substring-start
		{"SELECT LENGTH('é')", []string{"length-bytes:warning"}, nil},
		{"SELECT LENGTH(name) FROM t", []string{"length-bytes:info"}, nil},
		{"SELECT LENGTH('abc')", nil, []string{"length-bytes"}},
		{"SELECT LOWER('É')", []string{"lower-non-ascii:warning"}, nil},
		{"SELECT UPPER(name) FROM t", []string{"lower-non-ascii:info"}, nil},
		{"SELECT LOWER('ABC')", nil, []string{"lower-non-ascii"}},
		{"SELECT SUBSTR(x, 0, 2) FROM t", []string{"substring-start"}, nil},
		{"SELECT SUBSTRING(x, -2) FROM t", []string{"substring-start"}, nil},
		{"SELECT SUBSTRING(x FROM 0 FOR 2) FROM t", []string{"substring-start"}, nil},
		{"SELECT SUBSTR(x, 1, 2) FROM t", nil, []string{"substring-start"}},
		// backslashes
		{`SELECT 'C:\temp'`, []string{"backslash-escapes:warning"}, nil},
		{`SELECT 'it\'s'`, []string{"backslash-escapes:error"}, nil},
		{`SELECT E'a\tb'`, []string{"backslash-escapes:error"}, nil},
		{`SELECT 'plain'; SELECT 'a' LIKE 'a\_b' ESCAPE '\'`, nil, []string{"backslash-escapes", "like-escape"}},
		{`SELECT 'a_b' LIKE 'a\_b'`, []string{"like-escape"}, []string{"backslash-escapes"}},
		// log-base, caret
		{"SELECT LOG(100)", []string{"log-base"}, nil},
		{"SELECT LOG(10, 100); SELECT LN(100)", nil, []string{"log-base"}},
		{"SELECT 2 ^ 3", []string{"caret-operator"}, nil},
		// comments
		{"SELECT 1 --x", []string{"dash-comment"}, nil},
		{"SELECT 1 -- x\n; SELECT 2 --\n", nil, []string{"dash-comment"}},
		{"SELECT 1 # x", []string{"hash-comment:error"}, nil},
		// names
		{"SELECT 1 AS Total", []string{"column-name-case"}, nil},
		{"SELECT a Total FROM t", []string{"column-name-case"}, nil},
		{"CREATE TABLE t (UserName TEXT)", []string{"column-name-case"}, nil},
		{"SELECT 1 AS total; SELECT s.Total FROM (SELECT 1 AS Total) s", nil, []string{"column-name-case"}},
		{"CREATE TABLE Users (id INT); SELECT * FROM users", []string{"table-name-case"}, nil},
		{"CREATE TABLE users (id INT); SELECT * FROM users; SELECT * FROM Other", nil, []string{"table-name-case"}},
		// type-not-enforced
		{"CREATE TABLE t (n INTEGER); INSERT INTO t VALUES ('abc')", []string{"type-not-enforced"}, nil},
		{"CREATE TABLE t (s VARCHAR(3)); INSERT INTO t VALUES ('abcdef')", []string{"type-not-enforced"}, nil},
		{"CREATE TABLE t (n INTEGER, s VARCHAR(3)); INSERT INTO t (s, n) VALUES ('abc', '5')", nil, []string{"type-not-enforced"}},
		// boolean-as-number
		{"CREATE TABLE u (active BOOLEAN); SELECT * FROM u WHERE active = 1", []string{"boolean-as-number"}, nil},
		{"SELECT * FROM t WHERE 1", []string{"boolean-as-number"}, nil},
		{"SELECT * FROM t WHERE x = 1 AND 1", []string{"boolean-as-number"}, nil},
		{"CREATE TABLE u (active BOOLEAN); INSERT INTO u VALUES (1)", []string{"boolean-as-number"}, nil},
		{"SELECT TRUE = 1", []string{"boolean-as-number"}, nil},
		{"SELECT CASE WHEN 1 THEN 'a' END", []string{"boolean-as-number"}, nil},
		{"CREATE TABLE u (n INTEGER); SELECT * FROM u WHERE n", []string{"boolean-as-number"}, nil},
		{"SELECT * FROM t WHERE x = 1; SELECT CASE x WHEN 1 THEN 'a' END FROM t; SELECT * FROM t LIMIT 1", nil, []string{"boolean-as-number"}},
		{"CREATE TABLE u (n INTEGER, active BOOLEAN); SELECT * FROM u WHERE n = 1; SELECT * FROM u WHERE active", nil, []string{"boolean-as-number"}},
		// auto-increment
		{"CREATE TABLE t (id INTEGER PRIMARY KEY AUTO_INCREMENT)", []string{"auto-increment:error"}, nil},
		{"CREATE TABLE t (id SERIAL PRIMARY KEY)", []string{"auto-increment:warning"}, nil},
		{"CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT)", []string{"auto-increment"}, nil},
		{"CREATE TABLE t (id INTEGER GENERATED BY DEFAULT AS IDENTITY)", []string{"auto-increment"}, nil},
		{"CREATE TABLE t (id INTEGER PRIMARY KEY)", nil, []string{"auto-increment"}},
		// LIMIT and friends
		{"SELECT * FROM t LIMIT 1, 2", []string{"limit-comma"}, nil},
		{"SELECT * FROM t LIMIT 2 OFFSET 1", nil, []string{"limit-comma", "fetch-first"}},
		{"SELECT * FROM t FETCH FIRST 2 ROWS ONLY", []string{"fetch-first"}, nil},
		{"SELECT * FROM t OFFSET 2 ROWS", []string{"fetch-first"}, nil},
		{"SELECT x FROM t WHERE x IN (SELECT x FROM t LIMIT 1)", []string{"limit-in-subquery"}, nil},
		{"SELECT x FROM t WHERE x IN (SELECT x FROM (SELECT x FROM t LIMIT 1) s); SELECT x FROM t WHERE x = (SELECT x FROM t LIMIT 1)", nil, []string{"limit-in-subquery"}},
		// aliases
		{"SELECT x * 2 AS y FROM t WHERE y > 2", []string{"alias-in-where:warning"}, nil},
		{"CREATE TABLE t (x INTEGER); SELECT x * 2 AS y FROM t WHERE y > 2", []string{"alias-in-where:error"}, nil},
		{"SELECT x AS x FROM t WHERE x > 1; SELECT t.x AS x FROM t WHERE x > 1", nil, []string{"alias-in-where"}},
		{"CREATE TABLE t (y INTEGER, x INTEGER); SELECT x * 2 AS y FROM t WHERE y > 2", nil, []string{"alias-in-where"}},
		{"SELECT a, COUNT(*) AS n FROM t GROUP BY a HAVING n > 1", []string{"alias-in-having"}, nil},
		{"SELECT a, COUNT(*) AS n FROM t GROUP BY a HAVING COUNT(*) > 1", nil, []string{"alias-in-having"}},
		// writes
		{"UPDATE t SET x = 1 WHERE id IN (SELECT id FROM t WHERE x = 0)", []string{"update-same-table"}, nil},
		{"DELETE FROM t WHERE id IN (SELECT id FROM t)", []string{"update-same-table"}, nil},
		{"UPDATE t SET x = 1 WHERE id IN (SELECT id FROM (SELECT id FROM t) AS s); UPDATE t SET x = 1 WHERE id IN (SELECT id FROM u)", nil, []string{"update-same-table"}},
		{"UPDATE t SET x = u.x FROM u WHERE u.id = t.id", []string{"update-join"}, nil},
		{"UPDATE t JOIN u ON u.id = t.id SET t.x = u.x", []string{"update-join"}, nil},
		{"DELETE t FROM t JOIN u ON u.id = t.id", []string{"update-join"}, nil},
		{"DELETE FROM t USING u WHERE u.id = t.id", []string{"update-join"}, nil},
		{"UPDATE t SET x = (SELECT x FROM u WHERE u.id = t.id); UPDATE t SET x = 1 WHERE id IN (SELECT id FROM u)", nil, []string{"update-join"}},
		{"INSERT IGNORE INTO t VALUES (1)", []string{"insert-ignore"}, nil},
		{"INSERT OR IGNORE INTO t VALUES (1)", []string{"insert-ignore"}, nil},
		{"INSERT INTO t VALUES (1) ON CONFLICT DO NOTHING", []string{"insert-ignore"}, []string{"upsert"}},
		{"INSERT INTO t VALUES (1) ON CONFLICT (id) DO UPDATE SET n = 2", []string{"upsert"}, []string{"insert-ignore"}},
		{"INSERT INTO t VALUES (1) ON DUPLICATE KEY UPDATE n = 2", []string{"upsert"}, nil},
		{"REPLACE INTO t VALUES (1)", []string{"upsert"}, nil},
		{"INSERT OR REPLACE INTO t VALUES (1)", []string{"upsert"}, nil},
		{"SELECT REPLACE(a, 'x', 'y') FROM t", nil, []string{"upsert"}},
		{"INSERT INTO t VALUES (1) RETURNING id", []string{"returning"}, nil},
		// joins and aggregates
		{"SELECT * FROM a FULL OUTER JOIN b ON a.x = b.x; SELECT * FROM a FULL JOIN b ON a.x = b.x", []string{"full-outer-join"}, nil},
		{"SELECT * FROM a LEFT JOIN b ON a.x = b.x", nil, []string{"full-outer-join", "function-not-everywhere"}},
		{"SELECT DISTINCT ON (a) a, b FROM t", []string{"distinct-on"}, nil},
		{"SELECT COUNT(DISTINCT a, b) FROM t", []string{"count-distinct-columns"}, nil},
		{"SELECT COUNT(DISTINCT a) FROM t", nil, []string{"count-distinct-columns"}},
		{"SELECT GREATEST(a, b) FROM t", []string{"greatest-least"}, nil},
		// DDL
		{"CREATE TABLE t (x VARCHAR)", []string{"varchar-length"}, nil},
		{"ALTER TABLE t ADD COLUMN x VARCHAR", []string{"varchar-length"}, nil},
		{"CREATE TABLE t (x VARCHAR(10))", nil, []string{"varchar-length"}},
		{"CREATE TABLE t (d DATETIME)", []string{"type-not-everywhere"}, nil},
		{"CREATE TABLE t (x INT UNSIGNED)", []string{"type-not-everywhere"}, nil},
		{"CREATE TABLE t (x INTEGER[])", []string{"type-not-everywhere"}, nil},
		{"CREATE TABLE t (x TIMESTAMP WITH TIME ZONE)", []string{"type-not-everywhere"}, nil},
		{"CREATE TABLE t (x TIMESTAMP, y INTEGER, z TEXT)", nil, []string{"type-not-everywhere"}},
		{"CREATE TABLE t (y YEAR(4))", []string{"type-not-everywhere"}, []string{"function-not-everywhere"}},
		{"CREATE TABLE t (x INT) ENGINE=InnoDB", []string{"table-options"}, nil},
		{"CREATE TABLE t (x INT, KEY k (x))", []string{"table-options"}, nil},
		{"CREATE TABLE t (x INT COMMENT 'hi')", []string{"table-options"}, nil},
		{"CREATE TABLE t (x INT) WITHOUT ROWID", []string{"table-options"}, nil},
		{"CREATE TABLE t (x INT PRIMARY KEY, y INT REFERENCES u (id))", nil, []string{"table-options"}},
		{"CREATE TABLE t (x TEXT DEFAULT 'a')", []string{"text-default"}, nil},
		{"CREATE TABLE t (x TEXT DEFAULT ('a'), y VARCHAR(5) DEFAULT 'a', z TEXT DEFAULT NULL)", nil, []string{"text-default"}},
		{"CREATE TABLE t (x TEXT PRIMARY KEY)", []string{"index-on-text"}, nil},
		{"CREATE TABLE t (x TEXT, UNIQUE (x))", []string{"index-on-text"}, nil},
		{"CREATE TABLE t (x TEXT); CREATE INDEX i ON t (x)", []string{"index-on-text"}, nil},
		{"CREATE TABLE t (x VARCHAR(50) PRIMARY KEY); CREATE INDEX i ON t (x)", nil, []string{"index-on-text"}},
		{"CREATE INDEX IF NOT EXISTS i ON t (x)", []string{"index-if-not-exists"}, nil},
		{"CREATE UNIQUE INDEX IF NOT EXISTS i ON t (x)", []string{"index-if-not-exists"}, nil},
		{"CREATE TABLE IF NOT EXISTS t (x INT)", nil, []string{"index-if-not-exists"}},
		{"TRUNCATE TABLE t; TRUNCATE t", []string{"truncate"}, nil},
		{"CREATE OR REPLACE VIEW v AS SELECT 1", []string{"create-or-replace"}, nil},
		// functions and operators
		{"SELECT IFNULL(a, 0) FROM t", []string{"function-not-everywhere"}, nil},
		{"SELECT NOW()", []string{"function-not-everywhere"}, nil},
		{"SELECT CAST(x AS CHAR(5)) FROM t; SELECT COALESCE(a, 0) FROM t", nil, []string{"function-not-everywhere"}},
		{"SELECT x::int FROM t", []string{"operator-not-everywhere"}, nil},
		{"SELECT 7 DIV 2", []string{"operator-not-everywhere"}, nil},
		{"SELECT a <=> b FROM t", []string{"operator-not-everywhere"}, nil},
		{"SELECT a IS DISTINCT FROM b FROM t", []string{"operator-not-everywhere"}, nil},
		{"SELECT a FROM t WHERE a ~ 'x'", []string{"operator-not-everywhere"}, nil},
		{"SELECT ~5; SELECT a FROM t WHERE a IS NOT NULL; SELECT MOD(7, 3)", nil, []string{"operator-not-everywhere"}},
		// ignore comments
		{"SELECT 7 / 2 -- baresql:ignore integer-division", nil, []string{"integer-division"}},
		{"-- baresql:ignore\nSELECT 7 / 2", nil, []string{"integer-division"}},
		{"/* baresql:ignore integer-division, pipes-concat */ SELECT 7 / 2, a || b FROM t", nil, []string{"integer-division", "pipes-concat"}},
		{"-- baresql:ignore pipes-concat\nSELECT 7 / 2", []string{"integer-division"}, nil},
	}
	for _, tc := range cases {
		got := map[string]string{}
		for _, p := range LintScript("t.sql", tc.sql) {
			if _, ok := got[p.Code]; !ok {
				got[p.Code] = p.Severity
			}
		}
		for _, w := range tc.want {
			code, sev, _ := strings.Cut(w, ":")
			s, ok := got[code]
			switch {
			case !ok:
				t.Errorf("%q: no %s (got %v)", tc.sql, code, got)
			case sev != "" && s != sev:
				t.Errorf("%q: %s is %s, want %s", tc.sql, code, s, sev)
			}
		}
		for _, n := range tc.not {
			if _, ok := got[n]; ok {
				t.Errorf("%q: unexpected %s", tc.sql, n)
			}
		}
	}
}

// TestLintPositions: problems point at the right place, and messages come
// from the catalog.
func TestLintPositions(t *testing.T) {
	ps := LintScript("q.sql", "SELECT 1;\nSELECT a,\n  7 / 2 FROM t")
	if len(ps) != 1 {
		t.Fatalf("problems: %+v", ps)
	}
	p := ps[0]
	if p.File != "q.sql" || p.Line != 3 || p.Col != 3 || p.Text != "7 / 2" || p.Message == "" || p.Advice == "" {
		t.Fatalf("problem: %+v", p)
	}
	ps = LintScript("q.sql", "SELECT IFNULL(a, 0) FROM t")
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "IFNULL() works on SQLite, MySQL and MariaDB; PostgreSQL refuses it.") || ps[0].Advice != "Use COALESCE(a, b)." {
		t.Fatalf("function row: %+v", ps)
	}
}

// FuzzLint: lint never panics, whatever the input.
func FuzzLint(f *testing.F) {
	for _, e := range Catalog() {
		f.Add(e.Setup + ";\n" + e.Example)
		if e.Rewrite != "" {
			f.Add(e.Rewrite)
		}
	}
	f.Add("SELECT ((((")
	f.Add("SELECT a FROM t WHERE")
	f.Add("CREATE TABLE")
	f.Add("UPDATE")
	f.Add("SELECT CAST(")
	f.Fuzz(func(t *testing.T, src string) {
		LintScript("f.sql", src)
	})
}
