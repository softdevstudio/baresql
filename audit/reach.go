package audit

import (
	"sort"
	"strings"
)

// Route is one way a role gets a privilege.
type Route struct {
	// Kind: superuser, predefined (pg_read_all_data, pg_write_all_data),
	// owner, grant (table-level), column-grant, view (data read through a
	// view with its owner's rights), unexplained (the server says yes and
	// BareSQL can't tell why; a bug to report).
	Kind string `json:"kind"`
	// Role holds the grant or ownership (PUBLIC for grants to everyone).
	Role string `json:"role,omitempty"`
	// Via lists the roles in between when Role is inherited through a chain
	// of memberships: login > Via... > Role.
	Via []string `json:"via,omitempty"`
	// SetRole is set when the login has to SET ROLE first.
	SetRole string `json:"set_role,omitempty"`
	// Grantable: the grant came WITH GRANT OPTION.
	Grantable bool `json:"grantable,omitempty"`
	// View routes: the views the data passes through, outermost first,
	// each read with its owner's rights.
	Views []ViewHop `json:"views,omitempty"`
	// Rows says which rows row security lets through, for tables that
	// have it on: "all rows (...)", "rows allowed by policy ...", "no rows
	// (...)"; or, for a view that can't be read, "reading it fails: ...".
	Rows string `json:"rows,omitempty"`
}

// failsPrefix starts the Rows of a route to a view whose query PostgreSQL
// refuses (see viewFailure).
const failsPrefix = "reading it fails: "

// GivesRows reports whether the route hands out data today: row security
// doesn't filter it down to no rows, and it isn't a view that fails.
func (r Route) GivesRows() bool {
	return !strings.HasPrefix(r.Rows, "no rows") && !strings.HasPrefix(r.Rows, failsPrefix)
}

// givesRows reports whether any of the routes hands out data.
func givesRows(routes []Route) bool {
	for _, r := range routes {
		if r.GivesRows() {
			return true
		}
	}
	return false
}

type ViewHop struct {
	View  string `json:"view"`
	Owner string `json:"owner"`
}

// String is the route in the words the text report uses.
func (r Route) String() string {
	var b strings.Builder
	if r.SetRole != "" {
		b.WriteString("after SET ROLE " + r.SetRole + ": ")
	}
	switch r.Kind {
	case "superuser":
		b.WriteString("superuser")
	case "predefined":
		b.WriteString("member of " + r.Role)
	case "owner":
		b.WriteString("owner " + r.Role)
	case "grant":
		b.WriteString("granted to " + r.Role)
	case "column-grant":
		b.WriteString("column granted to " + r.Role)
	case "view":
		for i, h := range r.Views {
			if i > 0 {
				b.WriteString(", then ")
			}
			b.WriteString("through view " + h.View + " (rights of " + h.Owner + ")")
		}
	case "function":
		b.WriteString("may run " + r.Role)
	case "unexplained":
		b.WriteString("unexplained")
	}
	if len(r.Via) > 0 && r.Kind != "view" {
		b.WriteString(" through " + strings.Join(r.Via, " > "))
	}
	if r.Grantable {
		b.WriteString(" with grant option")
	}
	if r.Rows != "" {
		b.WriteString("; " + r.Rows)
	}
	return b.String()
}

// rights is what one role can do with the privileges it holds directly
// and inherits (no SET ROLE).
type rights struct {
	s        *Snapshot
	role     uint32
	closure  map[uint32]rolePath
	super    bool
	readAll  rolePath // path to pg_read_all_data, nil when not a member
	writeAll rolePath
}

func (s *Snapshot) rightsOf(role uint32) *rights {
	r := &rights{s: s, role: role, closure: s.inherited(role), super: s.isSuper(role)}
	if id := s.predefined("pg_read_all_data"); id != 0 {
		r.readAll = r.closure[id]
	}
	if id := s.predefined("pg_write_all_data"); id != 0 {
		r.writeAll = r.closure[id]
	}
	return r
}

// via returns the roles passed through on the way from the subject to a
// role it inherits (both ends left out), and whether it inherits it at all.
// PUBLIC is inherited by everyone, with nothing in between.
func (r *rights) via(role uint32) ([]string, bool) {
	if role == 0 {
		return nil, true
	}
	p, ok := r.closure[role]
	if !ok {
		return nil, false
	}
	return r.s.between(p), true
}

// usage reports whether the role may look into a schema.
func (r *rights) usage(schema uint32) bool {
	if r.super || r.readAll != nil || r.writeAll != nil {
		return true
	}
	sc := r.s.Schemas[schema]
	if sc == nil {
		return false
	}
	if _, ok := r.closure[sc.Owner]; ok {
		return true
	}
	for _, a := range sc.ACL {
		if a.Priv == "USAGE" {
			if _, ok := r.via(a.Grantee); ok {
				return true
			}
		}
	}
	return false
}

// privRoutes lists the routes giving priv on a relation column (attnum 0:
// the table-level privilege only).
func (r *rights) privRoutes(rel *Relation, attnum int, priv string) []Route {
	if !r.usage(rel.Schema) {
		return nil
	}
	if r.super {
		return []Route{{Kind: "superuser", Role: r.s.RoleName(r.role)}}
	}
	var out []Route
	all := r.readAll
	if priv != "SELECT" {
		all = r.writeAll
		if priv == "TRUNCATE" || priv == "REFERENCES" || priv == "TRIGGER" {
			all = nil // pg_write_all_data covers INSERT, UPDATE, DELETE
		}
	}
	if all != nil {
		out = append(out, Route{Kind: "predefined", Role: r.s.RoleName(all[len(all)-1]), Via: r.s.between(all)})
	}
	if v, ok := r.via(rel.Owner); ok && rel.Owner != 0 {
		out = append(out, Route{Kind: "owner", Role: r.s.RoleName(rel.Owner), Via: v})
	}
	seen := map[string]bool{}
	add := func(kind string, acl []ACLItem) {
		for _, a := range acl {
			if a.Priv != priv {
				continue
			}
			// The owner's own entry is covered by the owner route.
			if a.Grantee == rel.Owner {
				continue
			}
			v, ok := r.via(a.Grantee)
			if !ok {
				continue
			}
			key := kind + "/" + r.s.RoleName(a.Grantee)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Route{Kind: kind, Role: r.s.RoleName(a.Grantee), Via: v, Grantable: a.Grantable})
		}
	}
	add("grant", rel.ACL)
	if attnum > 0 {
		if col := rel.Column(attnum); col != nil {
			add("column-grant", col.ACL)
		}
	}
	return out
}

// Privileges BareSQL reports per relation.
var tablePrivs = []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE"}

// columnPriv says whether a privilege can also be granted per column.
func columnPriv(p string) bool { return p == "SELECT" || p == "INSERT" || p == "UPDATE" }

// grant is the Go side of one (role, relation, privilege): which columns
// it covers and why. Columns nil with All set means the whole table.
type grantSet struct {
	All     bool
	Columns map[int][]Route // attnum -> routes (for column-level privileges)
	Table   []Route         // routes of the table-level privilege
}

// evaluate works out every privilege of a role on every audited relation.
func (r *rights) evaluate() map[uint32]map[string]*grantSet {
	out := map[uint32]map[string]*grantSet{}
	for _, rel := range r.s.Relations {
		for _, priv := range tablePrivs {
			gs := &grantSet{Table: r.privRoutes(rel, 0, priv)}
			gs.All = len(gs.Table) > 0
			if columnPriv(priv) && !gs.All {
				for _, col := range rel.Columns {
					if routes := r.privRoutes(rel, col.Attnum, priv); len(routes) > 0 {
						if gs.Columns == nil {
							gs.Columns = map[int][]Route{}
						}
						gs.Columns[col.Attnum] = routes
					}
				}
			}
			if gs.All || len(gs.Columns) > 0 {
				if out[rel.OID] == nil {
					out[rel.OID] = map[string]*grantSet{}
				}
				out[rel.OID][priv] = gs
			}
		}
	}
	return out
}

// colRoutes returns the routes for one column under a grant set.
func (g *grantSet) colRoutes(attnum int) []Route {
	if g == nil {
		return nil
	}
	if g.All {
		return g.Table
	}
	return g.Columns[attnum]
}

func sortRoutes(rs []Route) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].String() < rs[j].String() })
}
