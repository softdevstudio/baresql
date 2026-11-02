package portable

import (
	"strings"
	"testing"
)

// The independent review of the 0.2 rules: each case failed on 594df26.
func TestReview020Lint(t *testing.T) {
	cases := []lintCase{
		{"CREATE TABLE a (id INTEGER, u_id INTEGER, FOREIGN KEY (u_id) REFERENCES u (id) ON UPDATE CASCADE)", nil, []string{"reserved-word"}},
		{"ALTER TABLE a ADD CONSTRAINT fk FOREIGN KEY (u_id) REFERENCES u (id) ON DELETE CASCADE ON UPDATE RESTRICT", nil, []string{"reserved-word"}},
		{"SELECT * FROM a WHERE id = 1 FOR UPDATE OF a", nil, []string{"reserved-word"}},
		{"SELECT TRIM(LEADING '0' FROM REPLACE(code, '-', '')) FROM a", nil, []string{"reserved-word"}},
		{"SELECT x FROM a WHERE x IS DISTINCT FROM CAST(y AS INTEGER)", nil, []string{"reserved-word"}},
		{"SELECT EXTRACT(YEAR FROM CAST(d AS DATE)) FROM a", nil, []string{"reserved-word"}},
		{"UPDATE IGNORE a SET x = 1; ALTER TABLE ONLY a ADD COLUMN c INTEGER; SELECT * FROM ONLY a", nil, []string{"reserved-word"}},
		{"ALTER TABLE a ALTER COLUMN id ADD GENERATED ALWAYS AS IDENTITY", nil, []string{"reserved-word"}},
		{"SELECT 1 AS user, 2 AS only, 3 AS end, 4 AS cast, 5 AS array, 6 AS do, 7 AS full", nil, []string{"reserved-word"}},
		{"SELECT 1 AS rank", []string{"reserved-word:error"}, nil},
		{"ALTER TABLE t RENAME a TO b", []string{"alter-column:error"}, nil},
		{"ALTER TABLE IF EXISTS t ADD COLUMN c INTEGER", []string{"alter-column:error"}, nil},
		{"ALTER TABLE t ADD COLUMN c INTEGER AFTER a", []string{"alter-column:error"}, nil},
		{"ALTER TABLE t DROP COLUMN c CASCADE", []string{"alter-column:error"}, nil},
		{"ALTER TABLE t ADD COLUMN c INTEGER, ALGORITHM = INPLACE; ALTER TABLE t ADD COLUMN d INTEGER", []string{"alter-column"}, nil},
		{"CREATE TABLE t (a INTEGER); SELECT ROUND(100 * AVG(a), 1), FLOOR(AVG(a)) FROM t", nil, []string{"avg-digits"}},
		{"CREATE TABLE t (a INTEGER, s INTEGER); SELECT s FROM t GROUP BY s HAVING AVG(a) > 1", nil, []string{"avg-digits"}},
		{"SELECT 'active' AS status UNION SELECT 'inactive'", nil, []string{"group-case"}},
		{"CREATE TABLE t (id INTEGER PRIMARY KEY, s VARCHAR(10)); SELECT DISTINCT id, s FROM t; SELECT id, s FROM t GROUP BY id, s", nil, []string{"group-case"}},
		{"CREATE TABLE t (s VARCHAR(10)); SELECT s FROM t INTERSECT SELECT s FROM t", []string{"group-case:info"}, nil},
		{"CREATE TABLE t (s VARCHAR(10)); SELECT s, COUNT(*) FROM t GROUP BY s", []string{"group-case:info"}, nil},
		{"CREATE TABLE d (x DECIMAL(10, 3)); SELECT ROUND(x, 2) FROM d", []string{"round-half:info"}, nil},
		{"SELECT ROUND(0.15, 1)", []string{"round-half:warning"}, nil},
		{"SELECT ROUND(0.25, 2), ROUND(0.14, 1), ROUND(2.5)", nil, []string{"round-half"}},
		{"CREATE TABLE t (a INTEGER); SELECT ROUND(AVG(a), 2) FROM t", nil, []string{"round-half"}},
		{"CREATE TABLE a (u_id INTEGER REFERENCES u (id) ON UPDATE CASCADE)", nil, []string{"table-options"}},
		{"SELECT SUBSTRING(s FOR 3) FROM t; SELECT SUBSTR(s FROM 2) FROM t", []string{"substring-from:error"}, nil},
		{"SELECT SUBSTRING(s FROM '[0-9]+') FROM t", []string{"substring-from"}, nil},
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
			if s, ok := got[code]; !ok {
				t.Errorf("%q: no %s (got %v)", tc.sql, code, got)
			} else if sev != "" && s != sev {
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

func TestReview020Messages(t *testing.T) {
	cases := []struct{ sql, code, mustNot string }{
		{"ALTER TABLE t ADD INDEX t_a (a)", "alter-column", "works on PostgreSQL"},
		{"ALTER TABLE t ADD KEY t_a (a)", "alter-column", "works on PostgreSQL"},
		{"ALTER TABLE t DROP CHECK ck", "alter-column", "works on PostgreSQL"},
		{"ALTER TABLE t ADD PRIMARY KEY (a)", "alter-column", "ADD PRIMARY works"},
		{"SELECT 1 AS desc", "reserved-word", "PostgreSQL, MySQL and MariaDB refuse"},
		{"DROP VIEW v CASCADE", "drop-cascade", "MySQL and MariaDB refuse"},
		{"DROP TABLE t CASCADE", "drop-cascade", "MySQL and MariaDB refuse"},
		{"SELECT SUBSTR(s FROM 2) FROM t", "substring-from", "works on PostgreSQL"},
		{"SELECT SUBSTRING(s FOR 3) FROM t", "substring-from", "works on MySQL"},
	}
	for _, tc := range cases {
		found := false
		for _, p := range LintScript("t.sql", tc.sql) {
			if p.Code == tc.code {
				found = true
				if strings.Contains(p.Message, tc.mustNot) {
					t.Errorf("%q: %s says %q", tc.sql, tc.code, p.Message)
				}
			}
		}
		if !found {
			t.Errorf("%q: no %s", tc.sql, tc.code)
		}
	}
	if ps := FilterEngines(LintScript("t.sql", "SELECT SUBSTRING(s FOR 3) FROM t"), []string{"postgres", "mysql"}); len(ps) == 0 {
		t.Errorf("SUBSTRING(s FOR 3) dropped by --engines postgres,mysql")
	}
}

// The second round of the review: each case failed on 50f50b2.
func TestReview020Round2(t *testing.T) {
	cases := []lintCase{
		{"CREATE TABLE m (a INTEGER, b INTEGER, s VARCHAR(10), PRIMARY KEY (a, b)); SELECT DISTINCT a, s FROM m", []string{"group-case"}, nil},
		{"CREATE TABLE u (id INTEGER PRIMARY KEY); CREATE TABLE g (uid INTEGER, tag VARCHAR(10)); " +
			"SELECT u.id, g.tag FROM u JOIN g ON g.uid = u.id GROUP BY u.id, g.tag", []string{"group-case"}, nil},
		{"SELECT 'X' UNION SELECT 'x'", []string{"group-case"}, nil},
		{"SELECT ROUND(-0.15, 1)", []string{"round-half"}, nil},
		{"CREATE TABLE d (x DECIMAL(10, 2)); SELECT ROUND(x, 2) FROM d", nil, []string{"round-half"}},
		{"CREATE TABLE d (x DECIMAL(10, 3)); SELECT ROUND(x, 2) FROM d", []string{"round-half"}, nil},
		{"ALTER TABLE t ADD COLUMN c INTEGER, ALGORITHM = INPLACE", []string{"alter-column"}, nil},
		{"CREATE TABLE t (a INTEGER); SELECT CAST(AVG(a) AS CHAR(20)) FROM t", []string{"avg-digits"}, nil},
		{"CREATE TABLE t (a INTEGER); SELECT CAST(AVG(a) AS DECIMAL(10, 2)) FROM t", nil, []string{"avg-digits"}},
	}
	for _, tc := range cases {
		got := map[string]bool{}
		for _, p := range LintScript("t.sql", tc.sql) {
			got[p.Code] = true
		}
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("%q: no %s", tc.sql, w)
			}
		}
		for _, n := range tc.not {
			if got[n] {
				t.Errorf("%q: unexpected %s", tc.sql, n)
			}
		}
	}
	for _, sql := range []string{"ALTER TABLE t ADD UNIQUE KEY uk (a)", "ALTER TABLE t ADD CONSTRAINT uk UNIQUE KEY (a)"} {
		for _, p := range LintScript("t.sql", sql) {
			if p.Code == "alter-column" && strings.Contains(p.Message, "works on PostgreSQL") {
				t.Errorf("%q: %s", sql, p.Message)
			}
		}
	}
}
