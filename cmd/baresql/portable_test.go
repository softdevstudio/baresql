package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/softdevstudio/baresql/internal/testenv"
	"github.com/softdevstudio/baresql/portable"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

func writeSQL(t *testing.T, sql string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "q.sql")
	if err := os.WriteFile(p, []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCLILint(t *testing.T) {
	bad := writeSQL(t, "SELECT 7 / 2;\nSELECT name FROM t WHERE name = 'x';\n")
	code, out, _ := runCLI(t, "lint", bad)
	if code != 1 || !strings.Contains(out, "q.sql:1:8: warning:") || !strings.Contains(out, "[integer-division]") ||
		strings.Contains(out, "text-compare-case") || !strings.Contains(out, "1 note about differences that depend on the data not shown") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	code, out, _ = runCLI(t, "lint", "--all", bad)
	if code != 1 || !strings.Contains(out, "[text-compare-case]") {
		t.Fatalf("--all: exit %d:\n%s", code, out)
	}
	code, out, _ = runCLI(t, "lint", "--ignore", "integer-division", bad)
	if code != 0 || !strings.Contains(out, "No problems found.") {
		t.Fatalf("--ignore: exit %d:\n%s", code, out)
	}
	// 7 / 2 answers alike on SQLite and PostgreSQL.
	code, out, _ = runCLI(t, "lint", "--engines", "sqlite,postgres", bad)
	if code != 0 {
		t.Fatalf("--engines sqlite,postgres: exit %d:\n%s", code, out)
	}
	code, out, _ = runCLI(t, "lint", "--engines", "postgres,mysql", bad)
	if code != 1 {
		t.Fatalf("--engines postgres,mysql: exit %d:\n%s", code, out)
	}
	code, out, _ = runCLI(t, "lint", "--format", "json", bad)
	var ps []portable.Problem
	if code != 1 || json.Unmarshal([]byte(out), &ps) != nil || len(ps) != 1 || ps[0].Code != "integer-division" || ps[0].Line != 1 {
		t.Fatalf("json: exit %d:\n%s", code, out)
	}
	good := writeSQL(t, "SELECT 7 * 1.0 / 2;")
	if code, out, _ := runCLI(t, "lint", "--format", "json", good); code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatalf("clean json: exit %d: %s", code, out)
	}
	for _, args := range [][]string{{"lint"}, {"lint", "--format", "xml", good}, {"lint", "--ignore", "nope", good},
		{"lint", "--engines", "oracle,sqlite", good}, {"lint", "--engines", "sqlite", good}, {"lint", filepath.Join(t.TempDir(), "missing.sql")}} {
		if code, _, errs := runCLI(t, args...); code != 2 || errs == "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, errs)
		}
	}
	// Standard input.
	stdin = strings.NewReader("SELECT a || b FROM t")
	defer func() { stdin = os.Stdin }()
	if code, out, _ := runCLI(t, "lint", "-"); code != 1 || !strings.Contains(out, "stdin:1:8") {
		t.Fatalf("stdin: exit %d:\n%s", code, out)
	}
}

func TestCLIRules(t *testing.T) {
	code, out, _ := runCLI(t, "rules")
	if code != 0 || !strings.Contains(out, "integer-division") || !strings.Contains(out, "operator-not-everywhere") {
		t.Fatalf("list: exit %d:\n%s", code, out)
	}
	code, out, _ = runCLI(t, "rules", "integer-division")
	if code != 0 || !strings.Contains(out, "SELECT 7 / 2") || !strings.Contains(out, "3.5000") || !strings.Contains(out, "The same on every engine:") {
		t.Fatalf("entry: exit %d:\n%s", code, out)
	}
	code, out, _ = runCLI(t, "rules", "type-not-everywhere")
	if code != 0 || !strings.Contains(out, "DATETIME") || !strings.Contains(out, "PostgreSQL") {
		t.Fatalf("table entry: exit %d:\n%s", code, out)
	}
	code, out, _ = runCLI(t, "rules", "--format", "json")
	var cat portable.CatalogJSON
	if code != 0 || json.Unmarshal([]byte(out), &cat) != nil || len(cat.Rules) != len(portable.Catalog()) || cat.RecordedOn["postgres"] == "" {
		t.Fatalf("json: exit %d", code)
	}
	if code, _, errs := runCLI(t, "rules", "nope"); code != 2 || !strings.Contains(errs, "unknown rule") {
		t.Fatalf("unknown: exit %d, %q", code, errs)
	}
}

func TestCLICompareArguments(t *testing.T) {
	f := writeSQL(t, "SELECT 1;")
	for _, args := range [][]string{{"compare", f}, {"compare", "--sqlite", f}, {"compare", "--sqlite", "--postgres", "x"},
		{"compare", "--sqlite", "--mysql", "http://x", f}, {"compare", "--sqlite", "--postgres", "postgres://x", "--format", "xml", f}} {
		if code, _, errs := runCLI(t, args...); code != 2 || errs == "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, errs)
		}
	}
}

func TestCLICompare(t *testing.T) {
	prog := testenv.SQLiteProgram(t)
	pg := testenv.DSN(t, testenv.PG)
	f := writeSQL(t, "SELECT 7 / 2;\nSELECT 1 / 0;\nSELECT 1;\n")
	code, out, errs := runCLI(t, "compare", "--sqlite", "--sqlite-program", prog, "--postgres", pg, f)
	if code != 1 || !strings.Contains(out, "q.sql:2  SELECT 1 / 0") || !strings.Contains(out, "refused by PostgreSQL") ||
		strings.Contains(out, "q.sql:1") || !strings.Contains(out, "3 statements: 2 answered alike, 1 differ.") {
		t.Fatalf("exit %d, stderr %q:\n%s", code, errs, out)
	}
	code, out, _ = runCLI(t, "compare", "--sqlite", "--postgres", pg, "--all", "--format", "json", f)
	var cmp portable.Comparison
	if code != 1 || json.Unmarshal([]byte(out), &cmp) != nil || len(cmp.Statements) != 3 || cmp.Summary.Differ != 1 {
		t.Fatalf("json: exit %d:\n%s", code, out)
	}
	same := writeSQL(t, "SELECT 1;")
	if code, out, _ := runCLI(t, "compare", "--sqlite", "--postgres", pg, same); code != 0 || !strings.Contains(out, "1 answered alike, 0 differ") {
		t.Fatalf("same: exit %d:\n%s", code, out)
	}
	// A database to start from.
	db := filepath.Join(t.TempDir(), "start.db")
	if err := exec(prog, db, "CREATE TABLE seeded (x INTEGER); INSERT INTO seeded VALUES (5);"); err != nil {
		t.Fatal(err)
	}
	q := writeSQL(t, "SELECT x FROM seeded;")
	if code, out, _ := runCLI(t, "compare", "--sqlite="+db, "--postgres", pg, q); code != 1 || !strings.Contains(out, "SQLite: 5") {
		t.Fatalf("--sqlite=file: exit %d:\n%s", code, out)
	}
}

func exec(prog string, args ...string) error {
	return osexec.Command(prog, args...).Run()
}

// TestReadmeDocumentsCodes: README.md lists every rule code and finding
// code, which are a public interface.
func TestReadmeDocumentsCodes(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range portable.RuleCodes() {
		if !strings.Contains(string(readme), "| `"+code+"` |") {
			t.Errorf("README.md does not list rule %s", code)
		}
	}
	src, err := os.ReadFile("../../audit/findings.go")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, part := range strings.Split(string(src), `add("`)[1:] {
		code := part[:strings.IndexByte(part, '"')]
		n++
		if !strings.Contains(string(readme), "| `"+code+"` |") {
			t.Errorf("README.md does not list finding %s", code)
		}
	}
	if n < 15 {
		t.Errorf("found only %d finding codes in audit/findings.go", n)
	}
}
