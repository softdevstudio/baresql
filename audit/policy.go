package audit

import (
	"strings"

	"github.com/softdevstudio/baresql/internal/sqltext"
)

// Row security policies hold a condition, which PostgreSQL stores as an
// expression tree and prints with pg_get_expr. BareSQL Audit never runs a
// policy; it reads the simple shapes of that text and says which rows they
// let through: true and false, a column compared with CURRENT_USER,
// SESSION_USER, a session setting (current_setting) or constants, and
// those joined with AND and OR. Anything else (subqueries, function calls,
// boolean columns) is left unread, and the report names the policy only.

// rowKind orders what a policy condition lets through, from nothing to
// everything.
type rowKind int

const (
	rowsUnknown rowKind = iota // a condition BareSQL doesn't read
	rowsNone                   // false
	rowsFixed                  // rows with given values
	rowsOwn                    // rows tied to the role's or the login's name
	rowsSetting                // rows picked by a setting the session can change
	rowsEvery                  // true
)

// rowMeaning is what a condition lets through, in words.
type rowMeaning struct {
	kind     rowKind
	text     string
	op       string   // AND or OR, for a chain of parts, to bracket it inside another
	settings []string // the session settings it depends on
}

// policyMeaning reads a condition as pg_get_expr prints it.
func policyMeaning(expr string) rowMeaning {
	var ts []sqltext.Token
	for _, t := range sqltext.Tokenize(expr) {
		if t.Significant() {
			ts = append(ts, t)
		}
	}
	p := &condParser{ts: ts}
	m, ok := p.or()
	if !ok || p.i != len(p.ts) {
		return rowMeaning{}
	}
	return m
}

type condParser struct {
	ts []sqltext.Token
	i  int
}

func (p *condParser) at(k int) sqltext.Token {
	if p.i+k < len(p.ts) {
		return p.ts[p.i+k]
	}
	return sqltext.Token{}
}

func (p *condParser) or() (rowMeaning, bool) {
	return p.chain("OR", p.and)
}

func (p *condParser) and() (rowMeaning, bool) {
	return p.chain("AND", p.term)
}

// chain reads parts joined by one keyword and combines what they let
// through: with AND, the narrowest part decides; with OR, the widest.
func (p *condParser) chain(word string, part func() (rowMeaning, bool)) (rowMeaning, bool) {
	first, ok := part()
	if !ok {
		return rowMeaning{}, false
	}
	parts := []rowMeaning{first}
	for p.at(0).Is(word) {
		p.i++
		m, ok := part()
		if !ok {
			return rowMeaning{}, false
		}
		parts = append(parts, m)
	}
	if len(parts) == 1 {
		return first, true
	}
	out := rowMeaning{kind: parts[0].kind}
	var texts []string
	own := false
	for _, m := range parts {
		texts = append(texts, m.text)
		out.settings = append(out.settings, m.settings...)
		own = own || m.kind == rowsOwn
		switch {
		case m.kind == rowsUnknown || out.kind == rowsUnknown:
			// One unread part leaves the whole unread, except that false
			// in an AND and true in an OR decide it anyway.
			out.kind = rowsUnknown
		case word == "AND" && andRank[m.kind] < andRank[out.kind], word == "OR" && orRank[m.kind] > orRank[out.kind]:
			out.kind = m.kind
		}
	}
	// ANDed with the role's own name, a setting picks among the role's own
	// rows only.
	if word == "AND" && own {
		out.settings = nil
	}
	for _, m := range parts {
		if word == "AND" && m.kind == rowsNone {
			return rowMeaning{kind: rowsNone, text: "no row"}, true
		}
		if word == "OR" && m.kind == rowsEvery {
			return rowMeaning{kind: rowsEvery, text: "every row"}, true
		}
	}
	if out.kind == rowsUnknown {
		return rowMeaning{}, true
	}
	for i, m := range parts {
		if m.op != "" && m.op != word {
			texts[i] = "(" + texts[i] + ")"
		}
	}
	out.text = strings.Join(texts, strings.ToLower(" "+word+" "))
	out.op = word
	return out, true
}

// andRank orders kinds for AND by how much they decide: false and the
// role's own rows narrow the most; a setting decides which rows whatever
// constants sit beside it.
var andRank = map[rowKind]int{rowsNone: 0, rowsOwn: 1, rowsSetting: 2, rowsFixed: 3, rowsEvery: 4}

// orRank orders kinds for OR by how much they let through: the role's own
// rows OR rows with a constant reach other roles' rows too.
var orRank = map[rowKind]int{rowsNone: 0, rowsOwn: 1, rowsFixed: 2, rowsSetting: 3, rowsEvery: 4}

// term is a parenthesized condition, true, false, or a comparison.
func (p *condParser) term() (rowMeaning, bool) {
	t := p.at(0)
	switch {
	case t.Is("TRUE") && !p.at(1).IsOp("::"):
		p.i++
		return rowMeaning{kind: rowsEvery, text: "every row"}, true
	case t.Is("FALSE") && !p.at(1).IsOp("::"):
		p.i++
		return rowMeaning{kind: rowsNone, text: "no row"}, true
	case t.IsOp("("):
		// A parenthesized condition, or an operand in parentheses that
		// starts a comparison: try the condition first.
		save := p.i
		p.i++
		m, ok := p.or()
		if ok && p.at(0).IsOp(")") && !p.at(1).IsOp("=", "::") {
			p.i++
			return m, true
		}
		p.i = save
	}
	return p.comparison()
}

// operand kinds in a comparison
type operand struct {
	kind    string // column, user, session-user, setting, literal, list
	text    string // the column, the setting, the literal or list as SQL
	setting string
}

func (p *condParser) comparison() (rowMeaning, bool) {
	l, ok := p.operand()
	if !ok || !p.at(0).IsOp("=") {
		// A bare operand (a boolean column, a function call) is unread.
		return rowMeaning{}, ok && p.skipUnread()
	}
	p.i++
	var r operand
	if p.at(0).Is("ANY") && p.at(1).IsOp("(") {
		p.i += 2
		list, ok := p.array()
		if !ok || !p.at(0).IsOp(")") {
			return rowMeaning{}, false
		}
		p.i++
		r = list
	} else if r, ok = p.operand(); !ok {
		return rowMeaning{}, false
	}
	if l.kind != "column" {
		l, r = r, l
	}
	switch {
	case l.kind == "column" && r.kind == "user":
		return rowMeaning{kind: rowsOwn, text: l.text + " is the name of the current role"}, true
	case l.kind == "column" && r.kind == "session-user":
		return rowMeaning{kind: rowsOwn, text: l.text + " is the name of the login"}, true
	case l.kind == "column" && r.kind == "setting":
		return rowMeaning{kind: rowsSetting, text: l.text + " matches the setting " + r.setting, settings: []string{r.setting}}, true
	case l.kind == "column" && r.kind == "literal":
		return rowMeaning{kind: rowsFixed, text: l.text + " is " + r.text}, true
	case l.kind == "column" && r.kind == "list":
		return rowMeaning{kind: rowsFixed, text: l.text + " is one of " + r.text}, true
	case l.kind == "setting" && r.kind == "literal", l.kind == "literal" && r.kind == "setting":
		s := l
		if s.kind != "setting" {
			s, r = r, l
		}
		return rowMeaning{kind: rowsSetting, text: "the setting " + s.setting + " is " + r.text, settings: []string{s.setting}}, true
	}
	return rowMeaning{}, true
}

// skipUnread steps over the rest of an unread term up to AND, OR or a
// closing parenthesis at this level, so the parts around it can still be
// read.
func (p *condParser) skipUnread() bool {
	depth := 0
	for p.i < len(p.ts) {
		t := p.ts[p.i]
		switch {
		case t.IsOp("("):
			depth++
		case t.IsOp(")"):
			if depth == 0 {
				return true
			}
			depth--
		case depth == 0 && (t.Is("AND") || t.Is("OR")):
			return true
		}
		p.i++
	}
	return depth == 0
}

// operand reads one side of a comparison, dropping casts.
func (p *condParser) operand() (operand, bool) {
	var o operand
	t := p.at(0)
	switch {
	case t.IsOp("("):
		p.i++
		inner, ok := p.operand()
		if !ok || !p.at(0).IsOp(")") {
			return operand{}, false
		}
		p.i++
		o = inner
	case t.Is("CURRENT_USER") || t.Is("USER") || t.Is("CURRENT_ROLE"):
		p.i++
		o = operand{kind: "user"}
	case t.Is("SESSION_USER"):
		p.i++
		o = operand{kind: "session-user"}
	case t.Is("current_setting") && p.at(1).IsOp("("):
		// pg_get_expr prints pg_catalog's current_setting without a schema
		// (the audit's search_path is pg_catalog); one from another schema
		// would have one, and isn't read.
		p.i += 2
		name := p.at(0)
		if name.Kind != sqltext.String {
			return operand{}, false
		}
		p.i++
		p.casts()
		if p.at(0).IsOp(",") {
			p.i++
			if !(p.at(0).Is("TRUE") || p.at(0).Is("FALSE")) {
				return operand{}, false
			}
			p.i++
		}
		if !p.at(0).IsOp(")") {
			return operand{}, false
		}
		p.i++
		setting := name.StringValue()
		// Placeholder settings (with a dot) can be set by any session;
		// built-in ones, and those of extensions that register them as
		// superuser-only, aren't read here.
		if !strings.Contains(setting, ".") || reservedPrefix(setting) {
			o = operand{kind: "other"}
		} else {
			o = operand{kind: "setting", setting: setting}
		}
	case t.Kind == sqltext.Word && !p.at(1).IsOp("(") && !keywordLike(t):
		p.i++
		o = operand{kind: "column", text: t.Text}
	case t.Kind == sqltext.QuotedIdent:
		p.i++
		o = operand{kind: "column", text: t.Text}
	case t.Kind == sqltext.String, t.Kind == sqltext.Number:
		p.i++
		o = operand{kind: "literal", text: t.Text}
	case t.IsOp("-") && p.at(1).Kind == sqltext.Number:
		p.i += 2
		o = operand{kind: "literal", text: "-" + p.ts[p.i-1].Text}
	default:
		// Something else: a function call, a subquery. Step over it.
		if t.Kind == sqltext.Word && p.at(1).IsOp("(") || t.IsOp("(") {
			p.i++
		}
		return operand{kind: "other"}, p.skipOperand()
	}
	p.casts()
	return o, true
}

// skipOperand steps over the rest of an operand that isn't read: up to a
// comparison, AND, OR or a closing parenthesis at this level.
func (p *condParser) skipOperand() bool {
	depth := 0
	for p.i < len(p.ts) {
		t := p.ts[p.i]
		switch {
		case t.IsOp("("):
			depth++
		case t.IsOp(")"):
			if depth == 0 {
				return true
			}
			depth--
		case depth == 0 && (t.IsOp("=") || t.Is("AND") || t.Is("OR")):
			return true
		}
		p.i++
	}
	return depth == 0
}

// casts drops ::type, ::type[] and ::"type" after an operand.
func (p *condParser) casts() {
	for p.at(0).IsOp("::") {
		p.i += 2
		for p.at(0).Kind == sqltext.Word && !p.at(0).Is("AND") && !p.at(0).Is("OR") { // character varying, double precision
			p.i++
		}
		if p.at(0).IsOp("[") && p.at(1).IsOp("]") {
			p.i += 2
		}
	}
}

// array reads ARRAY[literal, ...] (with casts), as = ANY (ARRAY[...])
// prints IN lists.
func (p *condParser) array() (operand, bool) {
	if !p.at(0).Is("ARRAY") || !p.at(1).IsOp("[") {
		return operand{}, false
	}
	p.i += 2
	var items []string
	for {
		v, ok := p.operand()
		if !ok || v.kind != "literal" {
			return operand{}, false
		}
		items = append(items, v.text)
		if p.at(0).IsOp(",") {
			p.i++
			continue
		}
		break
	}
	if !p.at(0).IsOp("]") {
		return operand{}, false
	}
	p.i++
	p.casts()
	return operand{kind: "list", text: strings.Join(items, ", ")}, true
}

// keywordLike: words pg_get_expr prints that aren't column names.
func keywordLike(t sqltext.Token) bool {
	switch t.Upper() {
	case "AND", "OR", "NOT", "NULL", "TRUE", "FALSE", "ANY", "ALL", "ARRAY", "SELECT", "CASE", "EXISTS", "IS", "IN",
		// SQL value functions, which pg_get_expr prints without parentheses.
		"CURRENT_CATALOG", "CURRENT_SCHEMA", "SYSTEM_USER", "CURRENT_DATE", "CURRENT_TIME", "CURRENT_TIMESTAMP",
		"LOCALTIME", "LOCALTIMESTAMP":
		return true
	}
	return false
}

// literal values print as pg_get_expr writes them: 'eu'::text becomes 'eu'.

// policyList splits the policy names out of a Rows text: "rows allowed by
// policy a, b and restricted by c" gives [a b] and [c].
func policyList(rows string) (permissive, restrictive []string, ok bool) {
	rest, ok := strings.CutPrefix(rows, "rows allowed by policy ")
	if !ok {
		return nil, nil, false
	}
	perm, restr, _ := strings.Cut(rest, " and restricted by ")
	permissive = strings.Split(perm, ", ")
	if restr != "" {
		restrictive = strings.Split(restr, ", ")
	}
	return permissive, restrictive, true
}

// whereText says in words what the named policies of rel let through for
// cmd, for the conditions BareSQL reads; "" when it reads none of them.
func (a *analysis) whereText(rel *Relation, cmd, rows string) string {
	perm, restr, ok := policyList(rows)
	if !ok {
		return ""
	}
	byName := map[string]*Policy{}
	for i := range a.s.Policies {
		p := &a.s.Policies[i]
		if p.Rel == rel.OID && (p.Cmd == "ALL" || p.Cmd == cmd) {
			byName[quoteIdent(p.Name)] = p
		}
	}
	read := 0
	say := func(n string) string {
		p := byName[n]
		if p == nil {
			return n + ": condition not read"
		}
		m := policyCondition(p, cmd)
		if m.kind == rowsUnknown {
			return n + ": condition not read"
		}
		read++
		text := m.text
		if len(m.settings) == 1 && strings.HasSuffix(text, "the setting "+m.settings[0]) {
			text += ", which any session can set"
		} else if len(m.settings) > 0 {
			text += "; any session can set " + strings.Join(m.settings, " and ")
		}
		return n + ": " + text
	}
	var ps, rs []string
	for _, n := range perm {
		ps = append(ps, say(n))
	}
	for _, n := range restr {
		rs = append(rs, say(n))
	}
	if read == 0 {
		return ""
	}
	if len(perm)+len(restr) == 1 {
		_, text, _ := strings.Cut(ps[0], ": ")
		return text
	}
	// Permissive policies let rows through when any of them does;
	// restrictive ones each have to.
	out := strings.Join(ps, "; or ")
	if len(rs) > 0 {
		out += "; and always " + strings.Join(rs, "; and ")
	}
	return out
}

// fillRowsWhere adds RowsWhere to every route whose Rows names policies.
func (a *analysis) fillRowsWhere(res *Result) {
	rels := map[string]*Relation{}
	for _, rel := range a.s.Relations {
		rels[a.s.QualifiedName(rel)] = rel
	}
	fill := func(rel string, cmd string, routes []Route) {
		r := rels[rel]
		if r == nil {
			return
		}
		for i := range routes {
			routes[i].RowsWhere = a.whereText(r, cmd, routes[i].Rows)
		}
	}
	for _, l := range append(append([]LoginReport{}, res.Logins...), res.Public) {
		for _, c := range l.Read {
			fill(c.Relation, "SELECT", c.Routes)
		}
		for _, w := range l.Write {
			fill(w.Relation, w.Privilege, w.Routes)
		}
	}
}

// reservedPrefix reports whether a placeholder setting belongs to an
// extension that registers its settings for superusers only, or that are
// otherwise not a session's to change: such a setting is no session's
// choice.
func reservedPrefix(setting string) bool {
	prefix, _, _ := strings.Cut(strings.ToLower(setting), ".")
	switch prefix {
	case "pgaudit", "auto_explain", "pg_stat_statements", "pg_cron", "pglogical", "citus", "timescaledb",
		"pgsodium", "pg_net", "supautils", "pg_partman_bgw", "pg_prewarm", "pgrst":
		return true
	}
	return false
}
