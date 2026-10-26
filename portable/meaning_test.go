package portable

import (
	"strings"
	"testing"
)

func TestCellsEqual(t *testing.T) {
	c := func(kind Kind, text string) Cell { return Cell{Text: text, Kind: kind} }
	null := Cell{Null: true}
	for _, tc := range []struct {
		a, b Cell
		same bool
	}{
		{c(KindNumeric, "3.5"), c(KindNumeric, "3.5000"), true},
		{c(KindInt, "3"), c(KindNumeric, "3.0000"), true},
		{c(KindInt, "3"), c(KindNumeric, "3.5"), false},
		{c(KindFloat, "0.30000000000000004"), c(KindNumeric, "0.3"), true},
		{c(KindNumeric, "0.30000000000000004"), c(KindNumeric, "0.3"), false}, // exact decimals stay exact
		{c(KindFloat, "1.2345679"), c(KindFloat, "1.23456789"), false},
		{c(KindBool, "t"), c(KindInt, "1"), true},
		{c(KindBool, "f"), c(KindInt, "0"), true},
		{c(KindBool, "t"), c(KindInt, "0"), false},
		{c(KindDate, "2024-02-01"), c(KindText, "2024-02-01"), true},
		{c(KindTimestamp, "2024-02-01 00:00:00"), c(KindTimestamp, "2024-02-01T00:00:00+00:00"), true},
		{c(KindTimestamp, "2024-02-01 00:00:00.500"), c(KindTimestamp, "2024-02-01 00:00:00.5"), true},
		{c(KindJSON, `{"a": 1, "b": [1, 2]}`), c(KindJSON, `{"b":[1,2],"a":1.0}`), true},
		{c(KindJSON, `{"a": 1}`), c(KindJSON, `{"a": 2}`), false},
		{c(KindBytes, "0aff"), c(KindBytes, "0AFF"), true},
		{c(KindBytes, "61"), c(KindText, "61"), false},
		{c(KindText, "a"), c(KindText, "A"), false},
		{c(KindText, "1"), c(KindInt, "1"), true}, // the text of a number column
		{null, null, true},
		{null, c(KindText, ""), false},
		{c(KindFloat, "Inf"), c(KindFloat, "Infinity"), true},
	} {
		if got := cellsEqual(tc.a, tc.b); got != tc.same {
			t.Errorf("%+v vs %+v: %v", tc.a, tc.b, got)
		}
	}
}

func TestSortedRows(t *testing.T) {
	a := [][]Cell{{{Text: "2", Kind: KindInt}}, {{Text: "10", Kind: KindInt}}, {{Null: true}}}
	b := [][]Cell{{{Text: "10.0", Kind: KindNumeric}}, {{Null: true}}, {{Text: "2", Kind: KindInt}}}
	if !rowsEqual(sortedRows(a), sortedRows(b)) {
		t.Errorf("sorted rows differ: %v %v", sortedRows(a), sortedRows(b))
	}
}

func TestQuoteMode(t *testing.T) {
	cols, rs, err := parseQuoteMode("'a','b,c','n'\n1,'x''y',NULL\n2.5,X'0AFF','line\none'\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cols, "|") != "a|b,c|n" || len(rs) != 2 {
		t.Fatalf("cols %q rows %v", cols, rs)
	}
	if r := rs[0]; r[0].Kind != KindInt || r[1].Text != "x'y" || !r[2].Null {
		t.Errorf("row 1 %+v", r)
	}
	if r := rs[1]; r[0].Kind != KindFloat || r[1].Kind != KindBytes || r[1].Text != "0aff" || r[2].Text != "line\none" {
		t.Errorf("row 2 %+v", r)
	}
	if _, _, err := parseQuoteMode("'a'\n'unterminated\n"); err == nil {
		t.Error("unterminated text accepted")
	}
	for in, want := range map[string]string{"0.300000000000000044": "0.30000000000000004", "6.0": "6.0", "1.0e+20": "1e+20",
		"9.99999999999999954e-08": "1e-07", "Inf": "Inf", "-Inf": "-Inf", "1.23456788999999989": "1.23456789"} {
		if got := shortFloat(in); got != want {
			t.Errorf("shortFloat(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestSQLiteRefusal(t *testing.T) {
	for stmt, refuse := range map[string]bool{
		"SELECT 1":                false,
		"SELECT 1 -- c":           false,
		"SELECT 'a;b'":            false,
		"SELECT 1\n, 2":           false,
		"SELECT t\n.x FROM t":     false,
		".shell ls":               true,
		"  .output x":             true,
		"#> x":                    true,
		"SELECT 1 # ;\n.shell ls": true, // ; after a MySQL comment ends the statement for SQLite
		"SELECT 'it\\'s'":         true, // unterminated for SQLite
		"SELECT 1 /* open":        true,
		"SELECT 10\n/\n2":         true,
		"SELECT 1\ngo":            true,
		"go":                      true,
		"SELECT 1\ngolang":        false,
		"CREATE TRIGGER tr AFTER INSERT ON t BEGIN\n  UPDATE t SET a = 1;\n  SELECT CASE WHEN 1 THEN 2 END;\nEND": false,
		"CREATE TRIGGER tr AFTER INSERT ON t BEGIN UPDATE t SET a = 1 END":                                        true,
		"SELECT $$a;\nb$$": true,
	} {
		if got := sqliteRefusal(stmt); (got != "") != refuse {
			t.Errorf("%q: refusal %q", stmt, got)
		}
	}
	if !sqliteComplete("SELECT 1;") || sqliteComplete("SELECT 1") || !sqliteComplete("SELECT 1; -- x") || sqliteComplete("SELECT ';'") {
		t.Error("sqliteComplete")
	}
}

func TestVersions(t *testing.T) {
	for in, want := range map[string]string{"16.15 (Ubuntu 16.15-1)": "16.15", "11.8.3-MariaDB-ubu2404": "11.8.3", "8.0.46-0ubuntu0.24.04.4": "8.0.46", "3.45.1": "3.45.1", "x": ""} {
		if got := cleanVersion(in); got != want {
			t.Errorf("cleanVersion(%q) = %q", in, got)
		}
	}
	if !versionLess("3.36.9", "3.37.0") || versionLess("3.45.1", "3.37.0") || versionLess("10.11", "10.11.0") {
		t.Error("versionLess")
	}
	m := map[string]Recorded{"mysql": {Rows: []string{"a"}}, "mysql 9.7": {Rows: []string{"b"}}, "mariadb 11": {Rows: []string{"c"}}}
	for _, tc := range []struct{ eng, v, want string }{{"mysql", "8.0.46", "a"}, {"mysql", "9.7.1", "b"}, {"mariadb", "11.8.3", "c"}} {
		if r, ok := Pick(m, tc.eng, tc.v); !ok || r.Rows[0] != tc.want {
			t.Errorf("Pick(%s %s) = %v", tc.eng, tc.v, r)
		}
	}
	if _, ok := Pick(m, "postgres", "16.1"); ok {
		t.Error("Pick found postgres")
	}
}
