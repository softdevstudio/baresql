package audit

import "testing"

func TestPolicyMeaning(t *testing.T) {
	cases := []struct {
		expr string
		kind rowKind
		text string
	}{
		{"(tenant_id = (current_setting('app.tenant_id'::text))::integer)", rowsSetting, "tenant_id matches the setting app.tenant_id"},
		{"((tenant_id)::text = current_setting('app.tenant_id'::text))", rowsSetting, "tenant_id matches the setting app.tenant_id"},
		{"(owner = CURRENT_USER)", rowsOwn, "owner is the name of the current role"},
		{"(owner = (CURRENT_USER)::text)", rowsOwn, "owner is the name of the current role"},
		{"(owner_name = SESSION_USER)", rowsOwn, "owner_name is the name of the login"},
		{"true", rowsEvery, "every row"},
		{"false", rowsNone, "no row"},
		{"(region = ANY (ARRAY['eu'::text, 'us'::text]))", rowsFixed, "region is one of 'eu', 'us'"},
		{"((tenant_id = 3) AND (owner = CURRENT_USER))", rowsFixed, "tenant_id is 3 and owner is the name of the current role"},
		{"((current_setting('app.role'::text, true) = 'admin'::text) OR (owner = CURRENT_USER))", rowsSetting,
			"the setting app.role is 'admin' or owner is the name of the current role"},
		{"((tenant_id = 1) OR true)", rowsEvery, "every row"},
		{"((tenant_id = 1) AND false)", rowsNone, "no row"},
		{"((tenant_id = 1) AND pg_has_role(CURRENT_USER, 'x'::name, 'member'::text))", rowsUnknown, ""},
		{"pg_has_role(CURRENT_USER, 'x'::name, 'member'::text)", rowsUnknown, ""},
		{"(tenant_id = ANY ((string_to_array(current_setting('app.tenants'::text), ','::text))::integer[]))", rowsUnknown, ""},
		{"flag", rowsUnknown, ""},
		{"(tenant_id = ( SELECT 1))", rowsUnknown, ""},
		{"(tenant_id = (current_setting('search_path'::text))::integer)", rowsUnknown, ""},
		{"(tenant_id = (public.current_setting('app.x'::text))::integer)", rowsUnknown, ""},
		{"(\"Owner\" = CURRENT_USER)", rowsOwn, "\"Owner\" is the name of the current role"},
		{"", rowsUnknown, ""},
		{"((", rowsUnknown, ""},
		{"(a = )", rowsUnknown, ""},
	}
	for _, c := range cases {
		m := policyMeaning(c.expr)
		if m.kind != c.kind || m.text != c.text {
			t.Errorf("%s: got %d %q, want %d %q", c.expr, m.kind, m.text, c.kind, c.text)
		}
	}
}

func FuzzPolicyMeaning(f *testing.F) {
	f.Add("(tenant_id = (current_setting('app.tenant_id'::text))::integer)")
	f.Add("((a = 1) OR (b = ANY (ARRAY[1, 2])))")
	f.Fuzz(func(t *testing.T, s string) {
		policyMeaning(s)
	})
}
