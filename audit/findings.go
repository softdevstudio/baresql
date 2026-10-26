package audit

import (
	"fmt"
	"sort"
	"strings"
)

// Finding codes are a public interface: never rename one, add new ones.
// README.md lists them all.

var severityOrder = map[string]int{"high": 0, "medium": 1, "low": 2}

func (a *analysis) findings(res *Result) []Finding {
	var out []Finding
	add := func(code, sev, subject, msg string, details ...string) {
		out = append(out, Finding{Code: code, Severity: sev, Subject: subject, Message: msg, Details: details})
	}
	pg16 := a.s.Server.VersionNum >= 160000

	// What any role can read, today and in the future.
	byRel := map[string][]string{}
	for _, c := range res.Public.Read {
		if c.Personal != nil && c.Kind != "view" && givesRows(c.Routes) {
			byRel[c.Relation] = append(byRel[c.Relation], c.Column+" ("+strings.Join(c.Personal.Categories, ", ")+")")
		}
	}
	for _, rel := range sortedKeys(byRel) {
		add("public-reads-personal", "high", rel,
			"Every role, including roles created later, can read personal data in "+rel+".", byRel[rel]...)
	}

	for _, l := range res.Logins {
		role := a.s.Roles[l.oid]
		if role.Super {
			add("superuser-login", "medium", l.Name, l.Name+" is a superuser: it can read and change everything, and run programs on the server.")
		}
		for _, x := range a.setRoleTargets(role.OID) {
			if a.s.isSuper(x) && !role.Super {
				add("set-role-superuser", "high", l.Name, fmt.Sprintf("%s can SET ROLE %s, a superuser, and so do anything.", l.Name, a.s.RoleName(x)))
			}
		}
		if !role.Super {
			inh := a.s.inherited(role.OID)
			for _, name := range []string{"pg_read_all_data", "pg_write_all_data"} {
				id := a.s.predefined(name)
				if id == 0 {
					continue
				}
				if p, ok := inh[id]; ok {
					via := ""
					if len(p) > 2 {
						via = " (via " + strings.Join(a.s.between(p), " > ") + ")"
					}
					add("read-all-data", "medium", l.Name, fmt.Sprintf("%s is a member of %s%s: it reaches every table.", l.Name, name, via))
					continue
				}
				// Without inheritance, membership still lets it SET ROLE.
				for _, x := range a.setRoleTargets(role.OID) {
					if _, ok := a.s.inherited(x)[id]; ok {
						msg := fmt.Sprintf("%s can SET ROLE %s and then reach every table.", l.Name, a.s.RoleName(x))
						if x != id {
							msg = fmt.Sprintf("%s can SET ROLE %s, a member of %s, and then reach every table.", l.Name, a.s.RoleName(x), name)
						}
						add("read-all-data", "medium", l.Name, msg)
						break
					}
				}
			}
			if role.CreateRole && !pg16 {
				add("createrole-escalation", "high", l.Name, fmt.Sprintf("%s has CREATEROLE on PostgreSQL %d: it can grant itself any role except superuser.",
					l.Name, a.s.Server.VersionNum/10000))
			}
			for _, sa := range l.Server {
				if sa.Capability == "see-all-queries" {
					add("see-all-queries", "low", l.Name, l.Name+" can see the text of every session's queries, which can hold personal data.")
					continue
				}
				add("server-file-access", "high", l.Name, fmt.Sprintf("%s can %s on the database server.", l.Name, capabilityWords(sa.Capability)),
					routeStrings(sa.Routes)...)
			}
		}
		// Grant options and admin options pass reach on.
		var grantable []string
		for _, c := range l.Read {
			if c.Personal == nil {
				continue
			}
			for _, r := range c.Routes {
				if r.Grantable && r.GivesRows() {
					grantable = append(grantable, c.Relation+"."+quoteIdent(c.Column))
					break
				}
			}
		}
		if len(grantable) > 0 && !role.Super {
			add("grant-option-personal", "medium", l.Name, fmt.Sprintf("%s may grant read access to personal data to other roles.", l.Name), grantable...)
		}
		for _, m := range a.s.Memberships {
			if m.Member == role.OID && m.Admin && !role.Super {
				g := a.s.Roles[m.Role]
				if g != nil && a.rolePersonalReach(g.OID) > 0 {
					add("admin-option", "medium", l.Name, fmt.Sprintf("%s can make any role a member of %s, which reads personal data.", l.Name, g.Name))
				}
			}
		}
		for _, mp := range l.Mappings {
			add("foreign-server-mapping", "low", l.Name, fmt.Sprintf("%s can use foreign server %s (%s) through a user mapping.", l.Name, mp.Server, mp.FDW))
		}
	}

	// Security-definer functions run with their owner's rights.
	for _, f := range a.s.Functions {
		if !f.SecDef || f.Builtin {
			continue
		}
		var runners []string
		for _, l := range res.Logins {
			for _, fa := range l.Functions {
				if fa.Function == f.Signature {
					runners = append(runners, l.Name)
				}
			}
		}
		public := false
		for _, acl := range f.ACL {
			if acl.Priv == "EXECUTE" && acl.Grantee == 0 {
				public = true
			}
		}
		if !hasSearchPath(f.Config) && (public || len(runners) > 0) {
			add("secdef-no-search-path", "high", f.Signature, fmt.Sprintf(
				"%s runs with the rights of %s and doesn't set search_path: whoever can run it may be able to make it run their own code.",
				f.Signature, a.s.RoleName(f.Owner)))
		}
		if n := a.rolePersonalReach(f.Owner); n > 0 && (public || len(runners) > 0) {
			who := strings.Join(runners, ", ")
			if public {
				who = "every role (PUBLIC)"
			}
			add("secdef-executable", "medium", f.Signature, fmt.Sprintf(
				"%s runs with the rights of %s, who can read %d personal data columns; it can be run by %s.",
				f.Signature, a.s.RoleName(f.Owner), n, who))
		}
	}

	// Row security.
	for _, rel := range sortedRelations(a.s) {
		if !rel.RLS || !isBase(rel) {
			continue
		}
		name := a.s.QualifiedName(rel)
		n := 0
		for _, p := range a.s.Policies {
			if p.Rel == rel.OID {
				n++
			}
		}
		if n == 0 {
			msg := "Row security is on for " + name + " but it has no policies: only its owner and roles that bypass row security see any rows."
			// Reading a parent reads its partitions' and children's rows
			// with the parent's row security, not theirs.
			var parents []string
			for _, p := range a.s.Inherits[rel.OID] {
				if pr := a.s.Relations[p]; pr != nil {
					parents = append(parents, a.s.QualifiedName(pr))
				}
			}
			if len(parents) > 0 {
				sort.Strings(parents)
				msg = "Row security is on for " + name + " but it has no policies, so reading it directly gives only its owner and roles that bypass row security any rows. " +
					"Its rows are still read through " + andList(parents) + ", where its own row security doesn't apply."
			}
			add("rls-no-policies", "low", name, msg)
		}
		if owner := a.s.Roles[rel.Owner]; owner != nil && owner.CanLogin && !rel.ForceRLS && !owner.Super {
			add("rls-owner-login", "medium", name, fmt.Sprintf(
				"Row security on %s is not forced and its owner %s can log in: sessions logged in as %s see every row.",
				name, owner.Name, owner.Name))
		}
	}
	// Views that hand out rows row security would hide.
	type bypassed struct{ tables, logins map[string]bool }
	byView := map[string]*bypassed{}
	for _, l := range res.Logins {
		role := a.s.Roles[l.oid]
		for _, c := range l.Read {
			for _, r := range c.Routes {
				if r.Kind != "view" || !strings.HasPrefix(r.Rows, "all rows") {
					continue
				}
				rel := a.relByName(c.Relation)
				if rel == nil || !rel.RLS || a.bypassesRLS(role.OID, rel) {
					continue
				}
				v := r.Views[0].View
				if byView[v] == nil {
					byView[v] = &bypassed{tables: map[string]bool{}, logins: map[string]bool{}}
				}
				byView[v].tables[c.Relation] = true
				byView[v].logins[l.Name] = true
			}
		}
	}
	for _, v := range sortedKeys(byView) {
		b := byView[v]
		add("view-bypasses-row-security", "high", v, fmt.Sprintf(
			"View %s reads %s with rights that skip row security, so these logins see every row: %s.",
			v, strings.Join(sortedKeys(b.tables), ", "), strings.Join(sortedKeys(b.logins), ", ")))
	}

	// Defaults that grant future tables to everyone.
	for _, d := range res.DefaultPrivileges {
		if d.Grantee == "PUBLIC" && (d.Objects == "tables" || d.Objects == "functions") {
			where := "any schema"
			if d.Schema != "" {
				where = "schema " + d.Schema
			}
			add("default-grants-to-public", "medium", d.Creator, fmt.Sprintf(
				"New %s that %s creates in %s are granted %s to PUBLIC.", d.Objects, d.Creator, where, d.Privilege))
		}
	}

	// Passwords stored with MD5 (deprecated in PostgreSQL 18).
	var md5 []string
	for _, l := range res.Logins {
		if a.s.Roles[l.oid].PasswordKind == "md5" {
			md5 = append(md5, l.Name)
		}
	}
	sort.Strings(md5)
	if len(md5) > 0 {
		add("md5-passwords", "medium", "logins", fmt.Sprintf("%d logins have MD5 password hashes, which are weak and deprecated.", len(md5)), md5...)
	}

	// The audit's own connection.
	if !res.Server.TLS && !strings.Contains(res.Server.Target, "@/") && !isLocalTarget(res.Server.Target) {
		add("connection-not-encrypted", "low", res.Server.Target, "The audit connection was not encrypted (TLS).")
	}
	if res.Server.Auth == "password" && !res.Server.TLS {
		add("cleartext-password-login", "medium", res.Server.Target, "The server asked for the password in clear text over an unencrypted connection.")
	}
	if len(a.mismatches) > 0 {
		add("unexplained-access", "medium", "baresql", fmt.Sprintf(
			"In %d places BareSQL's reading of the catalogs disagrees with the server's own checks. The server's answer is used; please report this.",
			len(a.mismatches)), a.mismatches...)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if severityOrder[out[i].Severity] != severityOrder[out[j].Severity] {
			return severityOrder[out[i].Severity] < severityOrder[out[j].Severity]
		}
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Subject < out[j].Subject
	})
	return out
}

func capabilityWords(c string) string {
	switch c {
	case "read-server-files":
		return "read files"
	case "write-server-files":
		return "write files"
	case "run-server-programs":
		return "run programs"
	}
	return c
}

func routeStrings(rs []Route) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.String())
	}
	return out
}

// rolePersonalReach counts the base personal-data columns a role reads
// with its own (inherited) rights.
func (a *analysis) rolePersonalReach(role uint32) int {
	n := 0
	for relID, privs := range a.eval(role) {
		rel := a.s.Relations[relID]
		if !isBase(rel) {
			continue
		}
		for _, c := range rel.Columns {
			if a.personal[colRef{relID, c.Attnum}] != nil && len(privs["SELECT"].colRoutes(c.Attnum)) > 0 {
				n++
			}
		}
	}
	return n
}

func (a *analysis) relByName(q string) *Relation {
	if a.byName == nil {
		a.byName = map[string]*Relation{}
		for _, rel := range a.s.Relations {
			a.byName[a.s.QualifiedName(rel)] = rel
		}
	}
	return a.byName[q]
}

func sortedRelations(s *Snapshot) []*Relation {
	var out []*Relation
	for _, r := range s.Relations {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return s.QualifiedName(out[i]) < s.QualifiedName(out[j]) })
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func isLocalTarget(t string) bool {
	return strings.Contains(t, "@localhost:") || strings.Contains(t, "@127.0.0.1:") || strings.Contains(t, "@[::1]:") || strings.Contains(t, "@::1:")
}
