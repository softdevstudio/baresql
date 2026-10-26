package portable

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/softdevstudio/baresql/internal/sqltext"
)

// The rules, in catalog order. Each finds one construct; the text of every
// problem comes from the catalog entry with the same code, or from the
// entry's table row for functions, operators and types.

func init() {
	addRule("integer-division", ruleIntegerDivision)
	addRule("decimal-division", ruleDecimalDivision)
	addRule("pipes-concat", rulePipesConcat)
	addRule("concat-null", ruleConcatNull)
	addRule("text-compare-case", ruleTextCompareCase)
	addRule("like-case", ruleLikeCase)
	addRule("text-number-compare", ruleTextNumberCompare)
	addRule("double-quotes", ruleDoubleQuotes)
	addRule("null-order", ruleNullOrder)
	addRule("ungrouped-column", ruleUngroupedColumn)
	addRule("cast-to-text", ruleCastToText)
	addRule("cast-to-number", ruleCastToNumber)
	addRule("cast-to-date", ruleCastToDate)
	addRule("date-arithmetic", ruleDateArithmetic)
	addRule("division-by-zero", ruleDivisionByZero)
	addRule("decimal-as-float", ruleDecimalAsFloat)
	addRule("float-precision", ruleFloatPrecision)
	addRule("char-padding", ruleCharPadding)
	addRule("length-bytes", ruleLengthBytes)
	addRule("lower-non-ascii", ruleLowerNonASCII)
	addRule("substring-start", ruleSubstringStart)
	addRule("backslash-escapes", ruleBackslashEscapes)
	addRule("like-escape", ruleLikeEscape)
	addRule("log-base", ruleLogBase)
	addRule("caret-operator", ruleCaretOperator)
	addRule("dash-comment", func(*rule, *lintCtx) {}) // checked on the whole script
	addRule("column-name-case", ruleColumnNameCase)
	addRule("table-name-case", ruleTableNameCase)
	addRule("type-not-enforced", ruleTypeNotEnforced)
	addRule("boolean-as-number", ruleBooleanAsNumber)
	addRule("auto-increment", ruleAutoIncrement)
	addRule("backticks", ruleBackticks)
	addRule("hash-comment", func(*rule, *lintCtx) {}) // checked on the whole script
	addRule("nulls-first-last", ruleNullsFirstLast)
	addRule("limit-comma", ruleLimitComma)
	addRule("fetch-first", ruleFetchFirst)
	addRule("alias-in-where", ruleAliasInWhere)
	addRule("alias-in-having", ruleAliasInHaving)
	addRule("limit-in-subquery", ruleLimitInSubquery)
	addRule("update-same-table", ruleUpdateSameTable)
	addRule("update-join", ruleUpdateJoin)
	addRule("full-outer-join", ruleFullOuterJoin)
	addRule("distinct-on", ruleDistinctOn)
	addRule("count-distinct-columns", ruleCountDistinctColumns)
	addRule("greatest-least", ruleGreatestLeast)
	addRule("insert-ignore", ruleInsertIgnore)
	addRule("upsert", ruleUpsert)
	addRule("returning", ruleReturning)
	addRule("varchar-length", ruleVarcharLength)
	addRule("type-not-everywhere", ruleTypeNotEverywhere)
	addRule("table-options", ruleTableOptions)
	addRule("text-default", ruleTextDefault)
	addRule("index-on-text", ruleIndexOnText)
	addRule("index-if-not-exists", ruleIndexIfNotExists)
	addRule("truncate", ruleTruncate)
	addRule("create-or-replace", ruleCreateOrReplace)
	addRule("function-not-everywhere", ruleFunctionNotEverywhere)
	addRule("operator-not-everywhere", ruleOperatorNotEverywhere)
}

func ruleIntegerDivision(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.IsOp("/") || i == 0 || i+1 >= len(c.ts) {
			continue
		}
		// The left side is the chain of * / % factors before the /.
		s := c.primaryStart(i - 1)
		kinds := []vkind{c.kindOfPrimary(s, i-1)}
		for s >= 2 && c.ts[s-1].IsOp("*", "/", "%") {
			s2 := c.primaryStart(s - 2)
			kinds = append(kinds, c.kindOfPrimary(s2, s-2))
			s = s2
		}
		if combine(kinds) != kInt {
			continue
		}
		e := c.primaryEnd(i + 1)
		if c.kindOfPrimary(i+1, e) != kInt {
			continue
		}
		c.hit(r, s, e, "")
	}
}

func ruleDecimalDivision(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.IsOp("/") || i == 0 || i+1 >= len(c.ts) {
			continue
		}
		s := c.primaryStart(i - 1)
		kinds := []vkind{c.kindOfPrimary(s, i-1)}
		for s >= 2 && c.ts[s-1].IsOp("*", "/", "%") {
			s2 := c.primaryStart(s - 2)
			kinds = append(kinds, c.kindOfPrimary(s2, s-2))
			s = s2
		}
		e := c.primaryEnd(i + 1)
		lk, rk := combine(kinds), c.kindOfPrimary(i+1, e)
		if !(lk == kDecimal && (rk == kInt || rk == kDecimal) || lk == kInt && rk == kDecimal) {
			continue
		}
		// ROUND(a / b, n) gives the same digits everywhere.
		if c.at(s-1).IsOp("(") && c.isCall(s-2) && c.ts[s-2].Is("ROUND") && c.at(e+1).IsOp(",") {
			continue
		}
		c.hit(r, s, e, "")
	}
}

func rulePipesConcat(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.IsOp("||") || i == 0 {
			continue
		}
		s := c.primaryStart(i - 1)
		if c.at(s - 1).IsOp("||") {
			continue // a chain a || b || c is one problem
		}
		e := c.primaryEnd(i + 1)
		for c.at(e + 1).IsOp("||") {
			e = c.primaryEnd(e + 2)
		}
		c.hit(r, s, e, "")
	}
}

func ruleConcatNull(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.Is("CONCAT") || !c.isCall(i) {
			continue
		}
		sev := ""
		for _, a := range c.args(i + 1) {
			s, e := a[0], a[1]
			switch k := c.kindOf(s, e); {
			case k == kNull:
				sev = "warning"
			case s == e && (c.ts[s].Kind == sqltext.String || c.ts[s].Kind == sqltext.Number):
			case c.isCall(s) && (c.ts[s].Is("COALESCE") || c.ts[s].Is("IFNULL")):
			default:
				if ci, ok := c.column(s, e); ok {
					if !ci.notNull {
						sev = "warning"
					}
				} else if sev == "" {
					sev = "info"
				}
			}
		}
		if sev != "" {
			c.hit(r, i, c.close(i+1), sev)
		}
	}
}

// folded reports whether s..e is one LOWER() or UPPER() call.
func (c *lintCtx) folded(s, e int) bool {
	return c.isCall(s) && (c.ts[s].Is("LOWER") || c.ts[s].Is("UPPER")) && c.close(s+1) == e
}

func (c *lintCtx) letterLiteral(s, e int) bool {
	return s == e && isPlainString(c.ts[s]) && hasLetter(c.ts[s].StringValue())
}

func ruleTextCompareCase(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.IsOp("=", "<>", "!=", "==") || i == 0 || c.assignment(i) {
			continue
		}
		ls, le := c.exprBefore(i-1), i-1
		rs, re := i+1, c.exprAfter(i+1)
		if rs > re {
			continue
		}
		lk, rk := c.kindOf(ls, le), c.kindOf(rs, re)
		switch {
		case c.folded(ls, le) || c.folded(rs, re):
		case lk.numeric() || rk.numeric() || lk == kDate || rk == kDate:
		case c.letterLiteral(ls, le) || c.letterLiteral(rs, re):
			c.hit(r, ls, re, "")
		case lk == kText && rk == kText && ls == le-0 && !isPlainString(c.ts[ls]) && !isPlainString(c.ts[rs]):
			c.hit(r, ls, re, "")
		}
	}
	for i, t := range c.ts {
		if !t.Is("IN") || !c.at(i+1).IsOp("(") || c.at(i+2).Is("SELECT") || i == 0 {
			continue
		}
		ls := c.exprBefore(i - 1)
		if c.at(i - 1).Is("NOT") {
			ls = c.exprBefore(i - 2)
		}
		if c.folded(ls, i-1) {
			continue
		}
		for _, a := range c.args(i + 1) {
			if c.letterLiteral(a[0], a[1]) {
				c.hit(r, ls, c.close(i+1), "")
				break
			}
		}
	}
}

func ruleLikeCase(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.Is("LIKE") || i == 0 {
			continue
		}
		le := i - 1
		if c.ts[le].Is("NOT") {
			le--
		}
		ls := c.exprBefore(le)
		pe := c.exprAfter(i + 1)
		if c.folded(ls, le) {
			continue
		}
		if p := c.at(i + 1); i+1 == pe && p.Kind == sqltext.String && !hasLetter(p.StringValue()) {
			continue
		}
		c.hit(r, ls, pe, "")
	}
}

var comparisonOps = []string{"=", "<>", "!=", "<", ">", "<=", ">=", "=="}

func ruleTextNumberCompare(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.IsOp(comparisonOps...) || i == 0 || c.assignment(i) {
			continue
		}
		ls, le := c.exprBefore(i-1), i-1
		rs, re := i+1, c.exprAfter(i+1)
		if rs > re {
			continue
		}
		form := c.textNumberMix(ls, le, rs, re)
		if form == "" {
			form = c.textNumberMix(rs, re, ls, le)
		}
		if form != "" {
			c.hitForm(r, ls, re, form, "", "")
		}
	}
}

// textNumberMix says how a is text and b a number in a way the engines
// read differently: the catalog row's name, or "".
func (c *lintCtx) textNumberMix(as, ae, bs, be int) string {
	aLit := as == ae && isPlainString(c.ts[as])
	bNum := bs == be && c.ts[bs].Kind == sqltext.Number || be == bs+1 && c.ts[bs].IsOp("-") && c.ts[be].Kind == sqltext.Number
	switch {
	case aLit && bNum:
		return "'1' = 1"
	case aLit && !numericText(c.ts[as].StringValue()) && c.kindOf(bs, be).numeric():
		return "'abc' = number column"
	case !aLit && c.kindOf(as, ae) == kText && bNum:
		return "text column = number"
	}
	return ""
}

func ruleDoubleQuotes(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if t.Kind == sqltext.QuotedIdent && strings.HasPrefix(t.Text, `"`) {
			c.hit(r, i, i, "")
			return // once per statement: schemas that quote every name would repeat it endlessly
		}
	}
}

func ruleNullOrder(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.Is("ORDER") || !c.at(i+1).Is("BY") {
			continue
		}
		d := c.depth[i]
		s := i + 2
		e := s
		for e < len(c.ts) {
			u := c.ts[e]
			if c.depth[e] < d || c.depth[e] == d && u.Kind == sqltext.Word && (clauseWord[u.Upper()] || u.Is("ROWS") || u.Is("RANGE") || u.Is("GROUPS") || u.Is("SEPARATOR")) {
				break
			}
			e++
		}
		var tested []string
		for _, k := range c.items(s, e, d) {
			ks, ke := k[0], k[1]
			nulls := false
			for ke > ks && (c.ts[ke].Is("ASC") || c.ts[ke].Is("DESC") || c.ts[ke].Is("FIRST") || c.ts[ke].Is("LAST") || c.ts[ke].Is("NULLS")) {
				if !c.ts[ke].Is("ASC") && !c.ts[ke].Is("DESC") {
					nulls = true
				}
				ke--
			}
			if ke > ks+1 && c.ts[ke-1].Is("COLLATE") {
				ke -= 2
			}
			text := strings.ToLower(c.span(ks, ke))
			switch {
			case nulls:
			case ke >= ks+2 && c.ts[ke].Is("NULL") && c.ts[ke-1].Is("IS"):
				tested = append(tested, strings.ToLower(c.span(ks, ke-2)))
			case ke >= ks+3 && c.ts[ke].Is("NULL") && c.ts[ke-1].Is("NOT") && c.ts[ke-2].Is("IS"):
				tested = append(tested, strings.ToLower(c.span(ks, ke-3)))
			case contains(tested, text):
			case ks == ke && c.ts[ks].Kind == sqltext.Number:
			case c.ts[ks].Is("CASE"):
			case c.isCall(ks) && c.ts[ks].Is("COUNT"):
			default:
				if ci, ok := c.column(ks, ke); ok && ci.notNull {
					continue
				}
				c.hit(r, ks, ke, "")
			}
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func ruleUngroupedColumn(r *rule, c *lintCtx) {
	for _, b := range c.selectBlocks() {
		gs, ge := c.clause(b, "GROUP", "BY")
		if gs < 0 {
			continue
		}
		d := c.depth[b[0]]
		items := c.selectItems(b)
		grouped := map[string]bool{}
		qualified := map[string]bool{} // "u.id" for GROUP BY u.id
		bare := map[string]bool{}      // "id" for GROUP BY id
		for _, g := range c.items(gs, ge, d) {
			gs, gee := g[0], g[1]
			for gee > gs && (c.ts[gee].Is("ASC") || c.ts[gee].Is("DESC")) {
				gee--
			}
			if gs == gee && c.ts[gs].Kind == sqltext.Number {
				if n, err := strconv.Atoi(c.ts[gs].Text); err == nil && n >= 1 && n <= len(items) {
					it := items[n-1]
					_, _, ie := c.alias(it)
					grouped[strings.ToLower(c.span(it[0], ie))] = true
					if isName(c.ts[ie]) {
						grouped[strings.ToLower(identName(c.ts[ie]))] = true
					}
				}
				continue
			}
			grouped[strings.ToLower(c.span(gs, gee))] = true
			if isName(c.ts[gee]) && (gs == gee || gee == gs+2 && c.ts[gs+1].IsOp(".")) {
				col := strings.ToLower(identName(c.ts[gee]))
				grouped[col] = true
				if gs == gee {
					bare[col] = true
				} else {
					qualified[strings.ToLower(identName(c.ts[gs]))+"."+col] = true
				}
			}
		}
		for _, it := range items {
			name, _, e := c.alias(it)
			s := it[0]
			if s > e || grouped[strings.ToLower(c.span(s, e))] || name != "" && grouped[strings.ToLower(name)] || c.isAggregate(s, e) {
				continue
			}
			ref := s == e && isName(c.ts[s]) || e == s+2 && c.ts[s+1].IsOp(".") && isName(c.ts[e])
			star := s == e && c.ts[s].IsOp("*") || e == s+2 && c.ts[e].IsOp("*")
			if !ref && !star {
				continue
			}
			if ref {
				col := strings.ToLower(identName(c.ts[e]))
				if grouped[col] || c.ts[e].Kind == sqltext.Word && (boundaryWord[c.ts[e].Upper()] || c.ts[e].Is("NULL") || c.ts[e].Is("TRUE") || c.ts[e].Is("FALSE") || strings.HasPrefix(c.ts[e].Upper(), "CURRENT_")) {
					continue
				}
				if c.groupedByKey(s, e, bare, qualified) {
					continue
				}
			}
			c.hit(r, s, e, "")
		}
	}
}

// groupedByKey: the column's table has a primary key whose columns are all
// grouped, which fixes every other column of that table. A key column
// counts when grouped bare (GROUP BY id) or under a name of the same table
// (GROUP BY u.id, with u an alias of the table).
func (c *lintCtx) groupedByKey(s, e int, bare, qualified map[string]bool) bool {
	if c.schema == nil || c.schema.pk == nil {
		return false
	}
	var table string
	col := strings.ToLower(identName(c.ts[e]))
	if e == s+2 {
		table = strings.ToLower(identName(c.ts[s]))
		if t, ok := c.aliases[table]; ok {
			table = strings.ToLower(t)
		}
	} else {
		for _, t := range c.tables {
			if _, ok := c.schema.cols[strings.ToLower(t)][col]; ok {
				table = strings.ToLower(t)
				break
			}
		}
	}
	pk := c.schema.pk[table]
	if len(pk) == 0 {
		return false
	}
	names := []string{table}
	for a, t := range c.aliases {
		if strings.EqualFold(t, table) {
			names = append(names, strings.ToLower(a))
		}
	}
	for _, k := range pk {
		ok := bare[k]
		for _, n := range names {
			ok = ok || qualified[n+"."+k]
		}
		if !ok {
			return false
		}
	}
	return true
}

// castTarget returns, for a CAST( at i, the type words after AS and
// whether a length or precision follows.
func (c *lintCtx) castTarget(i int) (string, int, bool) {
	if !c.ts[i].Is("CAST") || !c.at(i+1).IsOp("(") {
		return "", -1, false
	}
	open := i + 1
	end := c.close(open)
	for k := end - 1; k > open; k-- {
		if c.ts[k].Is("AS") && c.depth[k] == c.depth[open]+1 {
			typ := c.at(k + 1).Upper()
			j := k + 2
			for j < end && c.ts[j].Kind == sqltext.Word && typeContinuation(typ, c.ts[j].Upper()) {
				typ += " " + c.ts[j].Upper()
				j++
			}
			return typ, k, c.at(j).IsOp("(")
		}
	}
	return "", -1, false
}

func ruleCastToText(r *rule, c *lintCtx) {
	for i := range c.ts {
		typ, _, sized := c.castTarget(i)
		end := c.close(i + 1)
		switch typ {
		case "CHAR", "CHARACTER", "NCHAR":
			form := "CHAR"
			if sized {
				form = "CHAR(n)"
			}
			c.hitForm(r, i, end, form, "", "")
		case "VARCHAR", "CHARACTER VARYING":
			c.hitForm(r, i, end, "VARCHAR", "CAST(x AS "+typ+")", "")
		case "TEXT":
			c.hitForm(r, i, end, "TEXT", "CAST(x AS TEXT)", "")
		}
	}
}

func ruleCastToNumber(r *rule, c *lintCtx) {
	for i := range c.ts {
		typ, as, sized := c.castTarget(i)
		end := c.close(i + 1)
		switch {
		case typ == "INTEGER", typ == "INT":
			c.hitForm(r, i, end, typ, typ, "")
		case typ == "BIGINT", typ == "INT2", typ == "INT4", typ == "INT8":
			c.hitForm(r, i, end, "BIGINT", "CAST(x AS "+typ+")", "")
		case typ == "SMALLINT", typ == "TINYINT":
			c.hitForm(r, i, end, typ, "CAST(x AS "+typ+")", "")
		case strings.HasPrefix(typ, "SIGNED"):
			c.hitForm(r, i, end, "SIGNED", typ, "")
		case strings.HasPrefix(typ, "UNSIGNED"):
			c.hitForm(r, i, end, "UNSIGNED", typ, "")
		case typ == "NUMERIC":
			c.hitForm(r, i, end, "NUMERIC", "CAST(x AS NUMERIC)", "")
		case typ == "DECIMAL", typ == "DEC":
			if !sized {
				c.hitForm(r, i, end, "DECIMAL", typ, "")
			} else if args := c.args(as + 2); len(args) == 2 {
				c.hitForm(r, i, end, "DECIMAL(p, s)", typ, "")
			}
		}
	}
}

func ruleCastToDate(r *rule, c *lintCtx) {
	for i := range c.ts {
		typ, _, _ := c.castTarget(i)
		end := c.close(i + 1)
		switch {
		case typ == "DATE", typ == "TIME":
			c.hitForm(r, i, end, typ, "CAST(x AS "+typ+")", "")
		case typ == "DATETIME":
			c.hitForm(r, i, end, "DATETIME", "", "")
		case strings.HasPrefix(typ, "TIMESTAMP"):
			c.hitForm(r, i, end, "TIMESTAMP", "", "")
		}
	}
}

// unary reports whether the + or - at i is a sign rather than an operator.
func (c *lintCtx) unary(i int) bool {
	if i == 0 {
		return true
	}
	p := c.ts[i-1]
	switch {
	case p.IsOp(")"):
		return false
	case p.Kind == sqltext.Operator, p.IsOp("(", ","):
		return true
	case p.Kind == sqltext.Word && (boundaryWord[p.Upper()] || p.Is("RETURN")):
		return true
	}
	return false
}

func ruleDateArithmetic(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.IsOp("+", "-") || c.unary(i) || i+1 >= len(c.ts) {
			continue
		}
		ls := c.primaryStart(i - 1)
		re := c.primaryEnd(i + 1)
		lk, rk := c.kindOfPrimary(ls, i-1), c.kindOfPrimary(i+1, re)
		switch {
		case lk == kInterval || rk == kInterval:
			c.hitForm(r, ls, re, "INTERVAL", "", "")
		case lk == kDate && (rk.numeric() || rk == kDate), rk == kDate && lk.numeric():
			c.hitForm(r, ls, re, "date + number", "", "")
		}
	}
}

func ruleDivisionByZero(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.IsOp("/", "%") || i == 0 || i+1 >= len(c.ts) {
			continue
		}
		re := c.primaryEnd(i + 1)
		d := c.ts[i+1]
		switch {
		case i+1 == re && d.Kind == sqltext.Number:
			if f, err := strconv.ParseFloat(d.Text, 64); err == nil && f == 0 {
				c.hit(r, c.primaryStart(i-1), re, "error")
			}
		case c.isCall(i+1) && d.Is("NULLIF"):
		case d.IsOp("(") && c.isCall(i+2) && c.at(i+2).Is("NULLIF") && c.close(i+3) == re-1:
		default:
			c.hit(r, c.primaryStart(i-1), re, "info")
		}
	}
}

func ruleDecimalAsFloat(r *rule, c *lintCtx) {
	for _, d := range c.columnDefs() {
		if d.typ >= 0 && (d.typeName == "DECIMAL" || d.typeName == "NUMERIC" || d.typeName == "DEC") {
			c.hitMsg(r, d.typ, d.typEnd, "info", "SQLite stores "+d.typeName+" values as floating point numbers, so sums and comparisons can be off in the last digits.", "")
		}
	}
	for i, t := range c.ts {
		if !t.IsOp("=", "<>", "!=", "==") || i == 0 {
			continue
		}
		ls, rs := c.exprBefore(i-1), i+1
		re := c.exprAfter(rs)
		lk, rk := c.kindOf(ls, i-1), c.kindOf(rs, re)
		if lk != kDecimal && rk != kDecimal || c.rounded(ls, i-1) || c.rounded(rs, re) {
			continue
		}
		if c.computed(ls, i-1) || c.computed(rs, re) {
			c.hit(r, ls, re, "")
		}
	}
}

// rounded: tokens s..e are one ROUND() call.
func (c *lintCtx) rounded(s, e int) bool {
	return c.isCall(s) && c.ts[s].Is("ROUND") && c.close(s+1) == e
}

// computed: tokens s..e hold arithmetic or a SUM or AVG.
func (c *lintCtx) computed(s, e int) bool {
	for k := s; k <= e; k++ {
		if c.ts[k].IsOp("+", "-", "*", "/") && !c.unary(k) || c.isCall(k) && (c.ts[k].Is("SUM") || c.ts[k].Is("AVG") || c.ts[k].Is("TOTAL")) {
			return true
		}
	}
	return false
}

func ruleFloatPrecision(r *rule, c *lintCtx) {
	for _, d := range c.columnDefs() {
		if d.typ < 0 {
			continue
		}
		switch d.typeName {
		case "REAL", "FLOAT4":
			c.hitForm(r, d.typ, d.typEnd, "REAL", "", "")
		case "FLOAT":
			if d.typEnd > d.typ {
				if n, err := strconv.Atoi(c.at(d.typ + 2).Text); err == nil && n > 24 {
					continue
				}
			}
			c.hitForm(r, d.typ, d.typEnd, "FLOAT", "", "")
		}
	}
}

func ruleCharPadding(r *rule, c *lintCtx) {
	for _, d := range c.columnDefs() {
		if d.typ >= 0 && (d.typeName == "CHAR" || d.typeName == "CHARACTER" || d.typeName == "NCHAR") {
			c.hit(r, d.typ, d.typEnd, "")
		}
	}
}

func ruleLengthBytes(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.Is("LENGTH") || !c.isCall(i) {
			continue
		}
		a := c.args(i + 1)
		if len(a) != 1 {
			continue
		}
		if s, e := a[0][0], a[0][1]; s == e && c.ts[s].Kind == sqltext.String {
			if nonASCII(c.ts[s].StringValue()) {
				c.hit(r, i, c.close(i+1), "warning")
			}
			continue
		}
		c.hit(r, i, c.close(i+1), "info")
	}
}

func ruleLowerNonASCII(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !(t.Is("LOWER") || t.Is("UPPER")) || !c.isCall(i) {
			continue
		}
		a := c.args(i + 1)
		if len(a) != 1 {
			continue
		}
		if s, e := a[0][0], a[0][1]; s == e && c.ts[s].Kind == sqltext.String {
			if nonASCII(c.ts[s].StringValue()) {
				c.hit(r, i, c.close(i+1), "warning")
			}
			continue
		}
		c.hit(r, i, c.close(i+1), "info")
	}
}

func ruleSubstringStart(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !(t.Is("SUBSTR") || t.Is("SUBSTRING")) || !c.isCall(i) {
			continue
		}
		start := -1
		if a := c.args(i + 1); len(a) >= 2 {
			start = a[1][0]
		} else {
			end := c.close(i + 1)
			for k := i + 2; k < end; k++ {
				if c.ts[k].Is("FROM") && c.depth[k] == c.depth[i+1]+1 {
					start = k + 1
					break
				}
			}
		}
		if start < 0 {
			continue
		}
		s := c.at(start)
		if s.Kind == sqltext.Number && s.Text == "0" || s.IsOp("-") && c.at(start+1).Kind == sqltext.Number {
			c.hit(r, i, c.close(i+1), "")
		}
	}
}

// mysqlQuote reports whether a '...' literal holds a backslash-escaped
// quote, which only MySQL and MariaDB read.
func mysqlQuote(text string) bool {
	body := text[1 : len(text)-1]
	for k := 0; k+1 < len(body); k++ {
		if body[k] == '\\' {
			if body[k+1] == '\'' && !(k+2 < len(body) && body[k+2] == '\'') {
				return true
			}
			k++
		}
	}
	return false
}

func ruleBackslashEscapes(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if t.Kind != sqltext.String {
			continue
		}
		if strings.HasPrefix(t.Text, "E'") || strings.HasPrefix(t.Text, "e'") {
			c.hitForm(r, i, i, "E'...'", "E'...'", "")
			continue
		}
		if !strings.HasPrefix(t.Text, "'") || !strings.Contains(t.Text, `\`) || c.at(i-1).Is("LIKE") || c.at(i-1).Is("ESCAPE") {
			continue
		}
		if mysqlQuote(t.Text) {
			c.hitForm(r, i, i, `\'`, "", "")
			continue
		}
		c.hitForm(r, i, i, "backslash", "", "")
	}
}

func ruleLikeEscape(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.Is("LIKE") {
			continue
		}
		p := c.at(i + 1)
		if p.Kind != sqltext.String || !strings.Contains(p.Text, `\`) || c.at(i+2).Is("ESCAPE") {
			continue
		}
		c.hit(r, i, i+1, "")
	}
}

func ruleLogBase(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if t.Is("LOG") && c.isCall(i) && len(c.args(i+1)) == 1 {
			c.hit(r, i, c.close(i+1), "")
		}
	}
}

func ruleCaretOperator(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if t.IsOp("^") && i > 0 {
			c.hit(r, c.primaryStart(i-1), c.primaryEnd(i+1), "")
		}
	}
}

func ruleColumnNameCase(r *rule, c *lintCtx) {
	upper := func(i int) bool { return c.ts[i].Kind == sqltext.Word && hasUpper(c.ts[i].Text) }
	for _, b := range c.selectBlocks() {
		if c.depth[b[0]] != 0 {
			continue
		}
		for _, it := range c.selectItems(b) {
			if name, at, _ := c.alias(it); name != "" && upper(at) {
				c.hitMsg(r, at, at, "", "PostgreSQL returns the column "+name+" as "+strings.ToLower(name)+"; SQLite, MySQL and MariaDB return "+name+".", "")
			}
		}
	}
	if defs, open, _ := c.createTable(); open > 0 {
		for _, d := range defs {
			if !d.constraint && upper(d.name) {
				name := c.ts[d.name].Text
				c.hitMsg(r, d.name, d.name, "", "PostgreSQL names this column "+strings.ToLower(name)+"; SQLite, MySQL and MariaDB keep "+name+".", "")
			}
		}
	}
}

func ruleTableNameCase(r *rule, c *lintCtx) {
	if c.schema == nil || c.schema.names == nil {
		return
	}
	for i, t := range c.ts {
		if !(t.Is("FROM") || t.Is("JOIN") || t.Is("UPDATE") || t.Is("INTO") || t.Is("TABLE") || t.Is("ON") && c.at(i+2).IsOp("(")) || c.at(i+1).Kind != sqltext.Word {
			continue
		}
		j := i + 1
		for c.at(j+1).IsOp(".") && isName(c.at(j+2)) {
			j += 2
		}
		name := identName(c.ts[j])
		defined, ok := c.schema.names[strings.ToLower(name)]
		if ok && defined != name {
			c.hitMsg(r, j, j, "", "The table is created as "+defined+" and named "+name+" here: MySQL and MariaDB on Linux find no table "+name+".", "")
		}
	}
}

func ruleTypeNotEnforced(r *rule, c *lintCtx) {
	if c.schema == nil || len(c.ts) < 4 || !c.ts[0].Is("INSERT") {
		return
	}
	i := 1
	for i < len(c.ts) && !c.ts[i].Is("INTO") {
		i++
	}
	if i+1 >= len(c.ts) {
		return
	}
	j := i + 1
	for c.at(j + 1).IsOp(".") {
		j += 2
	}
	table := strings.ToLower(identName(c.at(j)))
	cols := c.schema.order[table]
	if cols == nil {
		return
	}
	k := j + 1
	if c.at(k).IsOp("(") {
		cols = nil
		for _, a := range c.args(k) {
			cols = append(cols, strings.ToLower(identName(c.ts[a[0]])))
		}
		k = c.close(k) + 1
	}
	if !c.at(k).Is("VALUES") {
		return
	}
	for k++; k < len(c.ts) && c.ts[k].IsOp("("); {
		for n, a := range c.args(k) {
			if n >= len(cols) || a[0] != a[1] || !isPlainString(c.ts[a[0]]) {
				continue
			}
			ci := c.schema.cols[table][cols[n]]
			v := c.ts[a[0]].StringValue()
			switch kind := typeKind(ci.typ); {
			case kind.numeric() && !numericText(v):
				c.hitForm(r, a[0], a[0], "text in a number column", "",
					"SQLite stores the text "+c.ts[a[0]].Text+" in the "+ci.typ+" column "+cols[n]+"; PostgreSQL, MySQL and MariaDB refuse it.")
			case kind == kInt && strings.ContainsAny(v, ".eE"):
				c.hitForm(r, a[0], a[0], "a fraction in an integer column", "",
					"PostgreSQL refuses the text "+c.ts[a[0]].Text+" for the "+ci.typ+" column "+cols[n]+"; MySQL and MariaDB round it to a whole number, and SQLite keeps a fraction as it is.")
			case kind == kText && ci.length > 0 && len([]rune(v)) > ci.length:
				c.hitForm(r, a[0], a[0], "too long for VARCHAR(n)", "",
					fmt.Sprintf("The value is %d characters long and %s holds %d: SQLite stores it whole, the other engines refuse it.", len([]rune(v)), cols[n], ci.length))
			}
		}
		k = c.close(k) + 1
		if !c.at(k).IsOp(",") {
			break
		}
		k++
	}
}

func ruleBooleanAsNumber(r *rule, c *lintCtx) {
	isNum := func(i int) bool { return c.at(i).Kind == sqltext.Number }
	for i, t := range c.ts {
		if t.IsOp("=", "<>", "!=") && i > 0 {
			ls, rs := c.primaryStart(i-1), i+1
			re := c.primaryEnd(rs)
			lk, rk := c.kindOfPrimary(ls, i-1), c.kindOfPrimary(rs, re)
			switch {
			case c.assignment(i):
				if lk == kBool && rs == re && isNum(rs) {
					c.hitForm(r, rs, rs, "number into BOOLEAN", "", "PostgreSQL refuses a number for the BOOLEAN column "+identName(c.ts[i-1])+".")
				}
			case lk == kBool && rs == re && isNum(rs) || rk == kBool && ls == i-1 && isNum(ls):
				c.hitForm(r, ls, re, "boolean = number", "", "PostgreSQL has no = between a boolean and a number.")
			}
		}
		// A bare number or integer column where a condition goes.
		if t.Is("WHERE") || t.Is("AND") || t.Is("OR") || t.Is("HAVING") || t.Is("ON") || t.Is("NOT") || t.Is("WHEN") && c.searchedCase(i) {
			e := c.primaryEnd(i + 1)
			if i+1 >= len(c.ts) || e >= len(c.ts) {
				continue
			}
			next := c.at(e + 1)
			ends := e+1 >= len(c.ts) || next.IsOp(")") || next.Kind == sqltext.Word && (next.Is("AND") || next.Is("OR") || next.Is("THEN") || clauseWord[next.Upper()])
			if !ends {
				continue
			}
			if e == i+1 && isNum(i+1) || c.kindOfPrimary(i+1, e) == kInt && c.ts[i+1].Kind == sqltext.Word && !c.isCall(i+1) {
				c.hitForm(r, i+1, e, "WHERE number", "", "PostgreSQL wants a boolean after "+t.Upper()+" and refuses a number.")
			}
		}
	}
	// INSERT ... VALUES with 0 or 1 for a BOOLEAN column.
	if c.schema == nil || len(c.ts) < 4 || !c.ts[0].Is("INSERT") {
		return
	}
	i := 1
	for i < len(c.ts) && !c.ts[i].Is("INTO") {
		i++
	}
	j := i + 1
	for c.at(j + 1).IsOp(".") {
		j += 2
	}
	table := strings.ToLower(identName(c.at(j)))
	cols := c.schema.order[table]
	k := j + 1
	if c.at(k).IsOp("(") {
		cols = nil
		for _, a := range c.args(k) {
			cols = append(cols, strings.ToLower(identName(c.ts[a[0]])))
		}
		k = c.close(k) + 1
	}
	if !c.at(k).Is("VALUES") {
		return
	}
	for k++; k < len(c.ts) && c.ts[k].IsOp("("); {
		for n, a := range c.args(k) {
			if n < len(cols) && a[0] == a[1] && isNum(a[0]) && typeKind(c.schema.cols[table][cols[n]].typ) == kBool {
				c.hitForm(r, a[0], a[0], "number into BOOLEAN", "", "PostgreSQL refuses a number for the BOOLEAN column "+cols[n]+".")
			}
		}
		k = c.close(k) + 1
		if !c.at(k).IsOp(",") {
			break
		}
		k++
	}
}

// searchedCase reports whether the WHEN at i belongs to CASE WHEN (a
// condition) rather than CASE x WHEN (a value).
func (c *lintCtx) searchedCase(i int) bool {
	depth := 0
	for j := i - 1; j >= 0; j-- {
		switch {
		case c.ts[j].Is("END"):
			depth++
		case c.ts[j].Is("CASE"):
			if depth == 0 {
				return c.at(j + 1).Is("WHEN")
			}
			depth--
		}
	}
	return false
}

func ruleAutoIncrement(r *rule, c *lintCtx) {
	for _, d := range c.columnDefs() {
		if d.typ < 0 {
			continue
		}
		switch d.typeName {
		case "SERIAL":
			c.hitForm(r, d.typ, d.typ, "SERIAL", "", "")
		case "BIGSERIAL", "SMALLSERIAL", "SERIAL8", "SERIAL4", "SERIAL2":
			c.hitForm(r, d.typ, d.typ, "BIGSERIAL", d.typeName, "")
		}
		for k := d.typEnd + 1; k <= d.end; k++ {
			switch {
			case c.ts[k].Is("AUTO_INCREMENT"):
				c.hitForm(r, k, k, "AUTO_INCREMENT", "AUTO_INCREMENT", "")
			case c.ts[k].Is("AUTOINCREMENT"):
				c.hitForm(r, k, k, "AUTOINCREMENT", "AUTOINCREMENT", "")
			case c.ts[k].Is("GENERATED") && c.has(k, d.end, "IDENTITY"):
				c.hitForm(r, k, d.end, "GENERATED AS IDENTITY", "GENERATED ... AS IDENTITY", "")
			case c.ts[k].Is("IDENTITY"):
				if !c.has(d.typEnd+1, k, "GENERATED") {
					c.hitForm(r, k, k, "IDENTITY(1, 1)", "", "")
				}
			}
		}
	}
}

func ruleBackticks(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if t.Kind == sqltext.QuotedIdent && strings.HasPrefix(t.Text, "`") {
			c.hit(r, i, i, "")
			return
		}
	}
}

func ruleNullsFirstLast(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if t.Is("NULLS") && (c.at(i+1).Is("FIRST") || c.at(i+1).Is("LAST")) {
			c.hit(r, i, i+1, "")
		}
	}
}

func ruleLimitComma(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !t.Is("LIMIT") {
			continue
		}
		e := c.primaryEnd(i + 1)
		if c.at(e+1).IsOp(",") && c.depth[e+1] == c.depth[i] {
			c.hit(r, i, c.primaryEnd(e+2), "")
		}
	}
}

func ruleFetchFirst(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		switch {
		case t.Is("FETCH") && (c.at(i+1).Is("FIRST") || c.at(i+1).Is("NEXT")):
			e := i + 1
			for e+1 < len(c.ts) && !c.ts[e].Is("ONLY") && !c.ts[e].Is("TIES") {
				e++
			}
			c.hitForm(r, i, e, "FETCH FIRST", "FETCH "+c.at(i+1).Upper(), "")
		case t.Is("OFFSET") && (c.at(i+2).Is("ROWS") || c.at(i+2).Is("ROW")):
			c.hitForm(r, i, i+2, "OFFSET n ROWS", "OFFSET n ROWS", "")
		}
	}
}

// selectAliases returns the aliases a query block defines that do not
// name a column.
func (c *lintCtx) selectAliases(b [2]int) map[string]bool {
	out := map[string]bool{}
	for _, it := range c.selectItems(b) {
		name, at, e := c.alias(it)
		if name == "" || at < 0 {
			continue
		}
		low := strings.ToLower(name)
		// x AS x, t.x AS x: the alias is the column's own name.
		if isName(c.ts[e]) && strings.EqualFold(identName(c.ts[e]), name) {
			continue
		}
		if _, ok := c.column(at, at); ok {
			continue
		}
		out[low] = true
	}
	return out
}

func (c *lintCtx) aliasUse(r *rule, b [2]int, keyword ...string) {
	s, e := c.clause(b, keyword...)
	if s < 0 {
		return
	}
	aliases := c.selectAliases(b)
	if len(aliases) == 0 {
		return
	}
	known := c.schema != nil && c.schema.cols != nil && len(c.tables) > 0
	for k := s; k < e; k++ {
		t := c.ts[k]
		if !isName(t) || c.at(k-1).IsOp(".") || c.at(k+1).IsOp("(", ".") || !aliases[strings.ToLower(identName(t))] {
			continue
		}
		sev := ""
		if !known {
			sev = "warning"
		}
		c.hit(r, k, k, sev)
		return
	}
}

func ruleAliasInWhere(r *rule, c *lintCtx) {
	for _, b := range c.selectBlocks() {
		c.aliasUse(r, b, "WHERE")
	}
}

func ruleAliasInHaving(r *rule, c *lintCtx) {
	for _, b := range c.selectBlocks() {
		c.aliasUse(r, b, "HAVING")
	}
}

func ruleLimitInSubquery(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if !(t.Is("IN") || t.Is("ANY") || t.Is("ALL") || t.Is("SOME")) || !c.at(i+1).IsOp("(") || !c.at(i+2).Is("SELECT") {
			continue
		}
		open := i + 1
		end := c.close(open)
		for k := open + 1; k < end; k++ {
			if c.ts[k].Is("LIMIT") && c.depth[k] == c.depth[open]+1 {
				c.hit(r, k, c.primaryEnd(k+1), "")
				break
			}
		}
	}
}

func ruleUpdateSameTable(r *rule, c *lintCtx) {
	var target string
	switch {
	case len(c.ts) > 2 && c.ts[0].Is("UPDATE"):
		j := 1
		for c.at(j).Is("LOW_PRIORITY") || c.at(j).Is("IGNORE") || c.at(j).Is("ONLY") {
			j++
		}
		target = identName(c.at(j))
	case len(c.ts) > 3 && c.ts[0].Is("DELETE") && c.ts[1].Is("FROM"):
		target = identName(c.at(2))
	default:
		return
	}
	for k, t := range c.ts {
		if c.depth[k] == 0 || !(t.Is("FROM") || t.Is("JOIN")) || !isName(c.at(k+1)) || !strings.EqualFold(identName(c.at(k+1)), target) {
			continue
		}
		if c.insideDerived(k) {
			continue
		}
		c.hit(r, k, k+1, "")
		return
	}
}

// insideDerived reports whether token k sits in a subquery used as a
// table (FROM (SELECT ...) AS s), which MySQL builds before the outer
// statement runs.
func (c *lintCtx) insideDerived(k int) bool {
	for j := k; j >= 0; j-- {
		if c.ts[j].IsOp("(") && c.pair[j] > k {
			if p := c.at(j - 1); p.Is("FROM") || p.Is("JOIN") || p.IsOp(",") && c.depth[j] > 0 {
				return true
			}
		}
	}
	return false
}

func ruleUpdateJoin(r *rule, c *lintCtx) {
	if len(c.ts) < 3 {
		return
	}
	switch {
	case c.ts[0].Is("UPDATE"):
		for k := 1; k < len(c.ts) && !c.ts[k].Is("SET"); k++ {
			if c.depth[k] == 0 && (c.ts[k].Is("JOIN") || c.ts[k].IsOp(",")) {
				what := "UPDATE ... JOIN"
				if c.ts[k].IsOp(",") {
					what = "UPDATE t, u"
				}
				c.hitForm(r, 0, k, "UPDATE ... JOIN", what, "")
				return
			}
		}
		set := -1
		for k, t := range c.ts {
			if t.Is("SET") && c.depth[k] == 0 && set < 0 {
				set = k
			}
			if set > 0 && t.Is("FROM") && c.depth[k] == 0 {
				c.hitForm(r, k, k, "UPDATE ... FROM", "UPDATE ... FROM", "")
				return
			}
		}
	case c.ts[0].Is("DELETE"):
		if !c.ts[1].Is("FROM") {
			c.hitForm(r, 0, 1, "DELETE ... JOIN", "DELETE t FROM t JOIN ...", "")
			return
		}
		for k, t := range c.ts {
			if t.Is("USING") && c.depth[k] == 0 {
				c.hitForm(r, k, k, "DELETE ... USING", "", "")
				return
			}
		}
	}
}

func ruleFullOuterJoin(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if t.Is("FULL") && (c.at(i+1).Is("JOIN") || c.at(i+1).Is("OUTER") && c.at(i+2).Is("JOIN")) {
			c.hit(r, i, i+1, "")
		}
	}
}

func ruleDistinctOn(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if t.Is("DISTINCT") && c.at(i+1).Is("ON") && c.at(i+2).IsOp("(") {
			c.hit(r, i, c.close(i+2), "")
		}
	}
}

func ruleCountDistinctColumns(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if t.Is("COUNT") && c.isCall(i) && c.at(i+2).Is("DISTINCT") && len(c.args(i+1)) > 1 {
			c.hit(r, i, c.close(i+1), "")
		}
	}
}

func ruleGreatestLeast(r *rule, c *lintCtx) {
	for i, t := range c.ts {
		if (t.Is("GREATEST") || t.Is("LEAST")) && c.isCall(i) {
			c.hit(r, i, c.close(i+1), "")
		}
	}
}

func ruleInsertIgnore(r *rule, c *lintCtx) {
	if len(c.ts) < 2 {
		return
	}
	if c.ts[0].Is("INSERT") && c.ts[1].Is("IGNORE") {
		c.hitForm(r, 0, 1, "INSERT IGNORE", "INSERT IGNORE", "")
	}
	if c.ts[0].Is("INSERT") && c.ts[1].Is("OR") && c.at(2).Is("IGNORE") {
		c.hitForm(r, 0, 2, "INSERT OR IGNORE", "INSERT OR IGNORE", "")
	}
	for k, t := range c.ts {
		if t.Is("CONFLICT") && c.at(k-1).Is("ON") {
			for j := k; j < len(c.ts); j++ {
				if c.ts[j].Is("DO") && c.at(j+1).Is("NOTHING") {
					c.hitForm(r, k-1, j+1, "ON CONFLICT DO NOTHING", "ON CONFLICT ... DO NOTHING", "")
				}
			}
		}
	}
}

func ruleUpsert(r *rule, c *lintCtx) {
	if len(c.ts) < 2 {
		return
	}
	switch {
	case c.ts[0].Is("REPLACE") && c.ts[1].Is("INTO"):
		c.hitForm(r, 0, 1, "REPLACE INTO", "REPLACE INTO", "")
	case c.ts[0].Is("INSERT") && c.ts[1].Is("OR") && c.at(2).Is("REPLACE"):
		c.hitForm(r, 0, 2, "INSERT OR REPLACE", "INSERT OR REPLACE", "")
	case c.ts[0].Is("MERGE"):
		c.hitForm(r, 0, 1, "MERGE", "MERGE", "")
	}
	for k, t := range c.ts {
		if t.Is("CONFLICT") && c.at(k-1).Is("ON") {
			for j := k; j < len(c.ts); j++ {
				if c.ts[j].Is("DO") && c.at(j+1).Is("UPDATE") {
					c.hitForm(r, k-1, j+1, "ON CONFLICT DO UPDATE", "ON CONFLICT ... DO UPDATE", "")
				}
			}
		}
		if t.Is("DUPLICATE") && c.at(k-1).Is("ON") && c.at(k+1).Is("KEY") {
			c.hitForm(r, k-1, k+2, "ON DUPLICATE KEY UPDATE", "ON DUPLICATE KEY UPDATE", "")
		}
	}
}

func ruleReturning(r *rule, c *lintCtx) {
	first := c.ts[0]
	if !(first.Is("INSERT") || first.Is("UPDATE") || first.Is("DELETE") || first.Is("WITH") || first.Is("REPLACE")) {
		return
	}
	// The statement's own verb: after WITH, the first one outside the CTEs.
	verb := first.Upper()
	if first.Is("WITH") {
		verb = "INSERT"
		for k, t := range c.ts {
			if c.depth[k] == 0 && (t.Is("INSERT") || t.Is("UPDATE") || t.Is("DELETE")) {
				verb = t.Upper()
				break
			}
		}
	}
	if verb == "REPLACE" {
		verb = "INSERT"
	}
	for k, t := range c.ts {
		if t.Is("RETURNING") && c.depth[k] == 0 {
			form := verb + " ... RETURNING"
			c.hitForm(r, k, k, form, form, "")
			return
		}
	}
}

func ruleVarcharLength(r *rule, c *lintCtx) {
	for _, d := range c.columnDefs() {
		if d.typ < 0 {
			continue
		}
		switch d.typeName {
		case "VARCHAR", "CHARACTER VARYING", "NVARCHAR", "VARCHAR2", "NATIONAL CHARACTER VARYING":
			if d.typEnd == d.typ || !c.at(d.typEnd).IsOp(")") {
				c.hit(r, d.typ, d.typEnd, "")
			}
		}
	}
}

// typeItems are the type names of type-not-everywhere, from its table.
func typeItem(name string) *Item {
	e := entry("type-not-everywhere")
	if e == nil {
		return nil
	}
	for i := range e.Items {
		if strings.EqualFold(e.Items[i].Name, name) {
			return &e.Items[i]
		}
	}
	return nil
}

func ruleTypeNotEverywhere(r *rule, c *lintCtx) {
	for _, d := range c.columnDefs() {
		if d.typ < 0 {
			continue
		}
		name := d.typeName
		if it := typeItem(name); it != nil {
			c.hitItem(r, d.typ, d.typEnd, it, itemMessage("type-not-everywhere", it, name))
		}
		for k := d.typ; k <= d.end && k <= d.typEnd+2; k++ {
			if c.ts[k].Is("UNSIGNED") {
				if it := typeItem("UNSIGNED"); it != nil {
					c.hitItem(r, k, k, it, itemMessage("type-not-everywhere", it, "UNSIGNED"))
				}
			}
		}
		if c.at(d.typEnd).IsOp("]") {
			if it := typeItem("ARRAY"); it != nil {
				c.hitItem(r, d.typ, d.typEnd, it, itemMessage("type-not-everywhere", it, "An array type"))
			}
		}
	}
}

// itemMessage says where an item of a table entry works, from the
// recorded answers.
func itemMessage(code string, it *Item, what string) string {
	rec := RecordedAnswers(code).Items[it.Name]
	notes := VersionNotes(rec, it.Since, it.Requires)
	named := func(eng string) string {
		if n := notes[eng]; len(n) > 0 {
			return EngineTitle(eng) + " (" + strings.Join(n, ", ") + ")"
		}
		return EngineTitle(eng)
	}
	var ok, no []string
	var all []string
	for _, eng := range EngineOrder {
		a, found := rec[eng]
		switch {
		case !found:
			continue
		case a.Error == "":
			ok = append(ok, named(eng))
		default:
			no = append(no, named(eng))
		}
		for _, n := range notes[eng] {
			all = append(all, EngineTitle(eng)+" "+n)
		}
	}
	switch {
	case len(ok) == 0 && len(no) == 0:
		return what + " does not work on every engine."
	case len(no) == 0:
		if len(all) > 0 {
			return what + " works on every engine (" + strings.Join(all, ", ") + ")."
		}
		return what + " works on every engine."
	case len(ok) == 0:
		return what + " works on none of the four engines."
	case len(ok) == 1:
		return what + " works on " + ok[0] + " only; " + titlesOf(no) + " refuse" + plural(no) + " it."
	}
	return what + " works on " + titlesOf(ok) + "; " + titlesOf(no) + " refuse" + plural(no) + " it."
}

func plural(xs []string) string {
	if len(xs) == 1 {
		return "s"
	}
	return ""
}

var mysqlTableOptions = map[string]bool{
	"ENGINE": true, "CHARSET": true, "COLLATE": true, "AUTO_INCREMENT": true, "ROW_FORMAT": true, "COMMENT": true,
	"KEY_BLOCK_SIZE": true, "STATS_PERSISTENT": true, "STATS_AUTO_RECALC": true, "PACK_KEYS": true, "CHECKSUM": true,
	"DELAY_KEY_WRITE": true, "TYPE": true, "AVG_ROW_LENGTH": true, "MAX_ROWS": true, "MIN_ROWS": true,
	"COMPRESSION": true, "ENCRYPTION": true,
}

func ruleTableOptions(r *rule, c *lintCtx) {
	defs, open, closeIdx := c.createTable()
	if open < 0 {
		return
	}
	// After the column list.
	for k := closeIdx + 1; k < len(c.ts); k++ {
		t := c.ts[k]
		switch {
		case mysqlTableOptions[t.Upper()] && t.Kind == sqltext.Word,
			t.Is("DEFAULT") && (c.at(k+1).Is("CHARSET") || c.at(k+1).Is("CHARACTER") || c.at(k+1).Is("COLLATE")),
			t.Is("CHARACTER") && c.at(k+1).Is("SET"):
			what := t.Upper()
			switch {
			case t.Is("DEFAULT"):
				what = "DEFAULT " + c.at(k+1).Upper()
			case t.Is("CHARACTER"):
				what = "CHARACTER SET"
			}
			c.hitForm(r, k, k, "ENGINE", "The table option "+what, "")
			return
		case t.Is("WITHOUT") && c.at(k+1).Is("ROWID"):
			c.hitForm(r, k, k+1, "WITHOUT ROWID", "The table option WITHOUT ROWID", "")
			return
		case t.Is("STRICT"):
			c.hitForm(r, k, k, "STRICT", "The table option STRICT", "")
			return
		case t.Is("INHERITS"):
			c.hitForm(r, k, k, "INHERITS", "INHERITS", "")
			return
		case t.Is("WITH") && c.at(k+1).IsOp("("):
			c.hitForm(r, k, k, "WITH (...)", "The table option WITH (...)", "")
			return
		case t.Is("PARTITION") && c.at(k+1).Is("BY"):
			c.hitForm(r, k, k+1, "PARTITION BY", "", "")
			return
		}
	}
	// Inside it.
	for _, d := range defs {
		if d.constraint {
			if t := c.ts[d.name]; t.Is("KEY") || t.Is("INDEX") || t.Is("FULLTEXT") || t.Is("SPATIAL") {
				c.hitForm(r, d.name, d.name, "index inside CREATE TABLE", "An index inside CREATE TABLE", "")
			}
			continue
		}
		for k := d.typEnd + 1; k <= d.end; k++ {
			t := c.ts[k]
			switch {
			case t.Is("CHARACTER") && c.at(k+1).Is("SET"), t.Is("CHARSET"):
				c.hitForm(r, k, k, "CHARACTER SET", "A character set on a column", "")
			case t.Is("COMMENT") && c.at(k+1).Kind == sqltext.String:
				c.hitForm(r, k, k+1, "COMMENT", "COMMENT on a column", "")
			case t.Is("ON") && c.at(k+1).Is("UPDATE"):
				c.hitForm(r, k, k+1, "ON UPDATE", "ON UPDATE for a column", "")
			case t.Is("COLLATE"):
				c.hitForm(r, k, k+1, "COLLATE", "", "")
			}
		}
	}
}

func ruleTextDefault(r *rule, c *lintCtx) {
	for _, d := range c.columnDefs() {
		if d.typ < 0 {
			continue
		}
		switch d.typeName {
		case "TEXT", "TINYTEXT", "MEDIUMTEXT", "LONGTEXT", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "JSON":
		default:
			continue
		}
		for k := d.typEnd + 1; k < d.end; k++ {
			if c.ts[k].Is("DEFAULT") && !c.at(k+1).IsOp("(") && !c.at(k+1).Is("NULL") {
				c.hit(r, k, k+1, "")
			}
		}
	}
}

func textKeyType(typ string) bool {
	switch typ {
	case "TEXT", "TINYTEXT", "MEDIUMTEXT", "LONGTEXT", "BLOB", "TINYBLOB", "MEDIUMBLOB", "LONGBLOB", "CLOB":
		return true
	}
	return false
}

func ruleIndexOnText(r *rule, c *lintCtx) {
	// CREATE [UNIQUE] INDEX ... ON t (cols)
	if len(c.ts) > 4 && c.ts[0].Is("CREATE") && (c.ts[1].Is("INDEX") || c.ts[2].Is("INDEX")) {
		for k, t := range c.ts {
			if !t.Is("ON") || !isName(c.at(k+1)) {
				continue
			}
			j := k + 1
			for c.at(j + 1).IsOp(".") {
				j += 2
			}
			table := strings.ToLower(identName(c.at(j)))
			if !c.at(j + 1).IsOp("(") {
				return
			}
			for _, a := range c.args(j + 1) {
				if a[0] != a[1] || c.schema == nil {
					continue
				}
				if ci, ok := c.schema.column(table, identName(c.ts[a[0]])); ok && textKeyType(ci.typ) {
					c.hitMsg(r, a[0], a[0], "", "MySQL refuses an index on the "+ci.typ+" column "+identName(c.ts[a[0]])+" without a prefix length.", "")
				}
			}
			return
		}
	}
	defs, open, _ := c.createTable()
	if open < 0 {
		return
	}
	types := map[string]string{}
	for _, d := range defs {
		if d.constraint || d.typ < 0 {
			continue
		}
		types[strings.ToLower(identName(c.ts[d.name]))] = d.typeName
		if textKeyType(d.typeName) && (c.has(d.typEnd+1, d.end, "PRIMARY") || c.has(d.typEnd+1, d.end, "UNIQUE")) {
			c.hit(r, d.name, d.end, "")
		}
	}
	for _, d := range defs {
		if !d.constraint {
			continue
		}
		for _, col := range c.parenNames(d.name, d.end) {
			if textKeyType(types[col]) {
				c.hit(r, d.name, d.end, "")
				break
			}
		}
	}
}

func ruleIndexIfNotExists(r *rule, c *lintCtx) {
	if len(c.ts) < 6 || !c.ts[0].Is("CREATE") {
		return
	}
	i := 1
	if c.ts[i].Is("UNIQUE") {
		i++
	}
	if c.at(i).Is("INDEX") && c.at(i+1).Is("IF") && c.at(i+2).Is("NOT") && c.at(i+3).Is("EXISTS") {
		c.hit(r, 0, i+3, "")
	}
}

func ruleTruncate(r *rule, c *lintCtx) {
	if c.ts[0].Is("TRUNCATE") {
		c.hit(r, 0, c.primaryEnd(1), "")
	}
}

func ruleCreateOrReplace(r *rule, c *lintCtx) {
	if len(c.ts) > 3 && c.ts[0].Is("CREATE") && c.ts[1].Is("OR") && c.ts[2].Is("REPLACE") {
		what := c.at(3).Upper()
		switch {
		case what == "VIEW" || c.has(3, min(len(c.ts)-1, 10), "VIEW"):
			c.hitForm(r, 0, 3, "VIEW", "CREATE OR REPLACE VIEW", "")
		case what == "TABLE":
			c.hitForm(r, 0, 3, "TABLE", "CREATE OR REPLACE TABLE", "")
		case what == "FUNCTION", what == "PROCEDURE", what == "TRIGGER":
			c.hitMsg(r, 0, 3, "", "CREATE OR REPLACE "+what+" has a different body syntax on every engine, and SQLite has none.", "")
		default:
			c.hitMsg(r, 0, 3, "", "SQLite has no CREATE OR REPLACE.", "")
		}
	}
}

// typePositions are the tokens that name a type: in column definitions and
// after AS in CAST. Function names that are also type names (CHAR, YEAR)
// are not calls there.
func (c *lintCtx) typePositions() map[int]bool {
	out := map[int]bool{}
	for _, d := range c.columnDefs() {
		if d.typ >= 0 {
			out[d.typ] = true
		}
	}
	for i := range c.ts {
		if _, as, _ := c.castTarget(i); as > 0 {
			out[as+1] = true
		}
		if c.ts[i].Is("CONVERT") && c.isCall(i) {
			if a := c.args(i + 1); len(a) == 2 {
				out[a[1][0]] = true
			}
		}
	}
	return out
}

func ruleFunctionNotEverywhere(r *rule, c *lintCtx) {
	e := entry(r.code)
	if e == nil {
		return
	}
	var types map[int]bool
	for i, t := range c.ts {
		if !c.isCall(i) {
			continue
		}
		for n := range e.Items {
			it := &e.Items[n]
			name, what := it.Name, it.Name+"()"
			if k := strings.IndexByte(name, '('); k > 0 {
				name, what = name[:k], it.Name
			}
			if !strings.EqualFold(name, t.Text) || it.MinArgs > 0 && len(c.args(i+1)) < it.MinArgs {
				continue
			}
			if types == nil {
				types = c.typePositions()
			}
			if types[i] {
				break
			}
			c.hitItem(r, i, c.close(i+1), it, itemMessage(r.code, it, what))
			break
		}
	}
}

// operatorMatch finds each operator of operator-not-everywhere: the index
// where it starts and where it ends, or -1.
var operatorMatch = map[string]func(c *lintCtx, i int) int{
	"::":    func(c *lintCtx, i int) int { return opAt(c, i, "::") },
	"ILIKE": func(c *lintCtx, i int) int { return wordAt(c, i, "ILIKE") },
	"DIV":   func(c *lintCtx, i int) int { return binaryWord(c, i, "DIV") },
	"MOD":   func(c *lintCtx, i int) int { return binaryWord(c, i, "MOD") },
	"<=>":   func(c *lintCtx, i int) int { return opAt(c, i, "<=>") },
	"REGEXP": func(c *lintCtx, i int) int {
		return wordAt(c, i, "REGEXP")
	},
	"RLIKE": func(c *lintCtx, i int) int { return wordAt(c, i, "RLIKE") },
	"~": func(c *lintCtx, i int) int {
		if c.ts[i].IsOp("~", "~*", "!~", "!~*") && i > 0 && !c.unary(i) {
			return i
		}
		return -1
	},
	"SIMILAR TO": func(c *lintCtx, i int) int {
		if c.ts[i].Is("SIMILAR") && c.at(i+1).Is("TO") {
			return i + 1
		}
		return -1
	},
	"IS DISTINCT FROM": func(c *lintCtx, i int) int {
		if !c.ts[i].Is("IS") {
			return -1
		}
		j := i + 1
		if c.at(j).Is("NOT") {
			j++
		}
		if c.at(j).Is("DISTINCT") && c.at(j+1).Is("FROM") {
			return j + 1
		}
		return -1
	},
	"XOR":  func(c *lintCtx, i int) int { return binaryWord(c, i, "XOR") },
	"&&":   func(c *lintCtx, i int) int { return opAt(c, i, "&&") },
	"GLOB": func(c *lintCtx, i int) int { return wordAt(c, i, "GLOB") },
	"==":   func(c *lintCtx, i int) int { return opAt(c, i, "==") },
	"!": func(c *lintCtx, i int) int {
		if c.ts[i].IsOp("!") {
			return i
		}
		return -1
	},
	"->>": func(c *lintCtx, i int) int {
		if c.ts[i].IsOp("->>", "->") {
			return i
		}
		return -1
	},
}

func opAt(c *lintCtx, i int, op string) int {
	if c.ts[i].IsOp(op) {
		return i
	}
	return -1
}

func wordAt(c *lintCtx, i int, w string) int {
	if c.ts[i].Is(w) && !c.at(i+1).IsOp("(") {
		return i
	}
	return -1
}

// binaryWord: a word used between two operands (7 DIV 2).
func binaryWord(c *lintCtx, i int, w string) int {
	if !c.ts[i].Is(w) || i == 0 || c.at(i+1).IsOp("(") {
		return -1
	}
	p := c.ts[i-1]
	if p.Kind == sqltext.Number || p.IsOp(")") || p.Kind == sqltext.String || isName(p) && !boundaryWord[p.Upper()] {
		return i
	}
	return -1
}

func ruleOperatorNotEverywhere(r *rule, c *lintCtx) {
	e := entry(r.code)
	if e == nil {
		return
	}
	for i := range c.ts {
		for n := range e.Items {
			it := &e.Items[n]
			m := operatorMatch[it.Name]
			if m == nil {
				continue
			}
			if end := m(c, i); end >= 0 {
				c.hitItem(r, i, end, it, itemMessage(r.code, it, "The "+it.Name+" operator"))
				break
			}
		}
	}
}

// lintComments checks the comments of a whole script.
func lintComments(file string, src string) []Problem {
	var out []Problem
	for _, t := range sqltext.Tokenize(src) {
		if t.Kind != sqltext.Comment {
			continue
		}
		code := ""
		switch {
		case strings.HasPrefix(t.Text, "#"):
			code = "hash-comment"
		case strings.HasPrefix(t.Text, "--") && len(t.Text) > 2 && !strings.ContainsRune(" \t\r\n\f\v", rune(t.Text[2])):
			code = "dash-comment"
		default:
			continue
		}
		e := entry(code)
		p := Problem{Code: code, File: file, Line: t.Line, Col: t.Col, Text: firstLine(t.Text)}
		for _, rl := range rules {
			if rl.code == code {
				p.Severity = rl.severity
			}
		}
		if e != nil {
			p.Message, p.Advice = e.Summary, e.Advice
		}
		out = append(out, p)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:77]) + "..."
	}
	return s
}
