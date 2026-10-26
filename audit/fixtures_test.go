package audit

import "testing"

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
