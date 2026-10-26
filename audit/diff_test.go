package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/softdevstudio/baresql/internal/pgwire"
)

// TestCompareAfterGrant audits the shop fixture, gives the intern access to
// customers and takes the analyst's away, and checks that the comparison
// names exactly those changes.
func TestCompareAfterGrant(t *testing.T) {
	f := loadFixture(t, "shop")
	before := auditFixture(t, f, true)
	ctx := context.Background()
	c, err := pgwire.Connect(ctx, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Exec(ctx, `GRANT SELECT (email) ON shop.customers TO shop_intern;
		REVOKE SELECT ON shop.products FROM shop_analyst`); err != nil {
		t.Fatal(err)
	}
	after := auditFixture(t, f, true)
	cmp := Compare(before, after)
	var grew, shrank []string
	for _, ch := range cmp.Grew {
		grew = append(grew, ch.Kind+" "+ch.Login+" "+ch.Object)
	}
	for _, ch := range cmp.Shrank {
		shrank = append(shrank, ch.Kind+" "+ch.Login+" "+ch.Object)
	}
	if got := strings.Join(grew, "|"); got != "read shop_intern shop.customers.email" {
		t.Errorf("grew: %s", got)
	}
	want := "read shop_analyst shop.products.id|read shop_analyst shop.products.name|read shop_analyst shop.products.price|read shop_analyst shop.products.sku"
	if got := strings.Join(shrank, "|"); got != want {
		t.Errorf("shrank: %s", got)
	}
	if !cmp.GrewAny() {
		t.Error("GrewAny is false")
	}
	// Nothing changed: nothing grew.
	if again := Compare(after, auditFixture(t, f, true)); again.GrewAny() || len(again.Shrank) > 0 {
		t.Errorf("an unchanged database compared as changed: %+v", again)
	}
}

// TestCompareRowSecurity: a route that row security filters down to no rows
// isn't reach, so a policy that lets rows through makes reach grow.
func TestCompareRowSecurity(t *testing.T) {
	f := loadFixture(t, "saas")
	before := auditFixture(t, f, true)
	ctx := context.Background()
	c, err := pgwire.Connect(ctx, f.admin)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// invoices forces row security, so support.invoice_summary (saas_owner's
	// rights) handed out no rows until saas_owner gets a policy.
	if err := c.Exec(ctx, `CREATE POLICY owner_read ON app.invoices FOR SELECT TO saas_owner USING (true)`); err != nil {
		t.Fatal(err)
	}
	after := auditFixture(t, f, true)
	cmp := Compare(before, after)
	var grew []string
	for _, ch := range cmp.Grew {
		grew = append(grew, ch.Kind+" "+ch.Login+" "+ch.Object)
	}
	if got, want := strings.Join(grew, "|"), "read saas_agent app.invoices.amount|read saas_agent app.invoices.id"; got != want {
		t.Errorf("grew: %s, want %s", got, want)
	}
	for _, l := range before.Logins {
		if l.Name == "saas_agent" && l.Summary.Columns >= len(l.Read) {
			t.Errorf("saas_agent's summary counts columns that give no rows: %d of %d", l.Summary.Columns, len(l.Read))
		}
	}
}

// TestCompareRowsWiden: row security that lets more rows through is growth,
// even when the login reached the column before (review finding H6).
func TestCompareRowsWiden(t *testing.T) {
	result := func(rows ...string) *Result {
		var routes []Route
		for _, r := range rows {
			routes = append(routes, Route{Kind: "grant", Role: "reader", Rows: r})
		}
		return &Result{Logins: []LoginReport{{Name: "reader", Read: []ColumnReach{{Relation: "app.rls", Column: "ssn",
			Personal: &Personal{Categories: []string{"national-id"}}, Routes: routes}}}}}
	}
	for _, tc := range []struct {
		name     string
		old, new *Result
		want     string
	}{
		{"row security turned off", result("rows allowed by policy p"), result(""),
			"rows reader app.rls.ssn (row security no longer narrows it: all rows)"},
		{"BYPASSRLS", result("rows allowed by policy p"), result("all rows (BYPASSRLS)"),
			"rows reader app.rls.ssn (row security no longer narrows it: all rows)"},
		{"another permissive policy", result("rows allowed by policy p"), result("rows allowed by policy p, q"),
			"rows reader app.rls.ssn (policy q now lets rows through)"},
		{"a restrictive policy dropped", result("rows allowed by policy p and restricted by r"), result("rows allowed by policy p"),
			"rows reader app.rls.ssn (restrictive policy r no longer applies)"},
		{"narrower", result(""), result("rows allowed by policy p"), ""},
		{"unchanged", result("rows allowed by policy p"), result("rows allowed by policy p"), ""},
	} {
		cmp := Compare(tc.old, tc.new)
		var got []string
		for _, ch := range cmp.Grew {
			got = append(got, ch.String())
		}
		if strings.Join(got, "|") != tc.want {
			t.Errorf("%s: grew %q, want %q", tc.name, got, tc.want)
		}
		if cmp.GrewAny() != (tc.want != "") {
			t.Errorf("%s: GrewAny %v", tc.name, cmp.GrewAny())
		}
	}
}
