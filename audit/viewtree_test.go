package audit

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/softdevstudio/baresql/internal/pgwire"
	"github.com/softdevstudio/baresql/internal/testenv"
)

func TestTokenizeTree(t *testing.T) {
	got := tokenizeTree(`({QUERY :a 1 :b "x y" :c my\ name :d <> :e (i 1 2) :f 3 [ 0 1 ]})`)
	want := []string{"(", "{", "QUERY", ":a", "1", ":b", `"x`, `y"`, ":c", "my name", ":d", "<>", ":e", "(", "i", "1", "2", ")", ":f", "3", "[", "0", "1", "]", "}", ")"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestParseNodeTreeErrors(t *testing.T) {
	for _, s := range []string{"", "(", "{QUERY", "{QUERY :a", "{QUERY a 1}", ")", "({QUERY :x 1}) extra", strings.Repeat("(", 2000)} {
		if _, err := parseNodeTree(s); err == nil {
			t.Errorf("%q: no error", s[:min(len(s), 30)])
		}
	}
}

// A real PostgreSQL 16 tree: SELECT c.email, c.first_name || ' ' ||
// c.last_name AS full_name FROM lin.customers c JOIN lin.orders o ON ...
const pg16Tree = `({QUERY :commandType 1 :querySource 0 :canSetTag true :utilityStmt <> :resultRelation 0 :hasAggs false :cteList <> :rtable ({RANGETBLENTRY :alias {ALIAS :aliasname c :colnames <>} :eref {ALIAS :aliasname c :colnames ("id" "first_name" "last_name" "email" "card")} :rtekind 0 :relid 16420 :relkind r :rellockmode 1 :tablesample <> :perminfoindex 1 :lateral false :inh true :inFromCl true :securityQuals <>} {RANGETBLENTRY :alias {ALIAS :aliasname o :colnames <>} :eref {ALIAS :aliasname o :colnames ("id" "customer_id" "total")} :rtekind 0 :relid 16425 :relkind r :rellockmode 1 :tablesample <> :perminfoindex 2 :lateral false :inh true :inFromCl true :securityQuals <>} {RANGETBLENTRY :alias <> :eref {ALIAS :aliasname unnamed_join :colnames ("id" "first_name" "last_name" "email" "card" "id" "customer_id" "total")} :rtekind 2 :jointype 0 :joinmergedcols 0 :joinaliasvars ({VAR :varno 1 :varattno 1 :vartype 23 :vartypmod -1 :varcollid 0 :varnullingrels (b) :varlevelsup 0 :varnosyn 1 :varattnosyn 1 :location -1} {VAR :varno 1 :varattno 2 :vartype 25 :vartypmod -1 :varcollid 100 :varnullingrels (b) :varlevelsup 0 :varnosyn 1 :varattnosyn 2 :location -1}) :joinleftcols (i 1 2 3 4 5) :joinrightcols (i 1 2 3) :join_using_alias <> :lateral false :inh false :inFromCl true :securityQuals <>}) :jointree {FROMEXPR :fromlist ({RANGETBLREF :rtindex 1}) :quals <>} :targetList ({TARGETENTRY :expr {VAR :varno 1 :varattno 4 :vartype 25 :vartypmod -1 :varcollid 100 :varnullingrels (b) :varlevelsup 0 :varnosyn 1 :varattnosyn 4 :location 29} :resno 1 :resname email :ressortgroupref 0 :resorigtbl 16420 :resorigcol 4 :resjunk false} {TARGETENTRY :expr {OPEXPR :opno 654 :opfuncid 1258 :opresulttype 25 :opretset false :opcollid 100 :inputcollid 100 :args ({OPEXPR :opno 654 :opfuncid 1258 :opresulttype 25 :opretset false :opcollid 100 :inputcollid 100 :args ({VAR :varno 1 :varattno 2 :vartype 25 :vartypmod -1 :varcollid 100 :varnullingrels (b) :varlevelsup 0 :varnosyn 1 :varattnosyn 2 :location 38} {CONST :consttype 25 :consttypmod -1 :constcollid 100 :constlen -1 :constbyval false :constisnull false :location 54 :constvalue 5 [ 20 0 0 0 32 ]}) :location 51} {VAR :varno 1 :varattno 3 :vartype 25 :vartypmod -1 :varcollid 100 :varnullingrels (b) :varlevelsup 0 :varnosyn 1 :varattnosyn 3 :location 61}) :location 58} :resno 2 :resname full_name :ressortgroupref 0 :resorigtbl 0 :resorigcol 0 :resjunk false}) :setOperations <>})`

func TestViewLineageFromText(t *testing.T) {
	lin, err := viewLineage(pg16Tree)
	if err != nil {
		t.Fatal(err)
	}
	got := describeLineage(lin, func(rel uint32) string { return fmt.Sprint(rel) })
	want := "email=16420.4 direct; full_name=16420.2,16420.3"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func describeLineage(lin []colLineage, relName func(uint32) string) string {
	var parts []string
	for _, cl := range lin {
		var srcs []string
		for _, s := range cl.Sources {
			if s.Attnum == 0 {
				srcs = append(srcs, relName(s.Rel)+".*")
			} else {
				srcs = append(srcs, fmt.Sprintf("%s.%d", relName(s.Rel), s.Attnum))
			}
		}
		sort.Strings(srcs)
		p := cl.Name + "=" + strings.Join(srcs, ",")
		if cl.Direct {
			p += " direct"
		}
		if cl.Opaque {
			p += " opaque"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "; ")
}

// TestViewLineageOnServer creates views of many shapes on a real server and
// checks where each column's data comes from. CI runs it on every
// supported PostgreSQL version, whose stored trees differ in detail.
func TestViewLineageOnServer(t *testing.T) {
	cfg, err := pgwire.ParseDSN(testenv.DSN(t, testenv.PG))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := pgwire.Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	schema := "baresql_lineage_" + fmt.Sprint(c.ProcessID())
	setup := strings.ReplaceAll(`
		CREATE SCHEMA S;
		CREATE TABLE S.a (id int, x text, y text, z int);
		CREATE TABLE S.b (id int, a_id int, w text);
		CREATE VIEW S.v_plain AS SELECT id, x FROM S.a;
		CREATE VIEW S.v_expr AS SELECT x || y AS xy, upper(x) AS ux, z + 1 AS z1, 42 AS k FROM S.a;
		CREATE VIEW S.v_join AS SELECT a.x, b.w FROM S.a a JOIN S.b b ON b.a_id = a.id;
		CREATE VIEW S.v_using AS SELECT id, x, w FROM S.a JOIN S.b USING (id);
		CREATE VIEW S.v_full AS SELECT id FROM S.a FULL JOIN S.b USING (id);
		CREATE VIEW S.v_sub AS SELECT s.x FROM (SELECT x FROM S.a) s;
		CREATE VIEW S.v_cte AS WITH c AS (SELECT y FROM S.a) SELECT y FROM c;
		CREATE VIEW S.v_union AS SELECT x FROM S.a UNION SELECT w FROM S.b;
		CREATE VIEW S.v_agg AS SELECT y, count(*) AS n, max(x) AS mx FROM S.a GROUP BY y;
		CREATE VIEW S.v_whole AS SELECT a AS r FROM S.a a;
		CREATE VIEW S.v_json AS SELECT row_to_json(a) AS j FROM S.a a;
		CREATE VIEW S.v_nested AS SELECT x FROM S.v_plain;
		CREATE VIEW S.v_func AS SELECT f.n FROM S.a, generate_series(1, a.z) f(n);
		CREATE VIEW S.v_case AS SELECT CASE WHEN z > 0 THEN x ELSE y END AS c FROM S.a;
		CREATE VIEW S.v_sublink AS SELECT (SELECT max(w) FROM S.b WHERE b.a_id = a.id) AS mw FROM S.a a;
		CREATE VIEW S.v_values AS SELECT * FROM (VALUES (1, 'x')) v(n, s);
		CREATE VIEW S.v_lateral AS SELECT l.w FROM S.a, LATERAL (SELECT w FROM S.b WHERE b.a_id = a.id) l;
		CREATE VIEW S.v_cast AS SELECT x::varchar AS xv, z::bigint AS zb FROM S.a;
		CREATE VIEW S.v_recursive AS WITH RECURSIVE r(n, t) AS (SELECT 1, x FROM S.a UNION ALL SELECT n + 1, t FROM r WHERE n < 3) SELECT t FROM r;
		CREATE MATERIALIZED VIEW S.m_plain AS SELECT id, y FROM S.a WITH NO DATA;`, "S.", schema+".")
	setup = strings.ReplaceAll(setup, "SCHEMA S;", "SCHEMA "+schema+";")
	if err := c.Exec(ctx, setup); err != nil {
		t.Fatal(err)
	}
	defer c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")

	res, err := c.QueryArgs(ctx, `SELECT c.relname, r.ev_action FROM pg_rewrite r JOIN pg_class c ON c.oid = r.ev_class
		WHERE c.relnamespace = $1::regnamespace AND r.rulename = '_RETURN' ORDER BY 1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	names, err := c.QueryArgs(ctx, `SELECT oid, relname FROM pg_class WHERE relnamespace = $1::regnamespace`, schema)
	if err != nil {
		t.Fatal(err)
	}
	relName := map[uint32]string{}
	for _, r := range names.Rows {
		relName[u32(r[0])] = r[1].String()
	}
	got := map[string]string{}
	for _, r := range res.Rows {
		lin, err := viewLineage(r[1].String())
		if err != nil {
			t.Errorf("%s: %v", r[0].String(), err)
			continue
		}
		got[r[0].String()] = describeLineage(lin, func(rel uint32) string { return relName[rel] })
	}
	// Columns: a(id=1, x=2, y=3, z=4), b(id=1, a_id=2, w=3), v_plain(id=1, x=2).
	want := map[string]string{
		"v_plain":     "id=a.1 direct; x=a.2 direct",
		"v_expr":      "xy=a.2,a.3; ux=a.2; z1=a.4; k=",
		"v_join":      "x=a.2 direct; w=b.3 direct",
		"v_using":     "id=a.1 direct; x=a.2 direct; w=b.3 direct",
		"v_full":      "id=a.1,b.1",
		"v_sub":       "x=a.2 direct",
		"v_cte":       "y=a.3 direct",
		"v_union":     "x=a.2,b.3",
		"v_agg":       "y=a.3 direct; n=; mx=a.2",
		"v_whole":     "r=a.*",
		"v_json":      "j=a.*",
		"v_nested":    "x=v_plain.2 direct",
		"v_func":      "n=a.4 opaque",
		"v_case":      "c=a.2,a.3,a.4",
		"v_sublink":   "mw=b.3",
		"v_values":    "n=; s=",
		"v_lateral":   "w=b.3 direct",
		"v_cast":      "xv=a.2 direct; zb=a.4",
		"v_recursive": "t=a.2",
		"m_plain":     "id=a.1 direct; y=a.3 direct",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], w)
		}
	}
}

// TestViewNeedsOnServer checks, on the server's own node trees, which
// columns PostgreSQL checks when a view is read: those in the select list,
// WHERE, joins, sublinks and CTEs, a whole row for a row reference, and
// "any column" for count(*).
func TestViewNeedsOnServer(t *testing.T) {
	cfg, err := pgwire.ParseDSN(testenv.DSN(t, testenv.PG))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c, err := pgwire.Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	schema := "baresql_needs_" + fmt.Sprint(c.ProcessID())
	setup := strings.ReplaceAll(`
		CREATE SCHEMA S;
		CREATE TABLE S.a (id int, x text, y text, z int);
		CREATE TABLE S.b (id int, a_id int, w text);
		CREATE VIEW S.v_plain AS SELECT id, x FROM S.a;
		CREATE VIEW S.v_where AS SELECT x FROM S.a WHERE z > 0;
		CREATE VIEW S.v_join AS SELECT a.x, b.w FROM S.a a JOIN S.b b ON b.a_id = a.id;
		CREATE VIEW S.v_sublink AS SELECT (SELECT max(w) FROM S.b WHERE b.a_id = a.id) AS mw FROM S.a a;
		CREATE VIEW S.v_cte AS WITH c AS (SELECT y FROM S.a) SELECT y FROM c;
		CREATE VIEW S.v_whole AS SELECT a AS r FROM S.a a;
		CREATE VIEW S.v_nested AS SELECT x FROM S.v_plain;
		CREATE VIEW S.v_count AS SELECT count(*) AS n FROM S.a;`, "S.", schema+".")
	setup = strings.ReplaceAll(setup, "SCHEMA S;", "SCHEMA "+schema+";")
	if err := c.Exec(ctx, setup); err != nil {
		t.Fatal(err)
	}
	defer c.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	res, err := c.QueryArgs(ctx, `SELECT c.relname, r.ev_action FROM pg_rewrite r JOIN pg_class c ON c.oid = r.ev_class
		WHERE c.relnamespace = $1::regnamespace AND r.rulename = '_RETURN' ORDER BY 1`, schema)
	if err != nil {
		t.Fatal(err)
	}
	names, err := c.QueryArgs(ctx, `SELECT oid, relname FROM pg_class WHERE relnamespace = $1::regnamespace`, schema)
	if err != nil {
		t.Fatal(err)
	}
	relName := map[uint32]string{}
	for _, r := range names.Rows {
		relName[u32(r[0])] = r[1].String()
	}
	got := map[string]string{}
	for _, r := range res.Rows {
		needs, err := viewNeeds(r[1].String())
		if err != nil {
			t.Errorf("%s: %v", r[0].String(), err)
			continue
		}
		var parts []string
		for _, n := range needs {
			switch n.Attnum {
			case anyColumn:
				parts = append(parts, relName[n.Rel]+".any")
			default:
				parts = append(parts, fmt.Sprintf("%s.%d", relName[n.Rel], n.Attnum))
			}
		}
		sort.Strings(parts)
		got[r[0].String()] = strings.Join(parts, " ")
	}
	want := map[string]string{
		"v_plain":   "a.1 a.2",
		"v_where":   "a.2 a.4",
		"v_join":    "a.1 a.2 b.2 b.3",
		"v_sublink": "a.1 b.2 b.3",
		"v_cte":     "a.3",
		"v_whole":   "a.0",
		"v_nested":  "v_plain.2",
		"v_count":   "a.any",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], w)
		}
	}
}
