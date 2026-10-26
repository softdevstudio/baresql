package audit

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/softdevstudio/baresql/internal/pgwire"
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
