package audit

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/softdevstudio/baresql/internal/pgwire"
	"github.com/softdevstudio/baresql/internal/testenv"
)

func TestFixtureShop(t *testing.T) {
	f := loadFixture(t, "shop")
	res := auditFixture(t, f, true)
	checkLines(t, f, res, "expected.txt")
}

func TestFixtureSaaS(t *testing.T) {
	f := loadFixture(t, "saas")
	res := auditFixture(t, f, true)
	checkLines(t, f, res, "expected.txt")
}

func TestFixtureClinic(t *testing.T) {
	f := loadFixture(t, "clinic")
	res := auditFixture(t, f, true)
	checkLines(t, f, res, "expected.txt")
}

func TestFixtureClean(t *testing.T) {
	f := loadFixture(t, "clean")
	res := auditFixture(t, f, true)
	checkLines(t, f, res, "expected.txt")
	if len(res.Findings) != 0 {
		t.Errorf("a well set up database got findings: %+v", res.Findings)
	}
}

// TestFixtureEdge holds the setups the independent review of 0.1.0 found
// the audit getting wrong; expected.txt has the server's own answers.
func TestFixtureEdge(t *testing.T) {
	// A real role named "PUBLIC" isn't one of the fixture's own (edge_...),
	// so it is dropped here, after the fixture's database.
	t.Cleanup(func() {
		base, err := pgwire.ParseDSN(testenv.DSN(t, testenv.PG))
		if err != nil {
			return
		}
		if c, err := pgwire.Connect(context.Background(), base); err == nil {
			c.Exec(context.Background(), `DROP ROLE IF EXISTS "PUBLIC"`)
			c.Close()
		}
	})
	f := loadFixture(t, "edge")
	res := auditFixture(t, f, true)
	checkLines(t, f, res, "expected.txt")
	rel := map[string]RelationReport{}
	for _, r := range res.Relations {
		rel[r.Name] = r
	}
	col := func(relation, column string) *Personal {
		for _, c := range rel[relation].Columns {
			if c.Name == column {
				return c.Personal
			}
		}
		return nil
	}
	// 1: the owner's cast to text never ran, so the table was sampled.
	if !rel["types.feelings"].Sampled {
		t.Error("types.feelings was not sampled")
	}
	if p := col("types.feelings", "email"); p == nil || p.Confidence != "high" {
		t.Errorf("types.feelings.email: %+v", p)
	}
	// 2: foreign tables are never read, not even through their parents;
	// the parents take what their local partitions and children hold.
	for _, name := range []string{"app.events_recent", "app.log", "app.patient_visits"} {
		if !rel[name].Sampled {
			t.Errorf("%s was not sampled", name)
		}
	}
	for _, name := range []string{"app.events_archive", "app.log_archive", "app.events"} {
		if rel[name].Sampled {
			t.Errorf("%s was sampled", name)
		}
	}
	for _, n := range res.NotSeen {
		if strings.Contains(n.What, "app.") || strings.Contains(n.What, "types.") {
			t.Errorf("not seen: %s: %s", n.What, n.Why)
		}
	}
	if p := col("app.events", "note"); p == nil || strings.Join(p.Categories, ",") != "email" ||
		!strings.Contains(strings.Join(p.Evidence, "|"), "found in partition app.events_recent") {
		t.Errorf("app.events.note: %+v", p)
	}
	if p := col("app.visits", "note"); p == nil || !strings.Contains(strings.Join(p.Evidence, "|"), "found in child table app.patient_visits") {
		t.Errorf("app.visits.note: %+v", p)
	}
	// 9: the login that may not connect gets nothing counted.
	for _, l := range res.Logins {
		if l.Name == "edge_noconnect" && (!l.CannotConnect || l.Summary.Columns != 0) {
			t.Errorf("edge_noconnect: cannot_connect %v, %d columns", l.CannotConnect, l.Summary.Columns)
		}
	}
	if len(res.Mismatches) > 0 {
		t.Errorf("mismatches: %v", res.Mismatches)
	}
}

// TestAuditorWithoutSuperuser audits the shop fixture as shop_analyst, who
// reads a few columns: the audit still covers every login, samples only
// what the analyst may read, and says what it couldn't see.
func TestAuditorWithoutSuperuser(t *testing.T) {
	f := loadFixture(t, "shop")
	ctx := context.Background()
	admin, err := pgwire.Connect(ctx, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec(ctx, "ALTER ROLE shop_analyst PASSWORD 'analyst-pass'"); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	cfg := f.admin
	cfg.User, cfg.Password = "shop_analyst", "analyst-pass"
	res, err := Run(ctx, cfg, RunOptions{Logins: f.logins, Sample: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Server.Superuser || res.Server.Auditor != "shop_analyst" {
		t.Fatalf("auditor %s superuser=%v", res.Server.Auditor, res.Server.Superuser)
	}
	if len(res.Mismatches) > 0 {
		t.Fatalf("mismatches: %v", res.Mismatches)
	}
	var notSeen []string
	for _, n := range res.NotSeen {
		notSeen = append(notSeen, n.What+": "+n.Why)
	}
	all := strings.Join(notSeen, "\n")
	for _, want := range []string{
		"password types (md5 or SCRAM): only a superuser can read pg_authid",
		"values of shop.api_credentials: not sampled: the auditor's login may not read it",
		"some values of shop.customers: not sampled: the auditor's login may read only some of its columns",
		"other databases",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("not_seen lacks %q:\n%s", want, all)
		}
	}
	// The reach of each login doesn't depend on who audits.
	super := auditFixture(t, f, true)
	key := func(r *Result) string {
		var ks []string
		for _, l := range r.Logins {
			for _, c := range l.Read {
				ks = append(ks, l.Name+" "+c.Relation+"."+c.Column)
			}
		}
		sort.Strings(ks)
		return strings.Join(ks, "\n")
	}
	if key(res) != key(super) {
		t.Error("the analyst's audit lists different reach than the superuser's")
	}
}
