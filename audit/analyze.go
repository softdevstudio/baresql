package audit

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/softdevstudio/baresql/internal/pgwire"
)

// analysis turns a snapshot (and the server's privilege answers) into the
// Result: what each login can read and change, and how.
type analysis struct {
	ctx context.Context
	c   *pgwire.Conn
	s   *Snapshot

	evals      map[uint32]map[uint32]map[string]*grantSet // role -> relation -> privilege
	funcRoutes map[uint32]map[uint32][]Route              // role -> function -> EXECUTE routes
	serverUse  map[uint32]map[uint32]bool                 // role -> foreign server -> USAGE
	personal   map[colRef]*Personal                       // per relation column
	sampled    map[uint32]bool
	// throughParent: partitions and children sampled through their top
	// parent, because the auditor may not read them on their own.
	throughParent map[uint32]uint32
	kids          map[uint32][]uint32
	// usageTruth: the server's answer to whether a subject may use a schema;
	// connect: whether it may connect to the audited database.
	usageTruth map[[2]uint32]bool
	connect    map[uint32]bool
	mismatches []string
	byName     map[string]*Relation
	auditor    uint32 // the auditor's role when it isn't a superuser
}

var kindNames = map[byte]string{'r': "table", 'p': "partitioned table", 'v': "view", 'm': "materialized view", 'f': "foreign table"}

func relKind(rel *Relation) string {
	if rel.IsPartition {
		return "partition"
	}
	return kindNames[rel.Kind]
}

// eval returns the reconciled privileges of a role, working them out (from
// the catalogs only) for roles the server wasn't asked about.
func (a *analysis) eval(role uint32) map[uint32]map[string]*grantSet {
	if e, ok := a.evals[role]; ok {
		return e
	}
	e := a.s.rightsOf(role).evaluate()
	a.evals[role] = e
	return e
}

func (a *analysis) canSelect(role uint32, rel *Relation, attnum int) bool {
	gs := a.eval(role)[rel.OID]["SELECT"]
	if attnum == 0 {
		if gs != nil && gs.All {
			return true
		}
		for _, c := range rel.Columns {
			if len(gs.colRoutes(c.Attnum)) == 0 {
				return false
			}
		}
		return len(rel.Columns) > 0
	}
	return len(gs.colRoutes(attnum)) > 0
}

// usage reports whether a role may name the relations of a schema: the
// server's answer for the subjects it was asked about, the catalogs'
// otherwise.
func (a *analysis) usage(role, schema uint32) bool {
	if ok, asked := a.usageTruth[[2]uint32{role, schema}]; asked {
		return ok
	}
	return a.s.rightsOf(role).usage(schema)
}

// checkUsage notes where the catalogs and the server disagree on schema
// USAGE.
func (a *analysis) checkUsage(subjects []uint32) {
	var ids []uint32
	for id, sc := range a.s.Schemas {
		if sc.Audited {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, sub := range subjects {
		rt := a.s.rightsOf(sub)
		for _, id := range ids {
			server, asked := a.usageTruth[[2]uint32{sub, id}]
			if !asked {
				continue
			}
			if mine := rt.usage(id); mine != server {
				what := "BareSQL found USAGE the server denies"
				if server {
					what = "the server allows USAGE and BareSQL can't tell why"
				}
				a.mismatches = append(a.mismatches, fmt.Sprintf("%s USAGE on schema %s: %s",
					a.s.RoleName(sub), quoteIdent(a.s.Schemas[id].Name), what))
			}
		}
	}
}

// canConnect reports whether a login may connect to the audited database:
// it has CONNECT and its connection limit isn't 0.
func (a *analysis) canConnect(role uint32) bool {
	r := a.s.Roles[role]
	if r == nil {
		return false
	}
	if r.Super {
		return true
	}
	if r.ConnLimit == 0 {
		return false
	}
	ok, asked := a.connect[role]
	return ok || !asked
}

// mappingFailure says why role can't read foreign table rel, or "": most
// wrappers need a user mapping for the role reading (or for PUBLIC), and
// PostgreSQL looks it up for the role itself, not through memberships.
// file_fdw needs none.
func (a *analysis) mappingFailure(role uint32, rel *Relation) string {
	if rel.Kind != 'f' || rel.ForeignServer == 0 || rel.FDW == "file_fdw" {
		return ""
	}
	for _, m := range a.s.Mappings {
		if m.ServerOID == rel.ForeignServer && (m.Role == role || m.Role == 0) {
			return ""
		}
	}
	return "no user mapping for " + a.s.RoleName(role) + " on foreign server " + quoteIdent(rel.ServerName)
}

// canPrivAny reports whether a role holds priv on column attnum of rel, or
// with anyColumn on at least one column.
func (a *analysis) canPrivAny(role uint32, rel *Relation, attnum int, priv string) bool {
	gs := a.eval(role)[rel.OID][priv]
	if gs == nil {
		return false
	}
	if gs.All {
		return true
	}
	if attnum != anyColumn {
		return len(gs.colRoutes(attnum)) > 0
	}
	return len(gs.Columns) > 0
}

// canSelectAny reports whether a role may read at least one column of rel.
func (a *analysis) canSelectAny(role uint32, rel *Relation) bool {
	gs := a.eval(role)[rel.OID]["SELECT"]
	if gs != nil && gs.All {
		return true
	}
	for _, c := range rel.Columns {
		if len(gs.colRoutes(c.Attnum)) > 0 {
			return true
		}
	}
	return false
}

// rowsFor says which rows of rel a role sees, when rel has row security.
func (a *analysis) rowsFor(role uint32, rel *Relation) string {
	return a.rowsForCmd(role, rel, "SELECT")
}

// cmdVerb names what a command does to rows, for the Rows texts.
var cmdVerb = map[string]string{"SELECT": "read", "INSERT": "insert", "UPDATE": "update", "DELETE": "delete"}

// rowsForCmd says which rows row security lets a role read, insert,
// update or delete (TRUNCATE ignores row security).
func (a *analysis) rowsForCmd(role uint32, rel *Relation, cmd string) string {
	if !rel.RLS || cmdVerb[cmd] == "" {
		return ""
	}
	if r := a.s.Roles[role]; r != nil && r.Super {
		return "all rows (superuser bypasses row security)"
	}
	if r := a.s.Roles[role]; r != nil && r.BypassRLS {
		return "all rows (BYPASSRLS)"
	}
	closure := a.s.inherited(role)
	if _, owner := closure[rel.Owner]; owner && !rel.ForceRLS {
		return "all rows (owner; row security not forced)"
	}
	var permissive, restrictive []string
	for _, p := range a.s.Policies {
		if p.Rel != rel.OID || (p.Cmd != "ALL" && p.Cmd != cmd) {
			continue
		}
		applies := false
		for _, r := range p.Roles {
			if _, ok := closure[r]; ok || r == 0 {
				applies = true
				break
			}
		}
		if !applies {
			continue
		}
		if p.Permissive {
			permissive = append(permissive, quoteIdent(p.Name))
		} else {
			restrictive = append(restrictive, quoteIdent(p.Name))
		}
	}
	if len(permissive) == 0 {
		return "no rows (row security on, no policy lets this role " + cmdVerb[cmd] + ")"
	}
	out := "rows allowed by policy " + strings.Join(permissive, ", ")
	if len(restrictive) > 0 {
		out += " and restricted by " + strings.Join(restrictive, ", ")
	}
	return out
}

// bypassesRLS reports whether a role reads every row of rel.
func (a *analysis) bypassesRLS(role uint32, rel *Relation) bool {
	return strings.HasPrefix(a.rowsFor(role, rel), "all rows")
}

// exposure is a base column whose data a view hands out.
type exposure struct {
	rel    *Relation
	attnum int
	hops   []ViewHop
	rights uint32 // whose rights read the base relation
}

// expose follows a view column down to the base columns it reads. current
// is the role reading the view; each hop reads with the view owner's
// rights, or with current's for security_invoker views (PostgreSQL checks
// those as the current user even inside another view). A hop that fails
// (see viewFailure) or whose rights don't cover a column exposes nothing.
func (a *analysis) expose(current uint32, v *Relation, attnum int, hops []ViewHop, depth int, out *[]exposure) {
	if depth > 30 || v.Lineage == nil {
		return
	}
	lin, ok := v.Lineage[attnum]
	if !ok {
		return
	}
	rights, who := v.Owner, a.s.RoleName(v.Owner)
	if v.SecurityInvoker {
		rights, who = current, "the reader"
	}
	next := append(append([]ViewHop{}, hops...), ViewHop{View: a.s.QualifiedName(v), Owner: who})
	for _, src := range lin.Sources {
		srel := a.s.Relations[src.Rel]
		if srel == nil {
			continue // outside the audited schemas
		}
		attnums := []int{src.Attnum}
		if src.Attnum == 0 {
			attnums = attnums[:0]
			for _, c := range srel.Columns {
				attnums = append(attnums, c.Attnum)
			}
		}
		for _, an := range attnums {
			if !a.canSelect(rights, srel, an) || a.mappingFailure(rights, srel) != "" {
				continue
			}
			if srel.Kind == 'v' {
				if a.viewFailure(srel, current, depth+1) == "" {
					a.expose(current, srel, an, next, depth+1, out)
				}
				continue
			}
			*out = append(*out, exposure{rel: srel, attnum: an, hops: next, rights: rights})
		}
	}
}

// viewFailure says why reading view v fails for role current, or "" when
// it doesn't. PostgreSQL checks every column the view's query reads (its
// Needs) with the owner's rights, or current's for a security_invoker
// view, and refuses the whole query when one is missing, even when the
// query names only columns that don't need it.
func (a *analysis) viewFailure(v *Relation, current uint32, depth int) string {
	if v.Kind != 'v' || depth > 30 {
		return ""
	}
	rights, who := v.Owner, "its owner "+a.s.RoleName(v.Owner)
	if v.SecurityInvoker {
		rights, who = current, "the reader"
	}
	var missing, locks []string
	for _, n := range v.Needs {
		srel := a.s.Relations[n.Rel]
		if srel == nil {
			continue // outside the audited schemas: BareSQL can't tell
		}
		var ok bool
		switch {
		case n.Priv == "UPDATE":
			ok = a.canPrivAny(rights, srel, n.Attnum, "UPDATE")
		case n.Attnum == anyColumn:
			ok = a.canSelectAny(rights, srel)
		default:
			ok = a.canSelect(rights, srel, n.Attnum)
		}
		if !ok {
			name := a.s.QualifiedName(srel)
			if c := srel.Column(n.Attnum); c != nil {
				name += "." + quoteIdent(c.Name)
			}
			if n.Priv == "UPDATE" {
				locks = append(locks, name)
			} else {
				missing = append(missing, name)
			}
			continue
		}
		if why := a.mappingFailure(rights, srel); why != "" {
			return "it reads foreign table " + a.s.QualifiedName(srel) + ", and there is " + why
		}
		if srel.Kind == 'v' {
			if why := a.viewFailure(srel, current, depth+1); why != "" {
				return "it reads view " + a.s.QualifiedName(srel) + ", which fails: " + why
			}
		}
	}
	if len(missing) == 0 && len(locks) > 0 {
		sort.Strings(locks)
		return "the view locks rows with FOR UPDATE, which needs UPDATE on " + andList(locks) + ", and " + who + " may not update it"
	}
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)
	list := missing
	if len(list) > 3 {
		list = append(list[:3:3], fmt.Sprintf("%d more", len(missing)-3))
	}
	return "the view needs " + andList(list) + ", which " + who + " may not read"
}

// andList joins words as in a sentence: a, b and c.
func andList(xs []string) string {
	if len(xs) < 2 {
		return strings.Join(xs, "")
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// subjects lists the roles to ask the server about: the logins, the roles
// they can SET ROLE to, and the owners of views (whose rights views use).
func (a *analysis) subjects(logins []*Role) []uint32 {
	set := map[uint32]bool{}
	for _, l := range logins {
		set[l.OID] = true
		for id := range a.s.settable(l.OID) {
			set[id] = true
		}
	}
	for _, rel := range a.s.Relations {
		if rel.Kind == 'v' {
			set[rel.Owner] = true
		}
	}
	var out []uint32
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// functionTruth asks the server who may EXECUTE each listed function and
// who may use each foreign server.
func (a *analysis) functionTruth(subjects []uint32) error {
	a.funcRoutes = map[uint32]map[uint32][]Route{}
	a.serverUse = map[uint32]map[uint32]bool{}
	ids := func(xs []uint32) string {
		var ss []string
		for _, x := range xs {
			ss = append(ss, strconv.FormatUint(uint64(x), 10))
		}
		return "{" + strings.Join(ss, ",") + "}"
	}
	var fns []uint32
	for _, f := range a.s.Functions {
		fns = append(fns, f.OID)
	}
	allowed := map[[2]uint32]bool{}
	if len(fns) > 0 && len(subjects) > 0 {
		res, err := a.c.QueryArgs(a.ctx, `SELECT s.s, f.f FROM pg_catalog.unnest($1::oid[]) s(s)
			CROSS JOIN pg_catalog.unnest($2::oid[]) f(f)
			WHERE pg_catalog.has_function_privilege(s.s, f.f, 'EXECUTE')`, ids(subjects), ids(fns))
		if err != nil {
			return fmt.Errorf("audit: asking the server about functions: %w", err)
		}
		for _, r := range res.Rows {
			allowed[[2]uint32{u32(r[0]), u32(r[1])}] = true
		}
	}
	for _, sub := range subjects {
		rt := a.s.rightsOf(sub)
		a.funcRoutes[sub] = map[uint32][]Route{}
		for _, f := range a.s.Functions {
			routes := rt.execRoutes(f)
			ok := allowed[[2]uint32{sub, f.OID}]
			switch {
			case ok && len(routes) == 0:
				a.mismatches = append(a.mismatches, fmt.Sprintf("%s EXECUTE on %s: the server allows it and BareSQL can't tell why", a.s.RoleName(sub), f.Signature))
				routes = []Route{{Kind: "unexplained"}}
			case !ok && len(routes) > 0:
				a.mismatches = append(a.mismatches, fmt.Sprintf("%s EXECUTE on %s: BareSQL found a route the server denies", a.s.RoleName(sub), f.Signature))
				routes = nil
			}
			if len(routes) > 0 {
				a.funcRoutes[sub][f.OID] = routes
			}
		}
	}
	var servers []uint32
	seen := map[uint32]bool{}
	for _, m := range a.s.Mappings {
		if !seen[m.ServerOID] {
			seen[m.ServerOID] = true
			servers = append(servers, m.ServerOID)
		}
	}
	if len(servers) > 0 && len(subjects) > 0 {
		res, err := a.c.QueryArgs(a.ctx, `SELECT s.s, v.v FROM pg_catalog.unnest($1::oid[]) s(s)
			CROSS JOIN pg_catalog.unnest($2::oid[]) v(v)
			WHERE pg_catalog.has_server_privilege(s.s, v.v, 'USAGE')`, ids(subjects), ids(servers))
		if err != nil {
			return fmt.Errorf("audit: asking the server about foreign servers: %w", err)
		}
		for _, r := range res.Rows {
			sub := u32(r[0])
			if a.serverUse[sub] == nil {
				a.serverUse[sub] = map[uint32]bool{}
			}
			a.serverUse[sub][u32(r[1])] = true
		}
	}
	return nil
}

// execRoutes: how a role may EXECUTE a function.
func (r *rights) execRoutes(f *Function) []Route {
	if r.super {
		return []Route{{Kind: "superuser", Role: r.s.RoleName(r.role), grantee: r.role}}
	}
	var out []Route
	// Like a table's, a function's owner holds what the access list gives
	// it, until it revokes EXECUTE from itself.
	ownerHas := f.Owner != 0 && holds(f.ACL, f.Owner, "EXECUTE")
	if v, ok := r.via(f.Owner); ok && ownerHas {
		out = append(out, Route{Kind: "owner", Role: r.s.RoleName(f.Owner), Via: v, grantee: f.Owner})
	}
	seen := map[uint32]bool{}
	for _, a := range f.ACL {
		if a.Priv != "EXECUTE" || a.Grantee == f.Owner && ownerHas || seen[a.Grantee] {
			continue
		}
		if v, ok := r.via(a.Grantee); ok {
			seen[a.Grantee] = true
			out = append(out, Route{Kind: "grant", Role: r.s.RoleName(a.Grantee), Via: v, Grantable: a.Grantable,
				grantee: a.Grantee, public: a.Grantee == 0})
		}
	}
	return out
}

// children maps each relation to its partitions and inheritance children.
func (a *analysis) children() map[uint32][]uint32 {
	if a.kids == nil {
		a.kids = map[uint32][]uint32{}
		for child, parents := range a.s.Inherits {
			for _, p := range parents {
				a.kids[p] = append(a.kids[p], child)
			}
		}
		for _, k := range a.kids {
			sort.Slice(k, func(i, j int) bool { return k[i] < k[j] })
		}
	}
	return a.kids
}

// descendants lists every audited relation below rel (partitions of
// partitions, children of children), each once.
func (a *analysis) descendants(rel *Relation) []*Relation {
	var out []*Relation
	seen := map[uint32]bool{rel.OID: true}
	var walk func(id uint32, depth int)
	walk = func(id uint32, depth int) {
		if depth > 100 {
			return
		}
		for _, c := range a.children()[id] {
			if seen[c] {
				continue
			}
			seen[c] = true
			if cr := a.s.Relations[c]; cr != nil {
				out = append(out, cr)
			}
			walk(c, depth+1)
		}
	}
	walk(rel.OID, 0)
	return out
}

// auditorColumns are the sampleable columns the auditor may read.
func (a *analysis) auditorColumns(rel *Relation, cols []*Column) []*Column {
	var out []*Column
	for _, c := range cols {
		if a.auditor == 0 || a.canSelect(a.auditor, rel, c.Attnum) {
			out = append(out, c)
		}
	}
	return out
}

// sample reads and classifies every sampleable relation, each with ONLY.
func (a *analysis) sample(rows int) {
	var rels []*Relation
	for _, rel := range a.s.Relations {
		if len(sampleable(rel)) > 0 {
			rels = append(rels, rel)
		}
	}
	sort.Slice(rels, func(i, j int) bool { return rels[i].OID < rels[j].OID })
	failed := map[uint32]string{} // relation -> why it wasn't sampled
	for _, rel := range rels {
		cols := a.auditorColumns(rel, sampleable(rel))
		if len(cols) == 0 {
			failed[rel.OID] = "the auditor's login may not read it"
			continue
		}
		if len(cols) < len(sampleable(rel)) {
			a.s.NotSeen = append(a.s.NotSeen, NotSeen{What: "some values of " + a.s.QualifiedName(rel),
				Why: "not sampled: the auditor's login may read only some of its columns"})
		}
		stats, err := sampleRelation(a.ctx, a.c, a.s, rel, cols, rows, true)
		if err != nil {
			if a.ctx.Err() != nil {
				return
			}
			failed[rel.OID] = sampleError(err)
			continue
		}
		a.sampled[rel.OID] = true
		for _, col := range rel.Columns {
			if p := classify(rel.Name, col, stats[col.Attnum]); p != nil {
				a.personal[colRef{rel.OID, col.Attnum}] = p
			}
		}
	}
	a.sampleThroughParents(rows, failed)
	var ids []uint32
	for id := range failed {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		a.s.NotSeen = append(a.s.NotSeen, NotSeen{What: "values of " + a.s.QualifiedName(a.s.Relations[id]),
			Why: "not sampled: " + failed[id]})
	}
}

// sampleThroughParents covers partitions and children the auditor may not
// read on their own but may read through their top parent: the parent is
// read once, with its whole tree, as long as no foreign table hangs below
// it (reading one would connect to another server or run a program).
func (a *analysis) sampleThroughParents(rows int, failed map[uint32]string) {
	var tops []*Relation
	for _, rel := range a.s.Relations {
		if len(a.s.Inherits[rel.OID]) == 0 && len(a.children()[rel.OID]) > 0 {
			tops = append(tops, rel)
		}
	}
	sort.Slice(tops, func(i, j int) bool { return tops[i].OID < tops[j].OID })
	for _, top := range tops {
		desc := a.descendants(top)
		var missing []*Relation
		foreign := false
		for _, d := range desc {
			if d.Kind == 'f' {
				foreign = true
			}
			if _, ok := failed[d.OID]; ok {
				missing = append(missing, d)
			}
		}
		if len(missing) == 0 || foreign {
			continue
		}
		cols := readableColumns(top)
		if len(a.auditorColumns(top, cols)) < len(cols) || len(cols) == 0 {
			continue
		}
		stats, err := sampleRelation(a.ctx, a.c, a.s, top, cols, rows, false)
		if err != nil {
			continue
		}
		for _, d := range missing {
			delete(failed, d.OID)
			a.throughParent[d.OID] = top.OID
			for _, dc := range d.Columns {
				for _, tc := range top.Columns {
					if tc.Name != dc.Name {
						continue
					}
					if p := classify(d.Name, dc, stats[tc.Attnum]); p != nil {
						q := *p
						q.Evidence = append([]string{"read through " + a.s.QualifiedName(top)}, p.Evidence...)
						a.personal[colRef{d.OID, dc.Attnum}] = &q
					}
				}
			}
		}
	}
}

// inheritPersonal gives partitioned tables and inheritance parents what
// their sampled partitions and children hold, column by column (by name),
// since reading the parent reads their rows too.
func (a *analysis) inheritPersonal() {
	var parents []*Relation
	for _, rel := range a.s.Relations {
		if len(a.children()[rel.OID]) > 0 {
			parents = append(parents, rel)
		}
	}
	sort.Slice(parents, func(i, j int) bool { return parents[i].OID < parents[j].OID })
	for _, parent := range parents {
		desc := a.descendants(parent)
		for _, col := range parent.Columns {
			var from []string
			var merged *Personal
			for _, d := range desc {
				if !a.sampled[d.OID] && a.throughParent[d.OID] == 0 {
					continue
				}
				dc := d.columnNamed(col.Name)
				if dc == nil {
					continue
				}
				p := a.personal[colRef{d.OID, dc.Attnum}]
				if p == nil {
					continue
				}
				q := *p
				q.Evidence = nil
				merged = mergePersonal(merged, &q)
				from = append(from, a.s.QualifiedName(d))
			}
			if merged == nil {
				continue
			}
			sort.Strings(from)
			list := from
			if len(list) > 3 {
				list = append(list[:3:3], fmt.Sprintf("%d more", len(from)-3))
			}
			what := "child table"
			if parent.Kind == 'p' {
				what = "partition"
			}
			if len(from) > 1 {
				what += "s"
			}
			merged.Evidence = []string{"found in " + what + " " + andList(list)}
			key := colRef{parent.OID, col.Attnum}
			a.personal[key] = mergePersonal(a.personal[key], merged)
		}
	}
}

// columnNamed returns the column with the given name, or nil.
func (r *Relation) columnNamed(name string) *Column {
	for _, c := range r.Columns {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// classifyRest names the columns sampling didn't cover: by name, from the
// partitions and children below a parent, or from the base columns a view
// reads.
func (a *analysis) classifyRest() {
	var rels []*Relation
	for _, rel := range a.s.Relations {
		rels = append(rels, rel)
	}
	sort.Slice(rels, func(i, j int) bool { return rels[i].OID < rels[j].OID })
	for _, rel := range rels {
		if a.sampled[rel.OID] {
			continue
		}
		for _, col := range rel.Columns {
			key := colRef{rel.OID, col.Attnum}
			if _, done := a.personal[key]; done {
				continue
			}
			if rel.Kind == 'v' {
				continue // views take their sources' verdicts below
			}
			if p := classify(rel.Name, col, nil); p != nil {
				a.personal[key] = p
			}
		}
	}
	a.inheritPersonal()
	// Views (and materialized views, which keep their sources' data):
	// combine the verdicts of the base columns they read.
	for _, rel := range rels {
		if rel.Lineage == nil {
			continue
		}
		for _, col := range rel.Columns {
			lin, ok := rel.Lineage[col.Attnum]
			if !ok {
				continue
			}
			p := a.lineagePersonal(rel, lin, 0)
			if p == nil {
				if rel.Kind == 'v' {
					if q := classify(rel.Name, col, nil); q != nil {
						a.personal[colRef{rel.OID, col.Attnum}] = q
					}
				}
				continue
			}
			key := colRef{rel.OID, col.Attnum}
			a.personal[key] = mergePersonal(a.personal[key], p)
		}
	}
}

func (a *analysis) lineagePersonal(v *Relation, lin colLineage, depth int) *Personal {
	if depth > 30 {
		return nil
	}
	var out *Personal
	for _, src := range lin.Sources {
		srel := a.s.Relations[src.Rel]
		if srel == nil {
			continue
		}
		cols := []*Column{srel.Column(src.Attnum)}
		if src.Attnum == 0 {
			cols = srel.Columns
		}
		for _, c := range cols {
			if c == nil {
				continue
			}
			var p *Personal
			if srel.Lineage != nil && srel.Kind == 'v' {
				if l2, ok := srel.Lineage[c.Attnum]; ok {
					p = a.lineagePersonal(srel, l2, depth+1)
				}
			} else {
				p = a.personal[colRef{srel.OID, c.Attnum}]
			}
			if p == nil {
				continue
			}
			q := *p
			q.Evidence = []string{"comes from " + a.s.QualifiedName(srel) + "." + quoteIdent(c.Name)}
			q.Derived = p.Derived || !lin.Direct
			out = mergePersonal(out, &q)
		}
	}
	return out
}

func mergePersonal(a, b *Personal) *Personal {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	cats := map[string]bool{}
	for _, c := range append(append([]string{}, a.Categories...), b.Categories...) {
		cats[c] = true
	}
	out := &Personal{Confidence: "medium", Derived: a.Derived && b.Derived}
	for c := range cats {
		out.Categories = append(out.Categories, c)
	}
	sort.Strings(out.Categories)
	if a.Confidence == "high" || b.Confidence == "high" {
		out.Confidence = "high"
	}
	ev := map[string]bool{}
	for _, e := range append(append([]string{}, a.Evidence...), b.Evidence...) {
		if !ev[e] {
			ev[e] = true
			out.Evidence = append(out.Evidence, e)
		}
	}
	return out
}

// isBase reports whether a relation holds its own data (reach to it is
// reach to data, not to a window onto other tables).
func isBase(rel *Relation) bool { return rel.Kind != 'v' }
