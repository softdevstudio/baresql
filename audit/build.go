package audit

import (
	"sort"
	"strings"
)

// readEntry collects the routes to one relation column.
type readKey struct {
	rel    uint32
	attnum int
}

type readSet map[readKey][]Route

func (rs readSet) add(rel uint32, attnum int, r Route) {
	k := readKey{rel, attnum}
	for _, old := range rs[k] {
		if old.String() == r.String() {
			return
		}
	}
	rs[k] = append(rs[k], r)
}

// setRoleTargets returns the roles a login may SET ROLE to that give it
// something new: roles it doesn't already inherit, and superuser roles
// (being a superuser is never inherited).
func (a *analysis) setRoleTargets(login uint32) []uint32 {
	inh := a.s.inherited(login)
	var out []uint32
	for id := range a.s.settable(login) {
		if _, ok := inh[id]; !ok || a.s.isSuper(id) {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return a.s.RoleName(out[i]) < a.s.RoleName(out[j]) })
	return out
}

// ownRoute reports whether a route only repeats what the login has without
// SET ROLE (a grant to PUBLIC or to a role it already inherits).
func (a *analysis) ownRoute(login uint32, r Route) bool {
	if r.Kind == "grant" || r.Kind == "column-grant" || r.Kind == "owner" || r.Kind == "predefined" {
		if r.Role == "PUBLIC" {
			return true
		}
		if role := a.s.RoleByName[r.Role]; role != nil {
			_, ok := a.s.inherited(login)[role.OID]
			return ok
		}
	}
	return false
}

// reads works out every relation column a role can read: directly, after
// SET ROLE, and through views.
func (a *analysis) reads(login uint32, public bool) readSet {
	rs := readSet{}
	type reader struct {
		role    uint32
		setRole string
	}
	readers := []reader{{role: login}}
	if !public {
		for _, x := range a.setRoleTargets(login) {
			readers = append(readers, reader{role: x, setRole: a.s.RoleName(x)})
		}
	}
	for _, rd := range readers {
		for relID, privs := range a.eval(rd.role) {
			gs := privs["SELECT"]
			if gs == nil {
				continue
			}
			rel := a.s.Relations[relID]
			for _, col := range rel.Columns {
				for _, r := range gs.colRoutes(col.Attnum) {
					if rd.setRole != "" {
						if a.ownRoute(login, r) {
							continue
						}
						r.SetRole = rd.setRole
					}
					r.Rows = a.rowsFor(rd.role, rel)
					rs.add(relID, col.Attnum, r)
				}
			}
		}
	}
	// Through views: each readable view column hands out its base columns.
	for k, routes := range rs {
		rel := a.s.Relations[k.rel]
		if rel.Kind != 'v' {
			continue
		}
		setRoles := map[string]bool{}
		for _, r := range routes {
			setRoles[r.SetRole] = true
		}
		for sr := range setRoles {
			role := login
			if sr != "" {
				role = a.s.RoleByName[sr].OID
			}
			if why := a.viewFailure(rel, role, 0); why != "" {
				for i := range rs[k] {
					if rs[k][i].SetRole == sr {
						rs[k][i].Rows = failsPrefix + why
					}
				}
				continue
			}
			var exps []exposure
			a.expose(role, rel, k.attnum, nil, 0, &exps)
			for _, e := range exps {
				rs.add(e.rel.OID, e.attnum, Route{Kind: "view", Views: e.hops, SetRole: sr, Rows: a.rowsFor(e.rights, e.rel)})
			}
		}
	}
	for k := range rs {
		sortRoutes(rs[k])
	}
	return rs
}

func (a *analysis) columnReach(rs readSet) []ColumnReach {
	type entry struct {
		cr     ColumnReach
		attnum int
	}
	var es []entry
	for k, routes := range rs {
		rel := a.s.Relations[k.rel]
		col := rel.Column(k.attnum)
		if col == nil {
			continue
		}
		es = append(es, entry{ColumnReach{Relation: a.s.QualifiedName(rel), Kind: relKind(rel), Column: col.Name,
			Personal: a.personal[colRef{rel.OID, col.Attnum}], Routes: routes}, col.Attnum})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].cr.Relation != es[j].cr.Relation {
			return es[i].cr.Relation < es[j].cr.Relation
		}
		return es[i].attnum < es[j].attnum
	})
	out := make([]ColumnReach, len(es))
	for i, e := range es {
		out[i] = e.cr
	}
	return out
}

// writes lists what a role can change.
func (a *analysis) writes(login uint32, public bool) []TableWrite {
	type reader struct {
		role    uint32
		setRole string
	}
	readers := []reader{{role: login}}
	if !public {
		for _, x := range a.setRoleTargets(login) {
			readers = append(readers, reader{role: x, setRole: a.s.RoleName(x)})
		}
	}
	type key struct {
		rel  uint32
		priv string
	}
	type acc struct {
		all    bool
		cols   map[int]bool
		routes []Route
	}
	m := map[key]*acc{}
	add := func(k key, r Route) {
		x := m[k]
		if x == nil {
			x = &acc{cols: map[int]bool{}}
			m[k] = x
		}
		for _, old := range x.routes {
			if old.String() == r.String() {
				return
			}
		}
		x.routes = append(x.routes, r)
	}
	for _, rd := range readers {
		for relID, privs := range a.eval(rd.role) {
			for _, priv := range []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE"} {
				gs := privs[priv]
				if gs == nil {
					continue
				}
				k := key{relID, priv}
				fix := func(r Route) (Route, bool) {
					if rd.setRole != "" {
						if a.ownRoute(login, r) {
							return r, false
						}
						r.SetRole = rd.setRole
					}
					return r, true
				}
				if gs.All {
					for _, r := range gs.Table {
						if r, ok := fix(r); ok {
							add(k, r)
							m[k].all = true
						}
					}
					continue
				}
				for attnum, routes := range gs.Columns {
					for _, r := range routes {
						if r, ok := fix(r); ok {
							add(k, r)
							m[k].cols[attnum] = true
						}
					}
				}
			}
		}
	}
	var out []TableWrite
	for k, x := range m {
		rel := a.s.Relations[k.rel]
		tw := TableWrite{Relation: a.s.QualifiedName(rel), Kind: relKind(rel), Privilege: k.priv, Routes: x.routes,
			Personal: a.relPersonal(rel)}
		if !x.all {
			for _, c := range rel.Columns {
				if x.cols[c.Attnum] {
					tw.Columns = append(tw.Columns, c.Name)
				}
			}
		}
		sortRoutes(tw.Routes)
		out = append(out, tw)
	}
	order := map[string]int{"INSERT": 0, "UPDATE": 1, "DELETE": 2, "TRUNCATE": 3}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Relation != out[j].Relation {
			return out[i].Relation < out[j].Relation
		}
		return order[out[i].Privilege] < order[out[j].Privilege]
	})
	return out
}

func (a *analysis) relPersonal(rel *Relation) bool {
	for _, c := range rel.Columns {
		if a.personal[colRef{rel.OID, c.Attnum}] != nil {
			return true
		}
	}
	return false
}

// functions lists the security-definer functions a login can run (other
// than its own) and the server-access functions granted to it.
func (a *analysis) functions(login uint32, public bool) []FunctionAccess {
	inh := a.s.inherited(login)
	var out []FunctionAccess
	for _, f := range a.s.Functions {
		var routes []Route
		if public {
			for _, acl := range f.ACL {
				if acl.Priv == "EXECUTE" && acl.Grantee == 0 {
					routes = append(routes, Route{Kind: "grant", Role: "PUBLIC"})
				}
			}
		} else {
			routes = append(routes, a.funcRoutes[login][f.OID]...)
			for _, x := range a.setRoleTargets(login) {
				for _, r := range a.funcRoutes[x][f.OID] {
					if !a.ownRoute(login, r) {
						r.SetRole = a.s.RoleName(x)
						routes = append(routes, r)
					}
				}
			}
		}
		if len(routes) == 0 {
			continue
		}
		if f.Builtin {
			// A superuser can run them anyway; list only real grants.
			var granted []Route
			for _, r := range routes {
				if r.Kind != "superuser" {
					granted = append(granted, r)
				}
			}
			if len(granted) == 0 {
				continue
			}
			routes = granted
		} else if _, own := inh[f.Owner]; own && !public {
			continue // running its own function gains nothing
		}
		sortRoutes(routes)
		out = append(out, FunctionAccess{Function: f.Signature, Owner: a.s.RoleName(f.Owner), SecurityDefiner: f.SecDef,
			SearchPathSet: hasSearchPath(f.Config), ServerAccess: f.Builtin, Routes: routes})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Function < out[j].Function })
	return out
}

func hasSearchPath(config []string) bool {
	for _, c := range config {
		if strings.HasPrefix(strings.ToLower(c), "search_path=") {
			return true
		}
	}
	return false
}

// serverAccess lists what a login can do on the database server itself.
func (a *analysis) serverAccess(login uint32, fns []FunctionAccess) []ServerAccess {
	caps := map[string][]Route{}
	type src struct {
		role    uint32
		setRole string
	}
	srcs := []src{{role: login}}
	for _, x := range a.setRoleTargets(login) {
		srcs = append(srcs, src{x, a.s.RoleName(x)})
	}
	roleCaps := map[string][]string{
		"pg_read_server_files":      {"read-server-files"},
		"pg_write_server_files":     {"write-server-files"},
		"pg_execute_server_program": {"run-server-programs"},
		"pg_read_all_stats":         {"see-all-queries"},
		"pg_monitor":                {"see-all-queries"},
	}
	for _, sr := range srcs {
		if a.s.isSuper(sr.role) {
			for _, c := range []string{"read-server-files", "write-server-files", "run-server-programs", "see-all-queries"} {
				caps[c] = append(caps[c], Route{Kind: "superuser", Role: a.s.RoleName(sr.role), SetRole: sr.setRole})
			}
			continue
		}
		inh := a.s.inherited(sr.role)
		names := make([]string, 0, len(roleCaps))
		for n := range roleCaps {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			id := a.s.predefined(name)
			p, ok := inh[id]
			if id == 0 || !ok {
				continue
			}
			for _, c := range roleCaps[name] {
				caps[c] = append(caps[c], Route{Kind: "predefined", Role: name, Via: a.s.between(p), SetRole: sr.setRole})
			}
		}
	}
	for _, f := range fns {
		if !f.ServerAccess {
			continue
		}
		c := "read-server-files"
		if strings.HasPrefix(f.Function, "lo_export") || strings.Contains(f.Function, ".lo_export") {
			c = "write-server-files"
		}
		for _, r := range f.Routes {
			caps[c] = append(caps[c], Route{Kind: "function", Role: f.Function, Via: r.Via, SetRole: r.SetRole})
		}
	}
	var out []ServerAccess
	for _, c := range []string{"read-server-files", "write-server-files", "run-server-programs", "see-all-queries"} {
		if routes := dedupRoutes(caps[c]); len(routes) > 0 {
			out = append(out, ServerAccess{Capability: c, Routes: routes})
		}
	}
	return out
}

func dedupRoutes(rs []Route) []Route {
	seen := map[string]bool{}
	var out []Route
	for _, r := range rs {
		if !seen[r.String()] {
			seen[r.String()] = true
			out = append(out, r)
		}
	}
	sortRoutes(out)
	return out
}

// mappings lists the foreign servers a login can reach through a user
// mapping (its own, an inherited role's, or PUBLIC's).
func (a *analysis) mappings(login uint32) []MappingAccess {
	inh := a.s.inherited(login)
	var out []MappingAccess
	for _, m := range a.s.Mappings {
		_, mine := inh[m.Role]
		if !(m.Role == 0 || mine) || !a.serverUse[login][m.ServerOID] {
			continue
		}
		r := Route{Kind: "grant", Role: a.s.RoleName(m.Role)}
		if v, ok := (&rights{s: a.s, role: login, closure: inh}).via(m.Role); ok {
			r.Via = v
		}
		out = append(out, MappingAccess{Server: m.Server, FDW: m.FDW, Routes: []Route{r}})
	}
	return out
}

func (a *analysis) loginReport(role *Role) LoginReport {
	lr := LoginReport{Name: role.Name, Attributes: roleAttributes(role), Password: role.PasswordKind}
	var member []string
	for _, m := range a.s.Memberships {
		if m.Member != role.OID || m.Implicit {
			continue
		}
		s := a.s.RoleName(m.Role)
		var opts []string
		if a.s.Server.VersionNum >= 160000 {
			if !m.Inherit {
				opts = append(opts, "no inherit")
			}
			if !m.Set {
				opts = append(opts, "no set")
			}
		} else if !m.Inherit {
			opts = append(opts, "no inherit")
		}
		if m.Admin {
			opts = append(opts, "admin")
		}
		if len(opts) > 0 {
			s += " (" + strings.Join(opts, ", ") + ")"
		}
		member = append(member, s)
	}
	sort.Strings(member)
	lr.MemberOf = member
	for _, x := range a.setRoleTargets(role.OID) {
		lr.CanSetRole = append(lr.CanSetRole, a.s.RoleName(x))
	}
	rs := a.reads(role.OID, false)
	lr.Read = a.columnReach(rs)
	lr.Write = a.writes(role.OID, false)
	lr.Functions = a.functions(role.OID, false)
	lr.Server = a.serverAccess(role.OID, lr.Functions)
	lr.Mappings = a.mappings(role.OID)
	lr.Summary = a.summary(lr)
	return lr
}

func (a *analysis) publicReport() LoginReport {
	lr := LoginReport{Name: "PUBLIC", Attributes: []string{}}
	rs := a.reads(0, true)
	lr.Read = a.columnReach(rs)
	lr.Write = a.writes(0, true)
	lr.Functions = a.functions(0, true)
	lr.Summary = a.summary(lr)
	return lr
}

func roleAttributes(r *Role) []string {
	out := []string{}
	add := func(ok bool, s string) {
		if ok {
			out = append(out, s)
		}
	}
	add(r.Super, "superuser")
	add(r.CreateRole, "createrole")
	add(r.CreateDB, "createdb")
	add(r.Replication, "replication")
	add(r.BypassRLS, "bypassrls")
	add(!r.Inherit, "noinherit")
	return out
}

func (a *analysis) summary(lr LoginReport) LoginSummary {
	var s LoginSummary
	rels := map[string]bool{}
	pTables := map[string]bool{}
	cats := map[string]bool{}
	for _, c := range lr.Read {
		if !givesRows(c.Routes) {
			continue
		}
		rels[c.Relation] = true
		s.Columns++
		if c.Personal != nil && c.Kind != "view" {
			s.PersonalColumns++
			pTables[c.Relation] = true
			for _, cat := range c.Personal.Categories {
				cats[cat] = true
			}
		}
	}
	s.Relations = len(rels)
	s.PersonalTables = len(pTables)
	for c := range cats {
		s.Categories = append(s.Categories, c)
	}
	sort.Strings(s.Categories)
	if s.Categories == nil {
		s.Categories = []string{}
	}
	wt := map[string]bool{}
	wp := map[string]bool{}
	for _, w := range lr.Write {
		wt[w.Relation] = true
		if w.Personal {
			wp[w.Relation] = true
		}
	}
	s.WritableTables = len(wt)
	s.PersonalWritable = len(wp)
	return s
}

func (a *analysis) relationReports() []RelationReport {
	var out []RelationReport
	for _, rel := range a.s.Relations {
		rr := RelationReport{Name: a.s.QualifiedName(rel), Kind: relKind(rel), Owner: a.s.RoleName(rel.Owner),
			SecurityInvoker: rel.SecurityInvoker, Sampled: a.sampled[rel.OID]}
		if rel.RLS {
			rr.RowSecurity = "on"
			if rel.ForceRLS {
				rr.RowSecurity = "forced"
			}
		}
		if root := a.s.Relations[rel.PartitionRoot]; root != nil {
			rr.PartitionOf = a.s.QualifiedName(root)
		}
		for _, p := range a.s.Policies {
			if p.Rel != rel.OID {
				continue
			}
			pr := PolicyReport{Name: p.Name, Command: p.Cmd, Permissive: p.Permissive, Using: p.Using, Check: p.Check}
			for _, r := range p.Roles {
				pr.Roles = append(pr.Roles, a.s.RoleName(r))
			}
			rr.Policies = append(rr.Policies, pr)
		}
		for _, c := range rel.Columns {
			cr := ColumnReport{Name: c.Name, Type: c.Type, Personal: a.personal[colRef{rel.OID, c.Attnum}]}
			if lin, ok := rel.Lineage[c.Attnum]; ok {
				for _, src := range lin.Sources {
					if srel := a.s.Relations[src.Rel]; srel != nil {
						name := a.s.QualifiedName(srel)
						if src.Attnum == 0 {
							cr.Sources = append(cr.Sources, name+".*")
						} else if sc := srel.Column(src.Attnum); sc != nil {
							cr.Sources = append(cr.Sources, name+"."+quoteIdent(sc.Name))
						}
					}
				}
			}
			rr.Columns = append(rr.Columns, cr)
		}
		out = append(out, rr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (a *analysis) defaultPrivileges() []DefaultPriv {
	out := []DefaultPriv{}
	for _, d := range a.s.DefaultACLs {
		schema := ""
		if sc := a.s.Schemas[d.Schema]; sc != nil {
			schema = sc.Name
		}
		for _, acl := range d.ACL {
			if acl.Grantee == d.Creator {
				continue
			}
			out = append(out, DefaultPriv{Creator: a.s.RoleName(d.Creator), Schema: schema, Objects: d.ObjType,
				Privilege: acl.Priv, Grantee: a.s.RoleName(acl.Grantee)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		return x.Creator+x.Schema+x.Objects+x.Privilege+x.Grantee < y.Creator+y.Schema+y.Objects+y.Privilege+y.Grantee
	})
	return out
}
