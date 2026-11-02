package audit

import (
	"strings"
	"testing"
)

// The independent review of 0.3 (several databases, policy conditions):
// each case failed on 5962b4b.

func TestReview030SettingAndConstant(t *testing.T) {
	m := policyMeaning("((tenant_id = (current_setting('app.t'::text))::integer) AND (status = 'active'::text))")
	if m.kind != rowsSetting || len(m.settings) != 1 {
		t.Errorf("kind %d (%q), want rowsSetting", m.kind, m.text)
	}
	// Tied to the role's own name, the setting picks among its own rows.
	if m := policyMeaning("((tenant_id = (current_setting('app.t'::text))::integer) AND (owner = CURRENT_USER))"); m.kind != rowsOwn || len(m.settings) != 0 {
		t.Errorf("own AND setting: kind %d settings %v", m.kind, m.settings)
	}
}

func TestReview030ValueFunctionsNotColumns(t *testing.T) {
	for _, e := range []string{"(CURRENT_CATALOG = 'shop'::name)", "(CURRENT_SCHEMA = 'app'::name)",
		"(SYSTEM_USER = 'scram-sha-256:alice'::text)", "(CURRENT_DATE = '2026-01-01'::date)",
		"(LOCALTIMESTAMP = '2026-01-01'::timestamp without time zone)"} {
		if m := policyMeaning(e); m.kind != rowsUnknown {
			t.Errorf("%s: read as %d %q, want unread", e, m.kind, m.text)
		}
	}
}

func TestReview030ReservedSettings(t *testing.T) {
	if m := policyMeaning("(level = current_setting('pgaudit.log'::text))"); m.kind != rowsUnknown {
		t.Errorf("pgaudit.log read as a session's choice: %q", m.text)
	}
}

func TestReview030WhereTextNamesUnread(t *testing.T) {
	rel := &Relation{OID: 1, Name: "t"}
	a := &analysis{s: &Snapshot{Policies: []Policy{
		{Rel: 1, Name: "p6a", Permissive: true, Cmd: "ALL", Using: "(owner = CURRENT_USER)"},
		{Rel: 1, Name: "p6b", Permissive: true, Cmd: "ALL", Using: "(id > 0)"},
		{Rel: 1, Name: "r", Permissive: false, Cmd: "ALL", Using: "(region = 'eu'::text)"},
	}}}
	got := a.whereText(rel, "SELECT", "rows allowed by policy p6a, p6b and restricted by r")
	for _, want := range []string{"p6b: condition not read", "; or p6b", "and always r: region is 'eu'"} {
		if !strings.Contains(got, want) {
			t.Errorf("RowsWhere %q lacks %q", got, want)
		}
	}
}

func TestReview030RestrictiveFalseAndCheckOnly(t *testing.T) {
	role := &Role{OID: 10, Name: "app", CanLogin: true}
	s := &Snapshot{Roles: map[uint32]*Role{10: role}, RoleByName: map[string]*Role{"app": role}, Policies: []Policy{
		{Rel: 1, Name: "p", Permissive: true, Cmd: "ALL", Roles: []uint32{0}, Using: "true"},
		{Rel: 1, Name: "r", Permissive: false, Cmd: "ALL", Roles: []uint32{0}, Using: "false"},
		{Rel: 2, Name: "c", Permissive: true, Cmd: "ALL", Roles: []uint32{0}, Check: "true"},
	}}
	a := &analysis{s: s}
	if got := a.rowsForCmd(10, &Relation{OID: 1, RLS: true, Owner: 99}, "SELECT"); !strings.HasPrefix(got, "no rows") {
		t.Errorf("true restricted by false: %q", got)
	}
	if got := a.rowsForCmd(10, &Relation{OID: 2, RLS: true, Owner: 99}, "SELECT"); !strings.HasPrefix(got, "no rows") {
		t.Errorf("WITH CHECK only, read: %q", got)
	}
}

// A 0.2 baseline against a 0.3 audit of the same database: a policy that
// is true reads "rows allowed by policy p" then and "all rows (policy p
// lets every row through)" now. That is no growth, and the words on
// conditions are no new route.
func TestReview030BaselineAcrossVersions(t *testing.T) {
	login := func(rows, where string) *Result {
		return &Result{Logins: []LoginReport{{Name: "app", Read: []ColumnReach{{Relation: "s.t", Column: "email",
			Routes: []Route{{Kind: "grant", Role: "app", Rows: rows, RowsWhere: where}}}}}}}
	}
	cmp := Compare(login("rows allowed by policy p", ""), login("all rows (policy p lets every row through)", ""))
	if cmp.GrewAny() || len(cmp.Other) != 0 {
		t.Errorf("grew %v, other %v", cmp.Grew, cmp.Other)
	}
	cmp = Compare(login("rows allowed by policy q", ""), login("rows allowed by policy q", "tenant_id is 1"))
	if cmp.GrewAny() || len(cmp.Other) != 0 {
		t.Errorf("RowsWhere counted as a change: %v %v", cmp.Grew, cmp.Other)
	}
}

func TestReview030DatabaseSets(t *testing.T) {
	one := &Result{Server: ServerReport{Database: "shop"}}
	two := &Result{Server: ServerReport{Database: "shop, crm", Databases: []string{"crm", "shop"}}}
	two2 := &Result{Server: ServerReport{Database: "shop", Databases: []string{"shop"}}}
	if Compare(one, two).Databases == "" || Compare(two, two2).Databases == "" {
		t.Error("audits of different databases compared without a word")
	}
	if Compare(two, two).Databases != "" || Compare(one, one).Databases != "" {
		t.Error("the same databases reported as different")
	}
}

func TestReview030MergeFindings(t *testing.T) {
	f := Finding{Code: "default-grants-to-public", Severity: "medium", Subject: "owner",
		Message: "New tables that owner creates in any schema are granted SELECT to PUBLIC."}
	s := Finding{Code: "superuser-login", Severity: "medium", Subject: "root", Message: "root is a superuser."}
	m := Merge([]*Result{{Server: ServerReport{Database: "a"}, Findings: []Finding{f, s}},
		{Server: ServerReport{Database: "b"}, Findings: []Finding{f, s}}})
	n, sup := 0, 0
	for _, x := range m.Findings {
		switch x.Code {
		case f.Code:
			n++
			if !strings.Contains(x.Message, "In database") {
				t.Errorf("finding names no database: %q", x.Message)
			}
		case s.Code:
			sup++
		}
	}
	if n != 2 || sup != 1 {
		t.Errorf("%d default-grants findings (want 2), %d superuser findings (want 1)", n, sup)
	}
}
