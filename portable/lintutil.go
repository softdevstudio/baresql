package portable

import (
	"strings"

	"github.com/softdevstudio/baresql/internal/sqltext"
)

// The helpers in this file let rules look at a statement's significant
// tokens a little like a parser would: matching parentheses, the operand
// on each side of an operator, a rough type for an expression, and the
// tables and aliases a statement names.

func isName(t sqltext.Token) bool { return t.Kind == sqltext.Word || t.Kind == sqltext.QuotedIdent }

// newLintCtx prepares a statement for the rules.
func newLintCtx(file string, st sqltext.Statement, schema *Schema) *lintCtx {
	ts := st.Significant()
	c := &lintCtx{file: file, ts: ts, schema: schema, pair: make([]int, len(ts)), depth: make([]int, len(ts))}
	var stack []int
	d := 0
	for i, t := range ts {
		c.pair[i] = -1
		switch {
		case t.IsOp("("):
			c.depth[i] = d
			stack = append(stack, i)
			d++
		case t.IsOp(")"):
			if d > 0 {
				d--
			}
			c.depth[i] = d
			if len(stack) > 0 {
				o := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				c.pair[o], c.pair[i] = i, o
			}
		default:
			c.depth[i] = d
		}
	}
	c.findTables()
	return c
}

// close returns the index of the parenthesis matching the one at i, or
// the last token.
func (c *lintCtx) close(i int) int {
	if i >= 0 && i < len(c.pair) && c.pair[i] >= 0 {
		return c.pair[i]
	}
	return len(c.ts) - 1
}

// args splits the arguments of the call whose "(" is at open into
// [start, end] index ranges.
func (c *lintCtx) args(open int) [][2]int {
	end := c.close(open)
	var out [][2]int
	start := open + 1
	for j := open + 1; j <= end; j++ {
		if j == end || c.ts[j].IsOp(",") && c.depth[j] == c.depth[open]+1 {
			if j > start {
				out = append(out, [2]int{start, j - 1})
			}
			start = j + 1
		}
	}
	return out
}

// keywordsBeforeParen are words that can stand before "(" without being a
// function name.
var keywordsBeforeParen = map[string]bool{
	"IN": true, "EXISTS": true, "AND": true, "OR": true, "NOT": true, "ON": true, "USING": true, "VALUES": true,
	"WHERE": true, "SELECT": true, "FROM": true, "JOIN": true, "AS": true, "WHEN": true, "THEN": true, "ELSE": true,
	"BY": true, "HAVING": true, "ANY": true, "ALL": true, "SOME": true, "OVER": true, "INTO": true, "SET": true,
	"RETURNING": true, "UNION": true, "INTERSECT": true, "EXCEPT": true, "DISTINCT": true, "TABLE": true,
	"KEY": true, "CHECK": true, "DEFAULT": true, "LIMIT": true, "OFFSET": true, "IS": true, "LIKE": true,
	"BETWEEN": true, "CASE": true, "WITH": true, "FILTER": true, "WITHIN": true, "UNIQUE": true, "INDEX": true,
	"REFERENCES": true, "PRIMARY": true, "FOREIGN": true, "CONSTRAINT": true, "LATERAL": true,
}

// isCall reports whether a function call starts at i (a name directly
// followed by "(").
func (c *lintCtx) isCall(i int) bool {
	t := c.at(i)
	return t.Kind == sqltext.Word && c.at(i+1).IsOp("(") && !keywordsBeforeParen[t.Upper()] && !c.at(i-1).IsOp(".")
}

// primaryEnd returns the last token of the operand that starts at i: a
// literal, a (qualified) name, a call, a parenthesized expression, a CASE
// ... END, or one of those after a unary sign.
func (c *lintCtx) primaryEnd(i int) int {
	t := c.at(i)
	switch {
	case i >= len(c.ts):
		return len(c.ts) - 1
	case t.IsOp("-", "+", "~") && i+1 < len(c.ts):
		return c.primaryEnd(i + 1)
	case t.IsOp("("):
		return c.close(i)
	case t.Kind == sqltext.Word && t.Is("CASE"):
		depth := 0
		for j := i; j < len(c.ts); j++ {
			if c.ts[j].Is("CASE") {
				depth++
			} else if c.ts[j].Is("END") {
				depth--
				if depth == 0 {
					return j
				}
			}
		}
		return len(c.ts) - 1
	case t.Kind == sqltext.Word && typedLiteral[t.Upper()] && c.at(i+1).Kind == sqltext.String:
		if t.Is("INTERVAL") && c.at(i+2).Kind == sqltext.Word && intervalUnit[c.at(i+2).Upper()] {
			return i + 2
		}
		return i + 1
	case t.Kind == sqltext.Word && t.Is("INTERVAL") && c.at(i+1).Kind == sqltext.Number && intervalUnit[c.at(i+2).Upper()]:
		return i + 2
	case isName(t):
		e := i
		for e+2 < len(c.ts) && c.ts[e+1].IsOp(".") && (isName(c.ts[e+2]) || c.ts[e+2].IsOp("*")) {
			e += 2
		}
		if c.at(e + 1).IsOp("(") {
			return c.close(e + 1)
		}
		return e
	}
	return i
}

// primaryStart returns the first token of the operand that ends at i.
func (c *lintCtx) primaryStart(i int) int {
	t := c.at(i)
	switch {
	case i < 0:
		return 0
	case t.IsOp(")"):
		open := c.pair[i]
		if open < 0 {
			return i
		}
		if open > 0 && isName(c.ts[open-1]) && !keywordsBeforeParen[c.ts[open-1].Upper()] {
			s := open - 1
			for s >= 2 && c.ts[s-1].IsOp(".") && isName(c.ts[s-2]) {
				s -= 2
			}
			return s
		}
		return open
	case t.Kind == sqltext.String && i > 0 && c.ts[i-1].Kind == sqltext.Word && typedLiteral[c.ts[i-1].Upper()]:
		return i - 1
	case t.Kind == sqltext.Word && intervalUnit[t.Upper()] && i >= 2 && c.ts[i-2].Is("INTERVAL"):
		return i - 2
	case t.Is("END"):
		depth := 0
		for j := i; j >= 0; j-- {
			if c.ts[j].Is("END") {
				depth++
			} else if c.ts[j].Is("CASE") {
				depth--
				if depth == 0 {
					return j
				}
			}
		}
		return 0
	case isName(t) || t.IsOp("*"):
		s := i
		for s >= 2 && c.ts[s-1].IsOp(".") && isName(c.ts[s-2]) {
			s -= 2
		}
		return s
	}
	return i
}

var typedLiteral = map[string]bool{"DATE": true, "TIME": true, "TIMESTAMP": true, "INTERVAL": true}

var intervalUnit = map[string]bool{
	"YEAR": true, "MONTH": true, "WEEK": true, "DAY": true, "HOUR": true, "MINUTE": true, "SECOND": true,
	"YEARS": true, "MONTHS": true, "DAYS": true, "HOURS": true, "MINUTES": true, "SECONDS": true,
	"MICROSECOND": true, "QUARTER": true,
}

// boundaryWord ends an expression when scanning outwards from an operator.
var boundaryWord = map[string]bool{
	"AND": true, "OR": true, "NOT": true, "WHERE": true, "ON": true, "WHEN": true, "THEN": true, "ELSE": true,
	"END": true, "SELECT": true, "FROM": true, "HAVING": true, "BY": true, "AS": true, "IS": true, "IN": true,
	"LIKE": true, "ILIKE": true, "BETWEEN": true, "CASE": true, "SET": true, "VALUES": true, "LIMIT": true,
	"OFFSET": true, "UNION": true, "JOIN": true, "USING": true, "RETURNING": true, "ESCAPE": true, "DESC": true,
	"ASC": true, "INTO": true, "GROUP": true, "ORDER": true, "DISTINCT": true, "ALL": true, "ANY": true,
	"SOME": true, "EXISTS": true, "REGEXP": true, "RLIKE": true, "GLOB": true, "COLLATE": true, "NULLS": true,
	"FETCH": true, "WINDOW": true, "INTERSECT": true, "EXCEPT": true,
}

var arithmetic = []string{"+", "-", "*", "/", "%", "||", "^", "&", "|", "<<", ">>", "DIV", "MOD"}

func isArith(t sqltext.Token) bool {
	return t.IsOp(arithmetic[:11]...) || t.Is("DIV") || t.Is("MOD")
}

// exprBefore returns the start of the expression that ends at i and is
// joined by arithmetic operators, stopping at comparisons, AND, OR,
// commas and keywords.
func (c *lintCtx) exprBefore(i int) int {
	s := c.primaryStart(i)
	for s >= 2 && isArith(c.ts[s-1]) {
		s2 := c.primaryStart(s - 2)
		if s2 < 0 || c.ts[s2].Kind == sqltext.Word && boundaryWord[c.ts[s2].Upper()] {
			break
		}
		s = s2
	}
	return s
}

// exprAfter returns the end of the expression that starts at i.
func (c *lintCtx) exprAfter(i int) int {
	if c.at(i).Kind == sqltext.Word && boundaryWord[c.at(i).Upper()] && !c.at(i).Is("CASE") {
		return i - 1
	}
	e := c.primaryEnd(i)
	for e+2 < len(c.ts) && isArith(c.ts[e+1]) {
		e = c.primaryEnd(e + 2)
	}
	return e
}

// vkind is the rough type of an operand.
type vkind int

const (
	kUnknown vkind = iota
	kInt
	kDecimal
	kFloat
	kText
	kDate
	kInterval
	kBool
	kNull
	kBytes
)

func (k vkind) numeric() bool { return k == kInt || k == kDecimal || k == kFloat }

// typeKind classifies a column type name.
func typeKind(typ string) vkind {
	f := strings.Fields(strings.ToUpper(typ))
	if len(f) == 0 {
		return kUnknown
	}
	t := f[0]
	switch {
	case intTypes[t]:
		return kInt
	case t == "DECIMAL" || t == "NUMERIC" || t == "DEC" || t == "MONEY" || t == "NUMBER":
		return kDecimal
	case t == "REAL" || t == "FLOAT" || t == "DOUBLE" || t == "FLOAT4" || t == "FLOAT8":
		return kFloat
	case strings.Contains(t, "CHAR") || strings.Contains(t, "TEXT") || t == "CLOB" || t == "STRING" || t == "ENUM" || t == "UUID" || t == "CITEXT":
		return kText
	case t == "DATE" || t == "TIME" || t == "TIMESTAMP" || t == "DATETIME" || t == "TIMESTAMPTZ" || t == "TIMETZ" || t == "YEAR":
		return kDate
	case t == "BOOLEAN" || t == "BOOL":
		return kBool
	case strings.Contains(t, "BLOB") || t == "BYTEA" || t == "BINARY" || t == "VARBINARY":
		return kBytes
	case t == "INTERVAL":
		return kInterval
	}
	return kUnknown
}

var intFunctionKinds = map[string]vkind{
	"COUNT": kInt, "LENGTH": kInt, "CHAR_LENGTH": kInt, "CHARACTER_LENGTH": kInt, "OCTET_LENGTH": kInt,
	"ROW_NUMBER": kInt, "RANK": kInt, "DENSE_RANK": kInt, "NTILE": kInt, "INSTR": kInt, "STRPOS": kInt,
	"LOWER": kText, "UPPER": kText, "CONCAT": kText, "CONCAT_WS": kText, "SUBSTR": kText, "SUBSTRING": kText,
	"TRIM": kText, "LTRIM": kText, "RTRIM": kText, "REPLACE": kText, "LEFT": kText, "RIGHT": kText,
	"LPAD": kText, "RPAD": kText, "GROUP_CONCAT": kText, "STRING_AGG": kText,
	"NOW": kDate, "CURDATE": kDate, "SYSDATE": kDate, "DATE": kDate, "DATETIME": kDate, "CURTIME": kDate,
	"AVG": kDecimal,
}

// sameKindFunctions return the kind of their first argument.
var sameKindFunctions = map[string]bool{"SUM": true, "MIN": true, "MAX": true, "ABS": true, "COALESCE": true, "IFNULL": true, "NULLIF": true}

// kindOf types the operand or expression in tokens s..e.
func (c *lintCtx) kindOf(s, e int) vkind {
	if s > e || s < 0 || e >= len(c.ts) {
		return kUnknown
	}
	// An arithmetic expression: combine the kinds of its operands.
	if pe := c.primaryEnd(s); pe < e {
		kinds := []vkind{c.kindOfPrimary(s, pe)}
		i := pe + 1
		for i <= e {
			op := c.ts[i]
			if !isArith(op) || i+1 > e {
				return kUnknown
			}
			if op.IsOp("||") {
				return kUnknown
			}
			pe = c.primaryEnd(i + 1)
			if pe > e {
				return kUnknown
			}
			kinds = append(kinds, c.kindOfPrimary(i+1, pe))
			i = pe + 1
		}
		return combine(kinds)
	}
	return c.kindOfPrimary(s, e)
}

func combine(kinds []vkind) vkind {
	out := kInt
	for _, k := range kinds {
		switch {
		case k == kDate || k == kInterval:
			return k
		case !k.numeric():
			return kUnknown
		case k > out:
			out = k
		}
	}
	return out
}

// kindOfPrimary types one operand.
func (c *lintCtx) kindOfPrimary(s, e int) vkind {
	if s < 0 || e >= len(c.ts) || s > e {
		return kUnknown
	}
	t := c.ts[s]
	switch {
	case t.IsOp("-", "+") && s < e:
		return c.kindOfPrimary(s+1, e)
	case t.IsOp("(") && c.close(s) == e:
		if c.at(s + 1).Is("SELECT") {
			return kUnknown
		}
		return c.kindOf(s+1, e-1)
	case t.Kind == sqltext.Number && s == e:
		switch {
		case strings.HasPrefix(t.Text, "0x") || strings.HasPrefix(t.Text, "0X"):
			return kInt
		case strings.ContainsAny(t.Text, "eE"):
			return kFloat
		case strings.Contains(t.Text, "."):
			return kDecimal
		}
		return kInt
	case t.Kind == sqltext.String && s == e:
		if strings.HasPrefix(t.Text, "X'") || strings.HasPrefix(t.Text, "x'") {
			return kBytes
		}
		return kText
	case t.Kind == sqltext.Word && typedLiteral[t.Upper()] && e == s+1:
		if t.Is("INTERVAL") {
			return kInterval
		}
		return kDate
	case t.Is("INTERVAL"):
		return kInterval
	case t.Kind == sqltext.Word && s == e:
		switch t.Upper() {
		case "NULL":
			return kNull
		case "TRUE", "FALSE":
			return kBool
		case "CURRENT_DATE", "CURRENT_TIMESTAMP", "CURRENT_TIME", "LOCALTIMESTAMP", "LOCALTIME":
			return kDate
		}
		return c.columnKind(s, e)
	case isName(t) && c.at(e).IsOp(")") && c.at(c.pair[e]-1).Kind == sqltext.Word:
		// A call.
		name := c.ts[c.pair[e]-1].Upper()
		open := c.pair[e]
		if name == "CAST" {
			for k := e - 1; k > open; k-- {
				if c.ts[k].Is("AS") && c.depth[k] == c.depth[open]+1 {
					return typeKind(c.ts[k+1].Text)
				}
			}
			return kUnknown
		}
		if k, ok := intFunctionKinds[name]; ok {
			return k
		}
		if sameKindFunctions[name] {
			if a := c.args(open); len(a) > 0 {
				return c.kindOf(a[0][0], a[0][1])
			}
		}
		return kUnknown
	case isName(t):
		return c.columnKind(s, e)
	}
	return kUnknown
}

// columnKind types a column reference (name or qualifier.name) from the
// schema.
func (c *lintCtx) columnKind(s, e int) vkind {
	ci, ok := c.column(s, e)
	if !ok {
		return kUnknown
	}
	return typeKind(ci.typ)
}

// column looks up the column referenced by tokens s..e.
func (c *lintCtx) column(s, e int) (colInfo, bool) {
	if c.schema == nil || c.schema.cols == nil {
		return colInfo{}, false
	}
	switch {
	case s == e && isName(c.ts[s]):
		name := identName(c.ts[s])
		// Unqualified: the tables this statement names first, then any.
		var found colInfo
		n := 0
		for _, tbl := range c.tables {
			if ci, ok := c.schema.cols[strings.ToLower(tbl)][strings.ToLower(name)]; ok {
				found = ci
				n++
			}
		}
		if n == 1 {
			return found, true
		}
		if n == 0 && len(c.tables) == 0 {
			return c.schema.column("", name)
		}
		return colInfo{}, false
	case e == s+2 && c.ts[s+1].IsOp("."):
		q := strings.ToLower(identName(c.ts[s]))
		if t, ok := c.aliases[q]; ok {
			q = t
		}
		return c.schema.column(q, identName(c.ts[e]))
	}
	return colInfo{}, false
}

// findTables collects the tables a statement names after FROM, JOIN,
// UPDATE, INTO and TABLE, with their aliases.
func (c *lintCtx) findTables() {
	c.aliases = map[string]string{}
	for i, t := range c.ts {
		if !(t.Is("FROM") || t.Is("JOIN") || t.Is("UPDATE") || t.Is("INTO") || t.Is("TABLE")) {
			continue
		}
		j := i + 1
		for {
			if !isName(c.at(j)) || keywordsBeforeParen[c.at(j).Upper()] && c.at(j).Kind == sqltext.Word {
				break
			}
			name := identName(c.ts[j])
			k := j
			for c.at(k+1).IsOp(".") && isName(c.at(k+2)) { // schema.table
				k += 2
				name = identName(c.ts[k])
			}
			if c.at(k + 1).IsOp("(") { // a function in FROM, or CREATE TABLE t (
				if t.Is("TABLE") || t.Is("INTO") {
					c.tables = append(c.tables, name)
				}
				break
			}
			c.tables = append(c.tables, name)
			k++
			if c.at(k).Is("AS") {
				k++
			}
			if a := c.at(k); a.Kind == sqltext.Word && !boundaryWord[a.Upper()] && !joinWord[a.Upper()] && !keywordsBeforeParen[a.Upper()] || a.Kind == sqltext.QuotedIdent {
				c.aliases[strings.ToLower(identName(a))] = strings.ToLower(name)
				k++
			}
			if !c.at(k).IsOp(",") || !t.Is("FROM") {
				break
			}
			j = k + 1
		}
	}
}

var joinWord = map[string]bool{"LEFT": true, "RIGHT": true, "INNER": true, "OUTER": true, "FULL": true, "CROSS": true, "NATURAL": true, "STRAIGHT_JOIN": true, "LATERAL": true}

// span is tokens s..e as one line of text.
func (c *lintCtx) span(s, e int) string {
	var b strings.Builder
	for k := s; k <= e && k < len(c.ts); k++ {
		t := c.ts[k]
		if k > s && !t.IsOp(",", ")", ".") && !c.ts[k-1].IsOp("(", ".") && !(t.IsOp("(") && c.ts[k-1].Kind == sqltext.Word && !keywordsBeforeParen[c.ts[k-1].Upper()]) {
			b.WriteByte(' ')
		}
		b.WriteString(t.Text)
	}
	return b.String()
}

// columnDef is one column definition of CREATE TABLE.
type columnDef struct {
	name       int // index of the column name
	typ        int // index of the type's first word
	typEnd     int // last token of the type, with any (n) or (p, s)
	end        int // last token of the definition
	typeName   string
	constraint bool // a table constraint, not a column
}

// createTable returns the column definitions of a CREATE TABLE statement,
// the index of the "(" that opens them, and of its ")".
func (c *lintCtx) createTable() (defs []columnDef, open, close int) {
	if len(c.ts) < 4 || !c.ts[0].Is("CREATE") {
		return nil, -1, -1
	}
	i := 1
	for i < len(c.ts) && (c.ts[i].Is("TEMP") || c.ts[i].Is("TEMPORARY") || c.ts[i].Is("UNLOGGED") || c.ts[i].Is("GLOBAL") || c.ts[i].Is("LOCAL") || c.ts[i].Is("OR") || c.ts[i].Is("REPLACE")) {
		i++
	}
	if !c.at(i).Is("TABLE") {
		return nil, -1, -1
	}
	for i < len(c.ts) && !c.ts[i].IsOp("(") {
		if c.ts[i].Is("AS") || c.ts[i].Is("SELECT") || c.ts[i].Is("LIKE") {
			return nil, -1, -1
		}
		i++
	}
	if i >= len(c.ts) {
		return nil, -1, -1
	}
	open, close = i, c.close(i)
	start := open + 1
	for j := open + 1; j <= close; j++ {
		if j == close || c.ts[j].IsOp(",") && c.depth[j] == c.depth[open]+1 {
			if j-1 >= start {
				defs = append(defs, c.columnDef(start, j-1))
			}
			start = j + 1
		}
	}
	return defs, open, close
}

func (c *lintCtx) columnDef(s, e int) columnDef {
	d := columnDef{name: s, typ: -1, typEnd: -1, end: e}
	if isConstraintWord(c.ts[s]) || c.ts[s].Is("FULLTEXT") || c.ts[s].Is("SPATIAL") || c.ts[s].Is("EXCLUDE") {
		d.constraint = true
		return d
	}
	if s+1 > e {
		return d
	}
	d.typ = s + 1
	d.typEnd = s + 1
	d.typeName = strings.ToUpper(c.ts[s+1].Text)
	k := s + 2
	// Multi-word types: DOUBLE PRECISION, CHARACTER VARYING, TIMESTAMP WITH TIME ZONE ...
	for k <= e && c.ts[k].Kind == sqltext.Word && typeContinuation(d.typeName, c.ts[k].Upper()) {
		d.typeName += " " + c.ts[k].Upper()
		d.typEnd = k
		k++
	}
	if k <= e && c.ts[k].IsOp("(") {
		d.typEnd = c.close(k)
		k = d.typEnd + 1
	}
	for k <= e && (c.ts[k].Is("UNSIGNED") || c.ts[k].Is("SIGNED") || c.ts[k].Is("ZEROFILL")) {
		k++
	}
	if k <= e && c.ts[k].IsOp("[") {
		d.typEnd = k
		if c.at(k + 1).IsOp("]") {
			d.typEnd = k + 1
		}
	}
	return d
}

func typeContinuation(base, w string) bool {
	switch base {
	case "DOUBLE":
		return w == "PRECISION"
	case "CHARACTER", "CHAR", "NATIONAL":
		return w == "VARYING" || w == "CHARACTER" || w == "CHAR"
	case "TIMESTAMP", "TIME", "TIMESTAMP WITH", "TIME WITH", "TIMESTAMP WITHOUT", "TIME WITHOUT", "TIMESTAMP WITH TIME", "TIME WITH TIME", "TIMESTAMP WITHOUT TIME", "TIME WITHOUT TIME":
		return w == "WITH" || w == "WITHOUT" || w == "TIME" || w == "ZONE"
	case "LONG":
		return w == "VARCHAR" || w == "VARBINARY"
	}
	return false
}

// selectBlocks returns the index of each SELECT keyword that starts a
// query block, and the end of that block (exclusive): the next UNION,
// INTERSECT or EXCEPT at the same depth, the closing parenthesis, or the
// end of the statement.
func (c *lintCtx) selectBlocks() [][2]int {
	var out [][2]int
	for i, t := range c.ts {
		if !t.Is("SELECT") {
			continue
		}
		d := c.depth[i]
		end := len(c.ts)
		for j := i + 1; j < len(c.ts); j++ {
			if c.depth[j] < d || c.ts[j].IsOp(")") && c.depth[j] == d-1 {
				end = j
				break
			}
			if c.depth[j] == d && (c.ts[j].Is("UNION") || c.ts[j].Is("INTERSECT") || c.ts[j].Is("EXCEPT")) {
				end = j
				break
			}
		}
		out = append(out, [2]int{i, end})
	}
	return out
}

// clause finds the top-level clause of a query block starting with the
// given keyword (WHERE, HAVING, FROM, ...): its first token after the
// keyword and its end (exclusive). It returns -1 when there is none.
func (c *lintCtx) clause(block [2]int, keyword ...string) (int, int) {
	d := c.depth[block[0]]
	start := -1
	for j := block[0] + 1; j < block[1]; j++ {
		if c.depth[j] != d {
			continue
		}
		t := c.ts[j]
		if start < 0 {
			if t.Is(keyword[0]) && (len(keyword) == 1 || c.at(j+1).Is(keyword[1])) {
				start = j + len(keyword)
			}
			continue
		}
		if clauseWord[t.Upper()] && t.Kind == sqltext.Word && !(t.Is("BY") && c.at(j-1).Is(keyword[0])) {
			return start, j
		}
	}
	if start < 0 {
		return -1, -1
	}
	return start, block[1]
}

var clauseWord = map[string]bool{
	"FROM": true, "WHERE": true, "GROUP": true, "HAVING": true, "ORDER": true, "LIMIT": true, "OFFSET": true,
	"FETCH": true, "WINDOW": true, "UNION": true, "INTERSECT": true, "EXCEPT": true, "INTO": true, "FOR": true,
	"RETURNING": true, "QUALIFY": true,
}

// items splits tokens s..e-1 into comma-separated items at depth d.
func (c *lintCtx) items(s, e, d int) [][2]int {
	var out [][2]int
	start := s
	for j := s; j <= e; j++ {
		if j == e || c.ts[j].IsOp(",") && c.depth[j] == d {
			if j-1 >= start {
				out = append(out, [2]int{start, j - 1})
			}
			start = j + 1
		}
	}
	return out
}

// selectItems returns the items of a query block's select list.
func (c *lintCtx) selectItems(block [2]int) [][2]int {
	s := block[0] + 1
	for s < block[1] && (c.ts[s].Is("DISTINCT") || c.ts[s].Is("ALL") || c.ts[s].Is("TOP") || c.ts[s].Is("SQL_CALC_FOUND_ROWS")) {
		s++
	}
	if c.at(s).IsOp("(") && c.at(s-1).Is("ON") { // DISTINCT ON (...)
		s = c.close(s) + 1
	}
	e := block[1]
	d := c.depth[block[0]]
	for j := s; j < block[1]; j++ {
		if c.depth[j] == d && c.ts[j].Kind == sqltext.Word && clauseWord[c.ts[j].Upper()] {
			e = j
			break
		}
	}
	return c.items(s, e, d)
}

// alias returns the alias an item gives itself (expr AS name, or expr
// name) and the end of the expression before it.
func (c *lintCtx) alias(item [2]int) (string, int, int) {
	s, e := item[0], item[1]
	if e > s && c.ts[e-1].Is("AS") && isName(c.ts[e]) {
		return identName(c.ts[e]), e, e - 2
	}
	if e > s && isName(c.ts[e]) && !c.ts[e-1].IsOp(".") {
		prev := c.ts[e-1]
		if prev.IsOp(")") || prev.Kind == sqltext.Number || prev.Kind == sqltext.String || isName(prev) && !boundaryWord[prev.Upper()] || prev.Is("END") {
			if c.ts[e].Kind == sqltext.Word && (boundaryWord[c.ts[e].Upper()] || c.ts[e].Is("END")) {
				return "", -1, e
			}
			return identName(c.ts[e]), e, e - 1
		}
	}
	return "", -1, e
}

// isAggregate reports whether tokens s..e call an aggregate function.
func (c *lintCtx) isAggregate(s, e int) bool {
	for k := s; k <= e; k++ {
		if c.isCall(k) && aggregates[c.ts[k].Upper()] {
			return true
		}
	}
	return false
}

var aggregates = map[string]bool{
	"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true, "GROUP_CONCAT": true, "STRING_AGG": true,
	"ARRAY_AGG": true, "JSON_AGG": true, "JSONB_AGG": true, "BOOL_AND": true, "BOOL_OR": true, "EVERY": true,
	"BIT_AND": true, "BIT_OR": true, "BIT_XOR": true, "STDDEV": true, "STDDEV_POP": true, "STDDEV_SAMP": true,
	"VARIANCE": true, "VAR_POP": true, "VAR_SAMP": true, "TOTAL": true, "JSON_ARRAYAGG": true,
	"JSON_OBJECTAGG": true, "JSON_GROUP_ARRAY": true, "JSON_GROUP_OBJECT": true, "LISTAGG": true,
	"PERCENTILE_CONT": true, "PERCENTILE_DISC": true, "MEDIAN": true, "ANY_VALUE": true,
}
