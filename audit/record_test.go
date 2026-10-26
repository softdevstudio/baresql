package audit

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/softdevstudio/baresql/internal/pgwire"
)

// TestRecordResults writes what the site quotes to $BARESQL_RECORD_DIR/audit
// (scripts/record-results.sh sets it): each fixture's report, and the case
// studies run as scenarios on the fixtures. Each audit is the one the
// fixture tests check line by line, so nothing recorded here is unchecked.
// Files hold what the CLI prints for the same audit: summary.txt is
// `baresql audit`, lines.txt `--format lines`, diff.txt `baresql diff`.
func TestRecordResults(t *testing.T) {
	dir := os.Getenv("BARESQL_RECORD_DIR")
	if dir == "" {
		t.Skip("set BARESQL_RECORD_DIR to record results")
	}
	out := filepath.Join(dir, "audit")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	var counts bytes.Buffer
	// edge, the corner cases, is counted and not recorded: the site shows
	// the other four as sample reports.
	dropPublicRoleAfter(t)
	for _, name := range []string{"shop", "saas", "clinic", "clean", "edge"} {
		f := loadFixture(t, name)
		res := auditFixture(t, f, true)
		checkLines(t, f, res, "expected.txt")
		if name != "edge" {
			record(t, filepath.Join(out, name), res)
		}
		countFixture(t, &counts, f, res)
	}
	writeFile(t, filepath.Join(out, "counts.txt"), counts.String())
	recordAgent(t, filepath.Join(out, "agent"))
	recordSupport(t, filepath.Join(out, "support"))
	recordQuarterly(t, filepath.Join(out, "quarterly"))
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// record writes report.html, audit.json, summary.txt and lines.txt.
func record(t *testing.T, dir string, res *Result) {
	t.Helper()
	if len(res.Mismatches) > 0 {
		t.Fatalf("%s: mismatches with the server: %v", dir, res.Mismatches)
	}
	if err := WriteFiles(dir, res); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	WriteSummary(&b, res)
	fmt.Fprintf(&b, "Wrote baresql-report/report.html and baresql-report/audit.json\n")
	writeFile(t, filepath.Join(dir, "summary.txt"), b.String())
	writeFile(t, filepath.Join(dir, "lines.txt"), strings.Join(res.Lines(), "\n")+"\n")
}

func recordDiff(t *testing.T, path string, old, cur *Result) *Comparison {
	t.Helper()
	cmp := Compare(old, cur)
	var b bytes.Buffer
	cmp.Write(&b)
	writeFile(t, path, b.String())
	return cmp
}

func runAt(t *testing.T, f *fixture, logins []string, when time.Time) *Result {
	t.Helper()
	res, err := Run(context.Background(), f.admin, RunOptions{Logins: logins, Sample: true, Now: when})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func execAll(t *testing.T, f *fixture, sql string) {
	t.Helper()
	c, err := pgwire.Connect(context.Background(), f.admin)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", f.name, err)
	}
}

// countFixture notes the numbers the site quotes about a fixture.
func countFixture(t *testing.T, w *bytes.Buffer, f *fixture, res *Result) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", f.name, "expected.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	reads, personal := 0, 0
	for _, l := range res.Logins {
		reads += l.Summary.Columns
		personal += l.Summary.PersonalColumns
	}
	sev := map[string]int{}
	for _, fd := range res.Findings {
		sev[fd.Severity]++
	}
	var perVersion []string
	for v := 13; v <= 18; v++ {
		perVersion = append(perVersion, fmt.Sprintf("%d on %d", len(filterVersion(lines, v*10000, false)), v))
	}
	fmt.Fprintf(w, "%s: %d logins; %d readable login-columns, %d of them personal data; findings %d high, %d medium, %d low; facts checked exactly: %s\n",
		f.name, len(res.Logins), reads, personal, sev["high"], sev["medium"], sev["low"], strings.Join(perVersion, ", "))
}

// recordAgent: an AI support agent's login, audited before go-live, fixed,
// and audited again.
func recordAgent(t *testing.T, dir string) {
	f := loadFixture(t, "saas")
	if f.ver < 150000 {
		t.Logf("agent scenario needs security_invoker (PostgreSQL 15); skipped on %d", f.ver)
		return
	}
	agent := []string{"saas_agent"}
	before := runAt(t, f, agent, time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))
	record(t, filepath.Join(dir, "before"), before)
	// The fix: the view runs with the reader's rights, and support reads
	// the accounts and tickets of the tenant the session works for.
	fix := `ALTER VIEW support.ticket_context SET (security_invoker = true);
GRANT SELECT (id, email, full_name) ON app.accounts TO saas_support;
CREATE POLICY support_read ON app.accounts FOR SELECT TO saas_support
  USING (tenant_id = current_setting('app.tenant_id')::integer);
ALTER POLICY support_read ON app.tickets USING (tenant_id = current_setting('app.tenant_id')::integer);`
	execAll(t, f, fix)
	writeFile(t, filepath.Join(dir, "fix.sql"), fix+"\n")
	after := runAt(t, f, agent, time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC))
	record(t, filepath.Join(dir, "after"), after)
	recordDiff(t, filepath.Join(dir, "diff.txt"), before, after)
	for _, fd := range after.Findings {
		if fd.Code == "view-bypasses-row-security" {
			t.Errorf("agent after the fix: %s %s", fd.Code, fd.Subject)
		}
	}
}

// recordSupport: a support view that hands out card numbers.
func recordSupport(t *testing.T, dir string) {
	f := loadFixture(t, "shop")
	team := []string{"shop_alice", "shop_bob"}
	before := runAt(t, f, team, time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	record(t, filepath.Join(dir, "before"), before)
	fix := `SET ROLE shop_owner;
DROP VIEW support.customer_lookup;
CREATE VIEW support.customer_lookup AS
  SELECT c.id, c.first_name || ' ' || c.last_name AS customer_name, c.email, c.phone
  FROM shop.customers c;
GRANT SELECT ON support.customer_lookup TO shop_support_team;
RESET ROLE;`
	execAll(t, f, fix)
	writeFile(t, filepath.Join(dir, "fix.sql"), fix+"\n")
	after := runAt(t, f, team, time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC))
	record(t, filepath.Join(dir, "after"), after)
	cmp := recordDiff(t, filepath.Join(dir, "diff.txt"), before, after)
	if cmp.GrewAny() {
		t.Errorf("support after the fix reaches more: %v", cmp.Grew)
	}
}

// recordQuarterly: the audit kept from one quarter, changes made during the
// next, and the comparison a quarterly review (or a nightly --baseline job)
// shows.
func recordQuarterly(t *testing.T, dir string) {
	f := loadFixture(t, "shop")
	q3 := runAt(t, f, f.logins, time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC))
	record(t, filepath.Join(dir, "q3"), q3)
	// Changes nobody reviewed: a refunds table with bank accounts (the
	// schema's default privileges make it readable by every role), the
	// intern added to the support team, the analyst's product access
	// removed. The account numbers are made here and never stored.
	g := &fakeGen{r: rand.New(rand.NewSource(3))}
	var rows []string
	for i := 1; i <= 12; i++ {
		rows = append(rows, fmt.Sprintf("(%d, %d, '%s', %d.90)", i, i, g.iban([]string{"DE", "FR", "NL", "GB"}[i%4]), 10+i))
	}
	changes := `SET ROLE shop_owner;
CREATE TABLE shop.refunds (id integer PRIMARY KEY, order_id integer, iban text, amount numeric(10,2));
INSERT INTO shop.refunds VALUES ` + strings.Join(rows, ", ") + `;
RESET ROLE;
GRANT shop_support_team TO shop_intern;
REVOKE SELECT ON shop.products FROM shop_analyst;`
	execAll(t, f, changes)
	writeFile(t, filepath.Join(dir, "changes.sql"), strings.Replace(changes, strings.Join(rows, ", "),
		"... twelve refunds with IBANs, made by the test ...", 1)+"\n")
	q4 := runAt(t, f, f.logins, time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC))
	record(t, filepath.Join(dir, "q4"), q4)
	cmp := recordDiff(t, filepath.Join(dir, "diff.txt"), q3, q4)
	if !cmp.GrewAny() {
		t.Error("the quarter's changes grew no reach")
	}
	// What `baresql audit --baseline q3/audit.json` prints.
	var b bytes.Buffer
	WriteSummary(&b, q4)
	fmt.Fprintf(&b, "Wrote baresql-report/report.html and baresql-report/audit.json\n")
	cmp.Write(&b)
	writeFile(t, filepath.Join(dir, "baseline.txt"), b.String())
	t.Logf("quarterly: %d grew, %d gone, %d other", len(cmp.Grew), len(cmp.Shrank), len(cmp.Other))

	// The fix: the refunds table loses its PUBLIC grant, and the default
	// privileges stop handing new tables to PUBLIC.
	fix := `REVOKE SELECT ON shop.refunds FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE shop_owner IN SCHEMA shop REVOKE SELECT ON TABLES FROM PUBLIC;`
	execAll(t, f, fix)
	writeFile(t, filepath.Join(dir, "fix.sql"), fix+"\n")
	fixed := runAt(t, f, f.logins, time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC))
	record(t, filepath.Join(dir, "fixed"), fixed)
	recordDiff(t, filepath.Join(dir, "fixed-diff.txt"), q4, fixed)
	for _, fd := range fixed.Findings {
		if (fd.Code == "public-reads-personal" && fd.Subject == "shop.refunds") || fd.Code == "default-grants-to-public" {
			t.Errorf("quarterly after the fix: %s %s", fd.Code, fd.Subject)
		}
	}
}
