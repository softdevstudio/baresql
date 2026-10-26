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

// rowsFor says which rows of rel a role sees, when rel has row security.
func (a *analysis) rowsFor(role uint32, rel *Relation) string {
	if !rel.RLS {
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
		if p.Rel != rel.OID || (p.Cmd != "ALL" && p.Cmd != "SELECT") {
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
			permissive = append(permissive, p.Name)
		} else {
			restrictive = append(restrictive, p.Name)
		}
	}
	if len(permissive) == 0 {
		return "no rows (row security on, no policy lets this role read)"
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

// expose follows a view column down to the base columns it reads. reader
// is the role reading the view; each hop reads with the view owner's
// rights, or the reader's for security_invoker views. A hop the rights
// don't cover would fail at run time, so it exposes nothing.
func (a *analysis) expose(reader uint32, v *Relation, attnum int, hops []ViewHop, depth int, out *[]exposure) {
	if depth > 30 || v.Lineage == nil {
		return
	}
	lin, ok := v.Lineage[attnum]
	if !ok {
		return
	}
	rights, who := v.Owner, a.s.RoleName(v.Owner)
	if v.SecurityInvoker {
		rights, who = reader, "the reader"
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
			if !a.canSelect(rights, srel, an) {
				continue
			}
			if srel.Kind == 'v' {
				a.expose(rights, srel, an, next, depth+1, out)
				continue
			}
			*out = append(*out, exposure{rel: srel, attnum: an, hops: next, rights: rights})
		}
	}
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
		return []Route{{Kind: "superuser", Role: r.s.RoleName(r.role)}}
	}
	var out []Route
	if v, ok := r.via(f.Owner); ok && f.Owner != 0 {
		out = append(out, Route{Kind: "owner", Role: r.s.RoleName(f.Owner), Via: v})
	}
	seen := map[uint32]bool{}
	for _, a := range f.ACL {
		if a.Priv != "EXECUTE" || a.Grantee == f.Owner || seen[a.Grantee] {
			continue
		}
		if v, ok := r.via(a.Grantee); ok {
			seen[a.Grantee] = true
			out = append(out, Route{Kind: "grant", Role: r.s.RoleName(a.Grantee), Via: v, Grantable: a.Grantable})
		}
	}
	return out
}

// sample reads and classifies every sampleable relation.
func (a *analysis) sample(rows int) {
	var rels []*Relation
	for _, rel := range a.s.Relations {
		if len(sampleable(rel)) > 0 {
			rels = append(rels, rel)
		}
	}
	sort.Slice(rels, func(i, j int) bool { return rels[i].OID < rels[j].OID })
	for _, rel := range rels {
		var cols []*Column
		for _, c := range sampleable(rel) {
			if a.auditor == 0 || a.canSelect(a.auditor, rel, c.Attnum) {
				cols = append(cols, c)
			}
		}
		if len(cols) == 0 {
			a.s.NotSeen = append(a.s.NotSeen, NotSeen{What: "values of " + a.s.QualifiedName(rel),
				Why: "not sampled: the auditor's login may not read it"})
			continue
		}
		if len(cols) < len(sampleable(rel)) {
			a.s.NotSeen = append(a.s.NotSeen, NotSeen{What: "some values of " + a.s.QualifiedName(rel),
				Why: "not sampled: the auditor's login may read only some of its columns"})
		}
		stats, err := sampleRelation(a.ctx, a.c, a.s, rel, cols, rows)
		if err != nil {
			if a.ctx.Err() != nil {
				return
			}
			a.s.NotSeen = append(a.s.NotSeen, NotSeen{What: "values of " + a.s.QualifiedName(rel),
				Why: "not sampled: " + sampleError(err)})
			continue
		}
		a.sampled[rel.OID] = true
		for _, col := range rel.Columns {
			if p := classify(rel.Name, col, stats[col.Attnum]); p != nil {
				a.personal[colRef{rel.OID, col.Attnum}] = p
			}
		}
	}
}

// classifyRest names the columns sampling didn't cover: by name, from the
// partition root, or from the base columns a view reads.
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
		root := a.s.Relations[rel.PartitionRoot]
		for _, col := range rel.Columns {
			key := colRef{rel.OID, col.Attnum}
			if _, done := a.personal[key]; done {
				continue
			}
			if root != nil && a.sampled[root.OID] {
				for _, rc := range root.Columns {
					if rc.Name == col.Name {
						if p := a.personal[colRef{root.OID, rc.Attnum}]; p != nil {
							a.personal[key] = p
						}
					}
				}
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
