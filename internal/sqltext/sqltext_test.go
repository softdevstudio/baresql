package sqltext

import (
	"fmt"
	"strings"
	"testing"
)

func kinds(src string) string {
	var parts []string
	for _, t := range Tokenize(src) {
		if t.Kind == Space {
			continue
		}
		parts = append(parts, kindName[t.Kind]+":"+t.Text)
	}
	return strings.Join(parts, " ")
}

var kindName = map[Kind]string{Space: "sp", Comment: "c", Word: "w", QuotedIdent: "qi", String: "s",
	Number: "n", Param: "p", Operator: "op", Punct: "pu", Invalid: "bad"}

func TestTokenize(t *testing.T) {
	for src, want := range map[string]string{
		`SELECT 'it''s', E'a\'b', "x""y", ` + "`b`" + `, [c] FROM t`: `w:SELECT s:'it''s' pu:, s:E'a\'b' pu:, qi:"x""y" pu:, qi:` + "`b`" + ` pu:, qi:[c] w:FROM w:t`,
		`a[1]`:                           `w:a pu:[ n:1 pu:]`,
		`x::int || y -> 'k' ->> 'v'`:     `w:x op::: w:int op:|| w:y op:-> s:'k' op:->> s:'v'`,
		`$1 + ? + ?2 + :name + @v + @@g`: `p:$1 op:+ p:? op:+ p:?2 op:+ p::name op:+ p:@v op:+ p:@@g`,
		`$$a;b$$ $tag$x$y$tag$`:          `s:$$a;b$$ s:$tag$x$y$tag$`,
		"1 .5 1.5e-3 0x1F X'0a' N'n'":    `n:1 n:.5 n:1.5e-3 n:0x1F s:X'0a' s:N'n'`,
		"a -- c;\n/* x /* y */ z */ b":   `w:a c:-- c; c:/* x /* y */ z */ w:b`,
		"a # mysql\nb":                   `w:a c:# mysql w:b`,
		"j #> '{a}' #- '{b}'":            `w:j op:#> s:'{a}' op:#- s:'{b}'`,
		"x <=> y != z <> w":              `w:x op:<=> w:y op:!= w:z op:<> w:w`,
		"'open":                          `bad:'open`,
		"café_ñ 1":                       `w:café_ñ n:1`,
	} {
		if got := kinds(src); got != want {
			t.Errorf("%s\n got  %s\n want %s", src, got, want)
		}
	}
}

func TestTokenizeRoundTrip(t *testing.T) {
	src := "SELECT 'a'\n  , \"b\" -- x\nFROM t /* y */ WHERE c = $1;\n"
	var b strings.Builder
	for _, tk := range Tokenize(src) {
		b.WriteString(tk.Text)
	}
	if b.String() != src {
		t.Fatalf("round trip lost text: %q", b.String())
	}
}

func TestPositions(t *testing.T) {
	toks := Tokenize("SELECT\n  é, x")
	var x Token
	for _, tk := range toks {
		if tk.Text == "x" {
			x = tk
		}
	}
	if x.Line != 2 || x.Col != 6 {
		t.Fatalf("x at %d:%d, want 2:6", x.Line, x.Col)
	}
}

func TestSplit(t *testing.T) {
	src := `-- header
CREATE TABLE t (a int);
INSERT INTO t VALUES (1), (2); ;
SELECT ';' AS semi, $$;$$ FROM t -- ; in a comment
;
CREATE TRIGGER tr AFTER INSERT ON t BEGIN
  UPDATE t SET a = a + 1;
  SELECT CASE WHEN 1 THEN 2 END;
END;
BEGIN;
SELECT 1`
	stmts := Split(src)
	var got []string
	for _, s := range stmts {
		got = append(got, s.FirstWord())
	}
	if strings.Join(got, " ") != "CREATE INSERT SELECT CREATE BEGIN SELECT" {
		t.Fatalf("statements: %v", got)
	}
	if stmts[2].Text != "SELECT ';' AS semi, $$;$$ FROM t -- ; in a comment" || stmts[2].Line != 4 {
		t.Fatalf("third statement %q at line %d", stmts[2].Text, stmts[2].Line)
	}
	if !strings.HasSuffix(stmts[3].Text, "END") || !strings.Contains(stmts[3].Text, "SELECT CASE") {
		t.Fatalf("trigger body split: %q", stmts[3].Text)
	}
	if stmts[5].Text != "SELECT 1" {
		t.Fatalf("last statement %q", stmts[5].Text)
	}
}

func TestReturnsRows(t *testing.T) {
	for src, want := range map[string]bool{
		"SELECT 1":                             true,
		"with x as (select 1) select *":        true,
		"(SELECT 1) UNION (SELECT 2)":          true,
		"VALUES (1)":                           true,
		"INSERT INTO t VALUES (1)":             false,
		"INSERT INTO t VALUES (1) RETURNING a": true,
		"WITH d AS (DELETE FROM t) SELECT 1":   true,
		"WITH d AS (SELECT 1) DELETE FROM t":   false,
		"WITH a AS (SELECT 1), b AS (SELECT 2) INSERT INTO t SELECT * FROM a RETURNING x": true,
		"WITH a AS (SELECT 1) (SELECT 2)":                                                 true,
		"CREATE TABLE t (a int)":                                                          false,
		"UPDATE t SET a = 1":                                                              false,
	} {
		if got := Split(src)[0].ReturnsRows(); got != want {
			t.Errorf("%s: %v", src, got)
		}
	}
}

func FuzzTokenize(f *testing.F) {
	for _, s := range []string{"SELECT 'a''b' FROM t;", "$x$ $$", "/* /* */", "E'\\'", "[a", "#", "?1:x@"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		var b strings.Builder
		for _, tk := range Tokenize(src) {
			if tk.Text == "" {
				t.Fatalf("empty token in %q", src)
			}
			b.WriteString(tk.Text)
		}
		if b.String() != src {
			t.Fatalf("round trip changed %q", src)
		}
		for _, s := range Split(src) {
			if !strings.Contains(src, s.Text) {
				t.Fatalf("statement %q not in the source", s.Text)
			}
		}
	})
}

func TestBackslashQuotes(t *testing.T) {
	for src, want := range map[string][]string{
		`SELECT 'it\'s', 2`:      {`'it\'s'`},
		`SELECT 'C:\', 2`:        {`'C:\'`},
		`SELECT 'it\''`:          {`'it\''`},
		`SELECT 'a\\', 'b'`:      {`'a\\'`, `'b'`},
		`SELECT 'O\'Brien\'s' x`: {`'O\'Brien\'s'`},
		`SELECT "say \"hi\"" x`:  nil,
		`SELECT 'don''t'`:        {`'don''t'`},
		`SELECT E'a\'b'`:         {`E'a\'b'`},
	} {
		var got []string
		for _, tok := range Tokenize(src) {
			if tok.Kind == String {
				got = append(got, tok.Text)
			}
			if tok.Kind == Invalid {
				t.Errorf("%s: invalid token %q", src, tok.Text)
			}
		}
		if want != nil && strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: strings %q, want %q", src, got, want)
		}
	}
	// A double-quoted MySQL string with escaped quotes stays one token.
	toks := Tokenize(`SELECT "say \"hi\"" x`)
	if toks[2].Kind != QuotedIdent || toks[2].Text != `"say \"hi\""` {
		t.Errorf("double-quoted: %q", toks[2].Text)
	}
}

func TestMySQLCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1 # comment\n, 2", "SELECT 1 , 2"},
		{"SELECT 1 --1", "SELECT 1 - -1"},
		{"SELECT 1 -- note", "SELECT 1"},
		{"CREATE /*! DATABASE */ x", "CREATE DATABASE x"},
		{"INSERT /*!40101 INTO db.t */ VALUES (1)", "INSERT INTO db.t VALUES (1)"},
		{"/*M!100100 SET GLOBAL x = 1 */", "SET GLOBAL x = 1"},
		{"SELECT /* a */ 'it\\'s' , \"q\"", "SELECT '' , ''"},
		{"SELECT 'a /*! b */' FROM `t``x`", "SELECT '' FROM `t``x`"},
		{"SELECT /*+ BKA(t) */ 1", "SELECT 1"},
		{"SELECT 'unterminated", "SELECT"},
	}
	for _, c := range cases {
		got := MySQLCode(c.in)
		if norm := strings.Join(strings.Fields(got), " "); norm != c.want {
			t.Errorf("MySQLCode(%q) = %q, want %q", c.in, norm, c.want)
		}
		if strings.Count(got, "\n") != strings.Count(c.in, "\n") {
			t.Errorf("MySQLCode(%q) lost line breaks: %q", c.in, got)
		}
	}
}

func TestSplitDelimiter(t *testing.T) {
	src := "DELIMITER //\nCREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END //\nDELIMITER ;\nSELECT 3;\n" +
		"delimiter $$\nCREATE FUNCTION f() RETURNS INT BEGIN RETURN ';'; END$$\nSELECT '$$', 4 $$\nDELIMITER ;\nSELECT 5"
	var got []string
	for _, st := range Split(src) {
		got = append(got, fmt.Sprintf("%d:%d %s", st.Line, st.Col, st.Text))
	}
	want := []string{
		"2:1 CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END",
		"4:1 SELECT 3",
		"6:1 CREATE FUNCTION f() RETURNS INT BEGIN RETURN ';'; END",
		"7:1 SELECT '$$', 4",
		"9:1 SELECT 5",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
