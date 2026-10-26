package audit

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/softdevstudio/baresql/internal/pgwire"
)

// The server is the judge of who can do what: has_table_privilege and
// has_column_privilege answer for every subject, relation and column, and
// BareSQL's own reading of the catalogs (reach.go) must agree with them.
// The catalog reading is what explains each answer.

// truthSet holds the server's answers for one subject and relation:
// privilege -> nil for the whole table, else the columns.
type truthSet map[string][]int

type truthKey struct {
	subject, rel uint32
}

// serverTruth asks the server about every subject and audited relation.
func serverTruth(ctx context.Context, c *pgwire.Conn, s *Snapshot, subjects []uint32) (map[truthKey]truthSet, error) {
	var rels []string
	for id := range s.Relations {
		rels = append(rels, strconv.FormatUint(uint64(id), 10))
	}
	sort.Strings(rels)
	out := map[truthKey]truthSet{}
	if len(rels) == 0 || len(subjects) == 0 {
		return out, nil
	}
	cols := func(priv string) string {
		return fmt.Sprintf(`CASE WHEN pg_catalog.has_table_privilege(s.s, c.oid, '%[1]s') THEN NULL
			WHEN NOT pg_catalog.has_any_column_privilege(s.s, c.oid, '%[1]s') THEN '{}'
			ELSE ARRAY(SELECT a.attnum FROM pg_catalog.pg_attribute a WHERE a.attrelid = c.oid AND a.attnum > 0
				AND NOT a.attisdropped AND pg_catalog.has_column_privilege(s.s, c.oid, a.attnum, '%[1]s')
				ORDER BY a.attnum)::text END`, priv)
	}
	sql := `SELECT s.s, c.oid, ` + cols("SELECT") + `, ` + cols("INSERT") + `, ` + cols("UPDATE") + `,
		pg_catalog.has_table_privilege(s.s, c.oid, 'DELETE'), pg_catalog.has_table_privilege(s.s, c.oid, 'TRUNCATE')
		FROM pg_catalog.unnest($1::oid[]) s(s) CROSS JOIN pg_catalog.pg_class c
		WHERE c.oid = ANY($2::oid[]) AND pg_catalog.has_schema_privilege(s.s, c.relnamespace, 'USAGE')`
	// Batches keep each answer a few megabytes at most.
	const batch = 25
	for i := 0; i < len(subjects); i += batch {
		end := min(i+batch, len(subjects))
		var ids []string
		for _, id := range subjects[i:end] {
			ids = append(ids, strconv.FormatUint(uint64(id), 10))
		}
		res, err := c.QueryArgs(ctx, sql, "{"+strings.Join(ids, ",")+"}", "{"+strings.Join(rels, ",")+"}")
		if err != nil {
			return nil, fmt.Errorf("audit: asking the server about privileges: %w", err)
		}
		for _, r := range res.Rows {
			ts := truthSet{}
			for j, priv := range []string{"SELECT", "INSERT", "UPDATE"} {
				v := r[2+j]
				if v.Null {
					ts[priv] = nil
				} else if attnums := parseIntArray(v.String()); len(attnums) > 0 {
					ts[priv] = attnums
				}
			}
			if boolv(r[5]) {
				ts["DELETE"] = nil
			}
			if boolv(r[6]) {
				ts["TRUNCATE"] = nil
			}
			if len(ts) > 0 {
				out[truthKey{u32(r[0]), u32(r[1])}] = ts
			}
		}
	}
	return out, nil
}

func parseIntArray(s string) []int {
	var out []int
	for _, p := range parseTextArray(s) {
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// reconcile makes the catalog reading agree with the server. Where the
// server allows something BareSQL can't explain, an "unexplained" route is
// added; where BareSQL sees a route the server denies, the route is
// dropped. Both are reported as mismatches, since either means BareSQL's
// model of PostgreSQL is incomplete.
func reconcile(s *Snapshot, subject uint32, eval map[uint32]map[string]*grantSet, truth map[truthKey]truthSet) []string {
	var mismatches []string
	note := func(rel *Relation, priv, what string) {
		mismatches = append(mismatches, fmt.Sprintf("%s %s on %s: %s", s.RoleName(subject), priv, s.QualifiedName(rel), what))
	}
	for _, rel := range s.Relations {
		ts := truth[truthKey{subject, rel.OID}]
		for _, priv := range tablePrivs {
			gs := eval[rel.OID][priv]
			want, has := ts[priv]
			switch {
			case !has && gs == nil:
				continue
			case !has:
				note(rel, priv, "BareSQL found a route the server denies")
				delete(eval[rel.OID], priv)
				continue
			}
			if gs == nil {
				gs = &grantSet{}
				if eval[rel.OID] == nil {
					eval[rel.OID] = map[string]*grantSet{}
				}
				eval[rel.OID][priv] = gs
			}
			unexplained := []Route{{Kind: "unexplained"}}
			if want == nil { // the whole table
				if !gs.All {
					note(rel, priv, "the server allows the whole table and BareSQL can't tell why")
					gs.All, gs.Table, gs.Columns = true, unexplained, nil
				}
				continue
			}
			if gs.All {
				note(rel, priv, "BareSQL found a table-level route the server denies")
				gs.All, gs.Table = false, nil
				gs.Columns = map[int][]Route{}
			}
			wantSet := map[int]bool{}
			for _, a := range want {
				wantSet[a] = true
				if len(gs.Columns[a]) == 0 {
					note(rel, priv, fmt.Sprintf("the server allows column %d and BareSQL can't tell why", a))
					if gs.Columns == nil {
						gs.Columns = map[int][]Route{}
					}
					gs.Columns[a] = unexplained
				}
			}
			for a := range gs.Columns {
				if !wantSet[a] {
					note(rel, priv, fmt.Sprintf("BareSQL found a route to column %d the server denies", a))
					delete(gs.Columns, a)
				}
			}
		}
	}
	sort.Strings(mismatches)
	return mismatches
}
