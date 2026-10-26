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
