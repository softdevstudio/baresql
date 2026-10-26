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
// something new: roles it doesn't already inherit, and roles whose
// attributes don't pass to members: superuser and BYPASSRLS.
func (a *analysis) setRoleTargets(login uint32) []uint32 {
	inh := a.s.inherited(login)
	var out []uint32
	for id := range a.s.settable(login) {
		_, inherited := inh[id]
		r := a.s.Roles[id]
		if !inherited || r != nil && (r.Super || r.BypassRLS) {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return a.s.RoleName(out[i]) < a.s.RoleName(out[j]) })
	return out
}

// ownRoute reports whether a route repeats a grant the login has without
// SET ROLE (one to PUBLIC or to a role it already inherits).
func (a *analysis) ownRoute(login uint32, r Route) bool {
	switch r.Kind {
	case "grant", "column-grant", "owner", "predefined":
		if r.public {
			return true
		}
		if r.grantee != 0 {
			_, ok := a.s.inherited(login)[r.grantee]
			return ok
		}
	}
	return false
}

// reader is a role whose rights a login uses: its own, or one it may SET
// ROLE to (setRole names it).
type reader struct {
	role    uint32
	setRole string
}

func (a *analysis) readers(login uint32, public bool) []reader {
	out := []reader{{role: login}}
	if !public {
		for _, x := range a.setRoleTargets(login) {
			out = append(out, reader{role: x, setRole: a.s.RoleName(x)})
		}
	}
	return out
}

// readRows is what reading rel by name gives a role: the Rows of its
// routes, and false when there is nothing to list. A role without USAGE on
// the schema can't name the table, yet pg_stats shows it sampled values of
// the columns it may select (unless row security is in the way). A foreign
// table needs a user mapping for the role.
func (a *analysis) readRows(role uint32, rel *Relation) (string, bool) {
	rows := a.rowsFor(role, rel)
	if !a.usage(role, rel.Schema) {
		if !isBase(rel) || rel.Kind == 'f' || rel.RLS && !strings.HasPrefix(rows, "all rows") {
			return "", false
		}
		return statsPrefix + "no USAGE on schema " + quoteIdent(a.s.Schemas[rel.Schema].Name) +
			", but pg_stats shows it sampled values of the columns it may select", true
	}
	if why := a.mappingFailure(role, rel); why != "" {
		return failsPrefix + why, true
	}
	return rows, true
}

// reads works out every relation column a role can read: directly, after
// SET ROLE, and through views.
func (a *analysis) reads(login uint32, public bool) readSet {
	rs := readSet{}
	roleOf := map[string]uint32{}
	for _, rd := range a.readers(login, public) {
		roleOf[rd.setRole] = rd.role
		for relID, privs := range a.eval(rd.role) {
			gs := privs["SELECT"]
			if gs == nil {
				continue
			}
			rel := a.s.Relations[relID]
			rows, ok := a.readRows(rd.role, rel)
			if !ok {
				continue
			}
			for _, col := range rel.Columns {
				for _, r := range gs.colRoutes(col.Attnum) {
					if rd.setRole != "" {
						// The same grant without SET ROLE is listed already,
						// unless SET ROLE lets more through: a role that
						// bypasses row security, or has a policy of its own.
						if own, _ := a.readRows(login, rel); a.ownRoute(login, r) && own == rows {
							continue
						}
						r.SetRole = rd.setRole
					}
					r.Rows = rows
					rs.add(relID, col.Attnum, r)
				}
			}
		}
	}
	// Through views: each view column read by a route that gives rows
	// hands out its base columns.
	for k, routes := range rs {
		rel := a.s.Relations[k.rel]
		if rel.Kind != 'v' {
			continue
		}
		setRoles := map[string]bool{}
		for _, r := range routes {
			if r.GivesRows() {
				setRoles[r.SetRole] = true
			}
		}
		for sr := range setRoles {
			role := roleOf[sr]
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

// writeRows is what changing rel by name gives a role: the Rows of its
// routes (row security on INSERT, UPDATE and DELETE), and false when it
// can't name the table at all.
func (a *analysis) writeRows(role uint32, rel *Relation, priv string) (string, bool) {
	if !a.usage(role, rel.Schema) {
		return "", false
	}
	if why := a.mappingFailure(role, rel); why != "" {
		return failsPrefix + why, true
	}
	return a.rowsForCmd(role, rel, priv), true
}

// writes lists what a role can change.
func (a *analysis) writes(login uint32, public bool) []TableWrite {
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
	for _, rd := range a.readers(login, public) {
		for relID, privs := range a.eval(rd.role) {
			rel := a.s.Relations[relID]
			for _, priv := range []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE"} {
				gs := privs[priv]
				if gs == nil {
					continue
				}
				rows, ok := a.writeRows(rd.role, rel, priv)
				if !ok {
					continue
				}
				k := key{relID, priv}
				fix := func(r Route) (Route, bool) {
					if rd.setRole != "" {
						if own, _ := a.writeRows(login, rel, priv); a.ownRoute(login, r) && own == rows {
							return r, false
						}
						r.SetRole = rd.setRole
					}
					r.Rows = rows
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
					routes = append(routes, Route{Kind: "grant", Role: "PUBLIC", public: true})
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
		r := Route{Kind: "grant", Role: a.s.RoleName(m.Role), grantee: m.Role, public: m.Role == 0}
		if v, ok := (&rights{s: a.s, role: login, closure: inh}).via(m.Role); ok {
			r.Via = v
		}
		out = append(out, MappingAccess{Server: m.Server, FDW: m.FDW, Routes: []Route{r}})
	}
	return out
}

func (a *analysis) loginReport(role *Role) LoginReport {
	lr := LoginReport{Name: a.s.RoleName(role.OID), Attributes: roleAttributes(role), Password: role.PasswordKind, oid: role.OID}
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
	if !a.canConnect(role.OID) {
		// Its grants here give it nothing until it may connect: keep them
		// in the report, marked, and out of the counts.
		lr.CannotConnect = true
		why := connectPrefix + "no CONNECT privilege on database " + quoteIdent(a.s.Server.Database)
		if role.ConnLimit == 0 {
			why = connectPrefix + "its connection limit is 0"
		}
		for i := range lr.Read {
			for j := range lr.Read[i].Routes {
				lr.Read[i].Routes[j].Rows = why
			}
		}
		for i := range lr.Write {
			for j := range lr.Write[i].Routes {
				lr.Write[i].Routes[j].Rows = why
			}
		}
		lr.Functions, lr.Mappings = []FunctionAccess{}, []MappingAccess{}
		var server []ServerAccess
		for _, sa := range lr.Server {
			if sa.Capability == "read-server-files" || sa.Capability == "write-server-files" || sa.Capability == "run-server-programs" || sa.Capability == "see-all-queries" {
				// Granted through roles, these work from any database the
				// login may connect to; through functions, only from this one.
				var routes []Route
				for _, r := range sa.Routes {
					if r.Kind != "function" {
						routes = append(routes, r)
					}
				}
				if len(routes) > 0 {
					server = append(server, ServerAccess{Capability: sa.Capability, Routes: routes})
				}
			}
		}
		lr.Server = server
	}
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
		if !givesRows(w.Routes) {
			continue
		}
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
