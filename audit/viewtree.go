package audit

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// A view's query is stored in pg_rewrite.ev_action as a node tree in
// PostgreSQL's text format:
//
//	({QUERY :commandType 1 ... :rtable ({RANGETBLENTRY ...}) :targetList (...)})
//
// Reading it lets BareSQL tell which base column feeds each view column,
// without asking the server to plan or run the view (planning can evaluate
// user functions).

// tnode is a parsed value: an atom, a list, or a node with named fields.
type tnode struct {
	atom   string
	isNull bool // <>
	list   []*tnode
	isList bool
	typ    string // node type, e.g. QUERY, VAR; empty for atoms and lists
	fields map[string]*tnode
}

func (n *tnode) field(name string) *tnode {
	if n == nil || n.fields == nil {
		return nil
	}
	return n.fields[name]
}

func (n *tnode) int(name string) int {
	f := n.field(name)
	if f == nil {
		return 0
	}
	v, _ := strconv.Atoi(f.atom)
	return v
}

func (n *tnode) str(name string) string {
	f := n.field(name)
	if f == nil {
		return ""
	}
	return f.atom
}

// maxTreeDepth bounds nesting; real view trees are far shallower.
const maxTreeDepth = 1000

type treeParser struct {
	toks []string
	pos  int
}

// parseNodeTree parses the text form of a pg_node_tree.
func parseNodeTree(s string) (*tnode, error) {
	p := &treeParser{toks: tokenizeTree(s)}
	n, err := p.value(0)
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("node tree: unexpected %q after the end", p.toks[p.pos])
	}
	return n, nil
}

// tokenizeTree splits like PostgreSQL's pg_strtok: ( ) { } are tokens of
// their own, anything else runs to whitespace or one of them, and a
// backslash makes the next character ordinary.
func tokenizeTree(s string) []string {
	var toks []string
	var cur strings.Builder
	inTok := false
	flush := func() {
		if inTok {
			toks = append(toks, cur.String())
			cur.Reset()
			inTok = false
		}
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\\' && i+1 < len(s):
			cur.WriteByte(s[i+1])
			inTok = true
			i++
		case ch == ' ' || ch == '\n' || ch == '\t' || ch == '\r':
			flush()
		case ch == '(' || ch == ')' || ch == '{' || ch == '}':
			flush()
			toks = append(toks, string(ch))
		default:
			cur.WriteByte(ch)
			inTok = true
		}
	}
	flush()
	return toks
}

var errTreeEnd = errors.New("node tree: unexpected end")

func (p *treeParser) next() (string, error) {
	if p.pos >= len(p.toks) {
		return "", errTreeEnd
	}
	t := p.toks[p.pos]
	p.pos++
	return t, nil
}

func (p *treeParser) peek() string {
	if p.pos >= len(p.toks) {
		return ""
	}
	return p.toks[p.pos]
}

func (p *treeParser) value(depth int) (*tnode, error) {
	if depth > maxTreeDepth {
		return nil, errors.New("node tree: nested too deeply")
	}
	t, err := p.next()
	if err != nil {
		return nil, err
	}
	switch t {
	case "{":
		typ, err := p.next()
		if err != nil {
			return nil, err
		}
		n := &tnode{typ: typ, fields: map[string]*tnode{}}
		for {
			f := p.peek()
			if f == "}" {
				p.pos++
				return n, nil
			}
			if !strings.HasPrefix(f, ":") {
				return nil, fmt.Errorf("node tree: expected a field in %s, got %q", typ, f)
			}
			p.pos++
			if p.peek() == "}" { // a field with no value (not produced today)
				n.fields[f[1:]] = &tnode{isNull: true}
				continue
			}
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			// A Datum prints as "<length> [ b b b ... ]"; skip the bytes.
			if p.peek() == "[" {
				for {
					x, err := p.next()
					if err != nil {
						return nil, err
					}
					if x == "]" {
						break
					}
				}
			}
			n.fields[f[1:]] = v
		}
	case "(":
		n := &tnode{isList: true}
		for p.peek() != ")" {
			if p.pos >= len(p.toks) {
				return nil, errTreeEnd
			}
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			n.list = append(n.list, v)
		}
		p.pos++
		return n, nil
	case ")", "}":
		return nil, fmt.Errorf("node tree: unexpected %q", t)
	case "<>":
		return &tnode{isNull: true}, nil
	}
	return &tnode{atom: unquoteTreeString(t)}, nil
}

// unquoteTreeString strips the double quotes of a String node ("email").
func unquoteTreeString(t string) string {
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
		return t[1 : len(t)-1]
	}
	return t
}

// RTEKind values (parsenodes.h); stable from PostgreSQL 13 to 18.
const (
	rteRelation  = 0
	rteSubquery  = 1
	rteJoin      = 2
	rteFunction  = 3
	rteTableFunc = 4
	rteValues    = 5
	rteCTE       = 6
	rteGroup     = 9 // PostgreSQL 18
)

// colRef is a column of a table, view or materialized view; attnum 0
// means the whole row (every column).
type colRef struct {
	Rel    uint32
	Attnum int
}

// colLineage is where one view column's data comes from.
type colLineage struct {
	Resno   int
	Name    string
	Sources []colRef
	// Direct is true when the column is a plain copy of one source column;
	// false when an expression transforms or combines its sources.
	Direct bool
	// Opaque is set when part of the value comes from a function's output
	// or something else BareSQL can't trace.
	Opaque bool
}

// viewLineage traces each output column of a view's stored query.
func viewLineage(evAction string) ([]colLineage, error) {
	root, err := parseNodeTree(evAction)
	if err != nil {
		return nil, err
	}
	q := root
	if root.isList {
		if len(root.list) != 1 {
			return nil, fmt.Errorf("node tree: expected one query, got %d", len(root.list))
		}
		q = root.list[0]
	}
	if q.typ != "QUERY" {
		return nil, fmt.Errorf("node tree: expected QUERY, got %q", q.typ)
	}
	tl := q.field("targetList")
	if tl == nil || !tl.isList {
		return nil, errors.New("node tree: query has no target list")
	}
	var out []colLineage
	for _, te := range tl.list {
		if te.typ != "TARGETENTRY" || te.str("resjunk") == "true" {
			continue
		}
		t := &tracer{seen: map[string]bool{}}
		stack := []*tnode{q}
		resno := te.int("resno")
		t.column(q, stack, resno, 0)
		cl := colLineage{Resno: resno, Name: te.str("resname"), Opaque: t.opaque}
		cl.Sources = dedupRefs(t.refs)
		cl.Direct = !t.opaque && !isSetOp(q) && t.plain(te.field("expr"), stack, 0) &&
			len(cl.Sources) == 1 && cl.Sources[0].Attnum != 0
		out = append(out, cl)
	}
	return out, nil
}

type tracer struct {
	refs   []colRef
	opaque bool
	seen   map[string]bool
	steps  int
}

// maxTraceSteps stops pathological trees (a recursive CTE referring to
// itself, say) from running long.
const maxTraceSteps = 100000

// expr collects the base columns an expression reads. stack holds the
// enclosing queries, innermost last: a Var with varlevelsup k belongs to
// stack[len-1-k].
func (t *tracer) expr(n *tnode, stack []*tnode, depth int) {
	if n == nil || n.isNull || depth > maxTreeDepth {
		return
	}
	t.steps++
	if t.steps > maxTraceSteps {
		t.opaque = true
		return
	}
	if n.isList {
		for _, e := range n.list {
			t.expr(e, stack, depth+1)
		}
		return
	}
	switch n.typ {
	case "VAR":
		t.variable(n, stack, depth)
		return
	case "SUBLINK":
		// The value of (SELECT x FROM ...) is what the subquery outputs;
		// its WHERE clause filters rows but doesn't expose them.
		t.expr(n.field("testexpr"), stack, depth+1)
		if sub := n.field("subselect"); sub != nil && sub.typ == "QUERY" {
			t.queryOutputs(sub, append(stack[:len(stack):len(stack)], sub), depth+1)
		}
		return
	case "QUERY":
		t.queryOutputs(n, append(stack[:len(stack):len(stack)], n), depth+1)
		return
	}
	// Every other node (operators, function calls, casts, CASE ...) is
	// walked for the Vars inside it.
	for _, f := range n.fields {
		t.expr(f, stack, depth+1)
	}
}

// queryOutputs collects what a (sub)query outputs: every output column.
func (t *tracer) queryOutputs(q *tnode, stack []*tnode, depth int) {
	t.column(q, stack, 0, depth)
}

func (t *tracer) variable(v *tnode, stack []*tnode, depth int) {
	up := v.int("varlevelsup")
	if up < 0 || up >= len(stack) {
		t.opaque = true
		return
	}
	q := stack[len(stack)-1-up]
	outer := stack[:len(stack)-up]
	rte := rangeEntry(q, v.int("varno"))
	if rte == nil {
		t.opaque = true
		return
	}
	attno := v.int("varattno")
	key := fmt.Sprintf("%p/%d/%d", rte, v.int("varno"), attno)
	if t.seen[key] {
		return
	}
	t.seen[key] = true
	switch rte.int("rtekind") {
	case rteRelation:
		rel, err := strconv.ParseUint(rte.str("relid"), 10, 32)
		if err != nil {
			t.opaque = true
			return
		}
		if attno < 0 { // system columns (ctid, xmin ...) hold no user data
			return
		}
		t.refs = append(t.refs, colRef{Rel: uint32(rel), Attnum: attno})
	case rteSubquery:
		sub := rte.field("subquery")
		if sub == nil || sub.typ != "QUERY" {
			t.opaque = true
			return
		}
		t.column(sub, append(outer[:len(outer):len(outer)], sub), attno, depth+1)
	case rteJoin:
		vars := rte.field("joinaliasvars")
		if vars == nil || !vars.isList {
			t.opaque = true
			return
		}
		if attno == 0 {
			t.expr(vars, outer, depth+1)
			return
		}
		if attno-1 < len(vars.list) {
			t.expr(vars.list[attno-1], outer, depth+1)
			return
		}
		t.opaque = true
	case rteCTE:
		cte := findCTE(stack, len(stack)-1-up, rte.str("ctename"), rte.int("ctelevelsup"))
		if cte == nil {
			t.opaque = true
			return
		}
		owner := cte.owner
		cq := cte.node.field("ctequery")
		if cq == nil || cq.typ != "QUERY" {
			t.opaque = true
			return
		}
		t.column(cq, append(stack[:owner+1:owner+1], cq), attno, depth+1)
	case rteGroup:
		exprs := rte.field("groupexprs")
		if exprs != nil && exprs.isList && attno >= 1 && attno-1 < len(exprs.list) {
			t.expr(exprs.list[attno-1], outer, depth+1)
			return
		}
		t.opaque = true
	case rteFunction, rteTableFunc:
		// Output of a function: its arguments are inputs, not the output.
		t.opaque = true
		t.expr(rte.field("functions"), outer, depth+1)
		t.expr(rte.field("tablefunc"), outer, depth+1)
	case rteValues:
		t.expr(rte.field("values_lists"), outer, depth+1)
	default:
		t.opaque = true
	}
}

// column follows output column attno of a query (0: all of them). q is the
// last entry of stack. For UNION, INTERSECT and EXCEPT the column comes
// from the same column of every branch.
func (t *tracer) column(q *tnode, stack []*tnode, attno, depth int) {
	if depth > maxTreeDepth {
		t.opaque = true
		return
	}
	if isSetOp(q) {
		var leaves []int
		setOpLeaves(q.field("setOperations"), &leaves, 0)
		for _, idx := range leaves {
			rte := rangeEntry(q, idx)
			sub := rte.field("subquery")
			if rte == nil || sub == nil || sub.typ != "QUERY" {
				t.opaque = true
				continue
			}
			t.column(sub, append(stack[:len(stack):len(stack)], sub), attno, depth+1)
		}
		return
	}
	tl := q.field("targetList")
	if tl == nil {
		t.opaque = true
		return
	}
	for _, te := range tl.list {
		if te.str("resjunk") == "true" {
			continue
		}
		if attno == 0 || te.int("resno") == attno {
			t.expr(te.field("expr"), stack, depth+1)
		}
	}
}

func isSetOp(q *tnode) bool {
	so := q.field("setOperations")
	return so != nil && !so.isNull && so.typ != ""
}

func setOpLeaves(n *tnode, out *[]int, depth int) {
	if n == nil || depth > maxTreeDepth {
		return
	}
	switch n.typ {
	case "RANGETBLREF":
		*out = append(*out, n.int("rtindex"))
	case "SETOPERATIONSTMT":
		setOpLeaves(n.field("larg"), out, depth+1)
		setOpLeaves(n.field("rarg"), out, depth+1)
	}
}

func rangeEntry(q *tnode, varno int) *tnode {
	rt := q.field("rtable")
	if rt == nil || !rt.isList || varno < 1 || varno > len(rt.list) {
		return nil
	}
	return rt.list[varno-1]
}

type cteRef struct {
	node  *tnode
	owner int // index in the stack of the query holding the CTE
}

func findCTE(stack []*tnode, at int, name string, levelsup int) *cteRef {
	i := at - levelsup
	if i < 0 || i >= len(stack) {
		return nil
	}
	list := stack[i].field("cteList")
	if list == nil || !list.isList {
		return nil
	}
	for _, c := range list.list {
		if c.str("ctename") == name {
			return &cteRef{node: c, owner: i}
		}
	}
	return nil
}

// plain reports whether e is a Var that resolves, through subqueries,
// joins and CTEs, to another plain Var: the value is copied unchanged.
func (t *tracer) plain(e *tnode, stack []*tnode, depth int) bool {
	if e == nil || depth > 200 {
		return false
	}
	switch e.typ {
	case "RELABELTYPE": // a binary-compatible cast keeps the value
		return t.plain(e.field("arg"), stack, depth+1)
	case "VAR":
	default:
		return false
	}
	up := e.int("varlevelsup")
	if up < 0 || up >= len(stack) {
		return false
	}
	q := stack[len(stack)-1-up]
	outer := stack[:len(stack)-up]
	rte := rangeEntry(q, e.int("varno"))
	attno := e.int("varattno")
	if rte == nil || attno <= 0 {
		return false
	}
	switch rte.int("rtekind") {
	case rteRelation:
		return true
	case rteSubquery:
		sub := rte.field("subquery")
		return sub != nil && !isSetOp(sub) && t.plain(targetExpr(sub, attno), append(outer[:len(outer):len(outer)], sub), depth+1)
	case rteJoin:
		vars := rte.field("joinaliasvars")
		return vars != nil && attno-1 < len(vars.list) && t.plain(vars.list[attno-1], outer, depth+1)
	case rteCTE:
		cte := findCTE(stack, len(stack)-1-up, rte.str("ctename"), rte.int("ctelevelsup"))
		if cte == nil || cte.node.str("cterecursive") == "true" {
			return false
		}
		cq := cte.node.field("ctequery")
		return cq != nil && !isSetOp(cq) && t.plain(targetExpr(cq, attno), append(stack[:cte.owner+1:cte.owner+1], cq), depth+1)
	case rteGroup:
		exprs := rte.field("groupexprs")
		return exprs != nil && attno-1 < len(exprs.list) && t.plain(exprs.list[attno-1], outer, depth+1)
	}
	return false
}

func targetExpr(q *tnode, resno int) *tnode {
	tl := q.field("targetList")
	if tl == nil {
		return nil
	}
	for _, te := range tl.list {
		if te.int("resno") == resno && te.str("resjunk") != "true" {
			return te.field("expr")
		}
	}
	return nil
}

// viewNeed is one privilege PostgreSQL checks whenever a view is read: SELECT
// on a column (or anyColumn: on at least one), and, for SELECT ... FOR
// UPDATE and the like, UPDATE on a column or on at least one.
type viewNeed struct {
	Rel    uint32
	Attnum int
	Priv   string
}

// viewNeeds lists what a view's stored query reads under permission
// checks: its range table entries' selectedCols (RTEPermissionInfo from
// PostgreSQL 16 on), in the query and every subquery, sublink and CTE that
// is referenced. PostgreSQL checks all of them whenever anyone reads the
// view, even a single column of it, and refuses the whole query when one is
// missing. Attnum 0 is a whole-row reference, which needs every column;
// anyColumn is a reference to the relation that reads no column (count(*)),
// which needs SELECT on at least one. A CTE nothing references is never
// planned, so its tables aren't checked.
func viewNeeds(evAction string) ([]viewNeed, error) {
	root, err := parseNodeTree(evAction)
	if err != nil {
		return nil, err
	}
	var out []viewNeed
	var walk func(n *tnode, depth int)
	walk = func(n *tnode, depth int) {
		if n == nil || depth > maxTreeDepth {
			return
		}
		switch {
		case n.typ == "COMMONTABLEEXPR" && n.field("cterefcount") != nil && n.int("cterefcount") == 0:
			return
		case n.typ == "RTEPERMISSIONINFO": // 16 and later
			addNeeds(n, &out)
		case (n.typ == "RANGETBLENTRY" || n.typ == "RTE") && n.field("requiredPerms") != nil && n.int("rtekind") == 0:
			// Before 16; 13 and 14 call the node RTE.
			addNeeds(n, &out)
		}
		for _, f := range n.fields {
			walk(f, depth+1)
		}
		for _, x := range n.list {
			walk(x, depth+1)
		}
	}
	walk(root, 0)
	return dedupNeeds(out), nil
}

// Column sets in node trees hold attnum - FirstLowInvalidHeapAttributeNumber
// (-7 since PostgreSQL 12); ACL_SELECT is bit 1 of requiredPerms and
// ACL_UPDATE bit 2.
const (
	firstLowInvalidAttnum = -7
	aclSelect             = 1 << 1
	aclUpdate             = 1 << 2
	anyColumn             = -1
)

func addNeeds(n *tnode, out *[]viewNeed) {
	perms := n.int("requiredPerms")
	if perms&(aclSelect|aclUpdate) == 0 {
		return // the OLD and NEW placeholders of a view's rule, before 16
	}
	rel, err := strconv.ParseUint(n.str("relid"), 10, 32)
	if err != nil || rel == 0 {
		return
	}
	add := func(field, priv string) {
		found := false
		if cols := n.field(field); cols != nil {
			for _, m := range cols.list {
				k, err := strconv.Atoi(m.atom)
				if err != nil {
					continue // the "b" that starts a bitmapset
				}
				found = true
				if attnum := k + firstLowInvalidAttnum; attnum >= 0 {
					*out = append(*out, viewNeed{uint32(rel), attnum, priv})
				}
			}
		}
		if !found {
			*out = append(*out, viewNeed{uint32(rel), anyColumn, priv})
		}
	}
	if perms&aclSelect != 0 {
		add("selectedCols", "SELECT")
	}
	if perms&aclUpdate != 0 {
		add("updatedCols", "UPDATE")
	}
}

func dedupNeeds(ns []viewNeed) []viewNeed {
	seen := map[viewNeed]bool{}
	var out []viewNeed
	for _, n := range ns {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func dedupRefs(refs []colRef) []colRef {
	seen := map[colRef]bool{}
	var out []colRef
	for _, r := range refs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}
