package portable

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/softdevstudio/baresql/internal/sqltext"
)

// Problem is one place where SQL behaves differently across engines.
type Problem struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // error: fails on some engine; warning: answers differ; info: answers differ for some data
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Text     string `json:"text"`    // the SQL it is about
	Message  string `json:"message"` // what happens where
	Advice   string `json:"advice,omitempty"`
	Item     string `json:"item,omitempty"` // the function, operator or type, for table rules
}

// SeverityRank orders severities: error, warning, info.
func SeverityRank(s string) int {
	switch s {
	case "error":
		return 0
	case "warning":
		return 1
	}
	return 2
}

// Schema is what lint learns from CREATE TABLE and ALTER TABLE ... ADD
// statements: column types, lengths, NOT NULL and primary keys.
type Schema struct {
	cols  map[string]map[string]colInfo // table -> column -> info, lower case
	order map[string][]string           // table -> columns in order
	names map[string]string             // table -> the name as created
	pk    map[string][]string           // table -> primary key columns
}

type colInfo struct {
	typ     string // upper case, as written: VARCHAR, DOUBLE PRECISION ...
	length  int    // n of CHAR(n) and VARCHAR(n)
	notNull bool
	pk      bool
}

// column finds a column by name, in the given table or, without one, in
// any table where the name is unique.
func (s *Schema) column(table, col string) (colInfo, bool) {
	if s == nil || s.cols == nil {
		return colInfo{}, false
	}
	col = strings.ToLower(col)
	if table != "" {
		ci, ok := s.cols[strings.ToLower(table)][col]
		return ci, ok
	}
	var found colInfo
	n := 0
	for _, t := range s.cols {
		if ci, ok := t[col]; ok {
			found = ci
			n++
		}
	}
	return found, n == 1
}

func (s *Schema) init() {
	if s.cols == nil {
		s.cols = map[string]map[string]colInfo{}
		s.order = map[string][]string{}
		s.names = map[string]string{}
		s.pk = map[string][]string{}
	}
}

// Learn reads a CREATE TABLE or ALTER TABLE ... ADD statement into the
// schema.
func (s *Schema) Learn(st sqltext.Statement) {
	c := newLintCtx("", st, nil)
	if defs, open, _ := c.createTable(); open > 0 {
		name := c.tableNameBefore(open)
		if name == "" {
			return
		}
		s.init()
		key := strings.ToLower(name)
		s.names[key] = name
		s.cols[key] = map[string]colInfo{}
		s.order[key] = nil
		s.pk[key] = nil
		for _, d := range defs {
			if d.constraint {
				if c.ts[d.name].Is("PRIMARY") || c.ts[d.name].Is("CONSTRAINT") && c.has(d.name, d.end, "PRIMARY") {
					s.pk[key] = append(s.pk[key], c.parenNames(d.name, d.end)...)
				}
				continue
			}
			s.addColumn(key, c, d)
		}
		for _, p := range s.pk[key] {
			if ci, ok := s.cols[key][p]; ok {
				ci.pk, ci.notNull = true, true
				s.cols[key][p] = ci
			}
		}
		return
	}
	if table, defs := c.alterAdd(); table != "" {
		key := strings.ToLower(table)
		if s.cols == nil || s.cols[key] == nil {
			return
		}
		for _, d := range defs {
			s.addColumn(key, c, d)
		}
	}
}

func (s *Schema) addColumn(table string, c *lintCtx, d columnDef) {
	if d.typ < 0 {
		return
	}
	col := strings.ToLower(identName(c.ts[d.name]))
	ci := colInfo{typ: d.typeName}
	if open := d.typ + 1; open <= d.typEnd && c.ts[open].IsOp("(") {
		ci.length, _ = strconv.Atoi(c.at(open + 1).Text)
	}
	for k := d.typEnd + 1; k <= d.end; k++ {
		switch {
		case c.ts[k].Is("NOT") && c.at(k+1).Is("NULL"):
			ci.notNull = true
		case c.ts[k].Is("PRIMARY"):
			ci.pk, ci.notNull = true, true
			s.pk[table] = append(s.pk[table], col)
		}
	}
	s.cols[table][col] = ci
	s.order[table] = append(s.order[table], col)
}

func isConstraintWord(t sqltext.Token) bool {
	for _, w := range []string{"PRIMARY", "FOREIGN", "UNIQUE", "CHECK", "CONSTRAINT", "KEY", "INDEX", "EXCLUDE", "FULLTEXT", "SPATIAL"} {
		if t.Is(w) {
			return true
		}
	}
	return false
}

func identName(t sqltext.Token) string {
	if t.Kind == sqltext.QuotedIdent && len(t.Text) >= 2 {
		return t.Text[1 : len(t.Text)-1]
	}
	return t.Text
}

// LintScript checks a whole script: it learns the tables its CREATE TABLE
// statements define, then checks each statement and the comments.
func LintScript(name, src string) []Problem {
	return LintFiles([]Script{{Name: name, SQL: src}})
}

// LintFiles lints several files against one schema, learned from the
// CREATE TABLE statements of all of them, so queries in one file are
// checked against tables another file creates.
func LintFiles(files []Script) []Problem {
	split := make([][]sqltext.Statement, len(files))
	var all []sqltext.Statement
	for i, f := range files {
		split[i] = sqltext.Split(f.SQL)
		all = append(all, split[i]...)
	}
	schema := SchemaOf(all)
	var out []Problem
	for i, f := range files {
		out = append(out, lintStatements(f.Name, f.SQL, split[i], schema)...)
	}
	return out
}

func lintStatements(name, src string, stmts []sqltext.Statement, schema *Schema) []Problem {
	var out []Problem
	ignores := ignoreComments(src)
	keep := func(p Problem) {
		if !ignores.covers(p) {
			out = append(out, p)
		}
	}
	for _, st := range stmts {
		for _, p := range Lint(name, st, schema) {
			keep(p)
		}
	}
	for _, p := range lintComments(name, src) {
		keep(p)
	}
	sortProblems(out)
	return out
}

// SchemaOf learns the tables that statements create.
func SchemaOf(stmts []sqltext.Statement) *Schema {
	s := &Schema{}
	for _, st := range stmts {
		s.Learn(st)
	}
	return s
}

func sortProblems(ps []Problem) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
}

// Lint checks one statement. schema may be nil.
func Lint(file string, st sqltext.Statement, schema *Schema) []Problem {
	c := newLintCtx(file, st, schema)
	if len(c.ts) == 0 {
		return nil
	}
	for _, r := range rules {
		r.check(c)
	}
	sortProblems(c.out)
	return c.out
}

type lintCtx struct {
	file    string
	ts      []sqltext.Token
	pair    []int // index of the matching parenthesis, or -1
	depth   []int // parentheses open before each token
	schema  *Schema
	tables  []string
	aliases map[string]string
	out     []Problem
}

// hit records a problem for rule r covering tokens i..j. severity ""
// means the rule's own.
func (c *lintCtx) hit(r *rule, i, j int, severity string) {
	c.hitMsg(r, i, j, severity, "", "")
}

// hitMsg records a problem with its own message and advice.
func (c *lintCtx) hitMsg(r *rule, i, j int, severity, msg, advice string) {
	if i < 0 {
		i = 0
	}
	if j < i {
		j = i
	}
	if j >= len(c.ts) {
		j = len(c.ts) - 1
	}
	text := c.span(i, j)
	if r := []rune(text); len(r) > 80 {
		text = string(r[:77]) + "..."
	}
	if severity == "" {
		severity = r.severity
	}
	if e := entry(r.code); e != nil {
		if msg == "" {
			msg = e.Summary
		}
		if advice == "" {
			advice = e.Advice
		}
	}
	for _, p := range c.out { // one problem per rule and place
		if p.Code == r.code && p.Line == c.ts[i].Line && p.Col == c.ts[i].Col {
			return
		}
	}
	c.out = append(c.out, Problem{Code: r.code, Severity: severity, File: c.file, Line: c.ts[i].Line, Col: c.ts[i].Col, Text: text, Message: msg, Advice: advice})
}

// hitForm records a problem about one form of a rule, a row of its entry:
// the row's recorded answers decide --engines, and the message is msg when
// the code words it, else the row's own, else the entry's summary when
// every engine accepts the form, else one worked out from the answers.
// what is the construct as the SQL writes it.
func (c *lintCtx) hitForm(r *rule, i, j int, form, what, msg string) {
	e := entry(r.code)
	it := e.item(form)
	if it == nil {
		if testing.Testing() {
			panic("portable: rule " + r.code + " has no form " + form)
		}
		c.hitMsg(r, i, j, "", msg, "")
		return
	}
	if msg == "" {
		msg = strings.ReplaceAll(it.Message, "{what}", what)
	}
	refused := refusedSomewhere(RecordedAnswers(r.code).Items[it.Name])
	if msg == "" && refused {
		msg = itemMessage(r.code, it, what)
	}
	// An engine that refuses the form makes it an error, as everywhere,
	// and a form every engine runs is at most a warning.
	severity := it.Severity
	switch {
	case severity != "":
	case refused:
		severity = "error"
	case e.Severity == "error":
		severity = "warning"
	}
	if it.Note != "" {
		if msg == "" {
			msg = e.Summary
		}
		msg += " " + it.Note
	}
	n := len(c.out)
	c.hitMsg(r, i, j, severity, msg, it.Advice)
	if len(c.out) > n {
		c.out[len(c.out)-1].Item = it.Name
	}
}

func refusedSomewhere(m map[string]Recorded) bool {
	for _, a := range m {
		if a.Error != "" {
			return true
		}
	}
	return false
}

// hitItem records a problem about one row of a table rule.
func (c *lintCtx) hitItem(r *rule, i, j int, it *Item, msg string) {
	n := len(c.out)
	c.hitMsg(r, i, j, "", msg, it.Advice)
	if len(c.out) > n {
		c.out[len(c.out)-1].Item = it.Name
	}
}

func (c *lintCtx) at(i int) sqltext.Token {
	if i < 0 || i >= len(c.ts) {
		return sqltext.Token{}
	}
	return c.ts[i]
}

// has reports whether one of tokens s..e is the given word.
func (c *lintCtx) has(s, e int, word string) bool {
	for k := s; k <= e && k < len(c.ts); k++ {
		if c.ts[k].Is(word) {
			return true
		}
	}
	return false
}

// parenNames returns the names inside the first parentheses in s..e.
func (c *lintCtx) parenNames(s, e int) []string {
	for k := s; k <= e; k++ {
		if c.ts[k].IsOp("(") {
			var out []string
			for _, a := range c.args(k) {
				if isName(c.ts[a[0]]) {
					out = append(out, strings.ToLower(identName(c.ts[a[0]])))
				}
			}
			return out
		}
	}
	return nil
}

// tableNameBefore returns the table name just before the "(" of CREATE
// TABLE.
func (c *lintCtx) tableNameBefore(open int) string {
	if open > 0 && isName(c.ts[open-1]) {
		return identName(c.ts[open-1])
	}
	return ""
}

// alterAdd returns the table and the column definitions of ALTER TABLE t
// ADD [COLUMN] ... (one or more, comma separated).
func (c *lintCtx) alterAdd() (string, []columnDef) {
	if len(c.ts) < 5 || !c.ts[0].Is("ALTER") || !c.ts[1].Is("TABLE") {
		return "", nil
	}
	i := 2
	if c.at(i).Is("IF") && c.at(i+1).Is("EXISTS") {
		i += 2
	}
	if c.at(i).Is("ONLY") {
		i++
	}
	k := i
	for c.at(k + 1).IsOp(".") {
		k += 2
	}
	table := identName(c.at(k))
	var defs []columnDef
	for j := k + 1; j < len(c.ts); j++ {
		if !c.ts[j].Is("ADD") || c.depth[j] != 0 {
			continue
		}
		s := j + 1
		if c.at(s).Is("COLUMN") {
			s++
		}
		if c.at(s).Is("IF") && c.at(s+1).Is("NOT") && c.at(s+2).Is("EXISTS") {
			s += 3
		}
		if s >= len(c.ts) || isConstraintWord(c.ts[s]) {
			continue
		}
		e := s
		for e+1 < len(c.ts) && !(c.ts[e+1].IsOp(",") && c.depth[e+1] == 0) {
			e++
		}
		defs = append(defs, c.columnDef(s, e))
	}
	return table, defs
}

// columnDefs returns the column definitions of CREATE TABLE and ALTER
// TABLE ... ADD.
func (c *lintCtx) columnDefs() []columnDef {
	if defs, open, _ := c.createTable(); open > 0 {
		return defs
	}
	_, defs := c.alterAdd()
	return defs
}

type rule struct {
	code     string
	severity string
	check    func(c *lintCtx)
}

// rules: each looks for one construct in the tokens. The catalog
// (catalog.json) holds what each one means and how each engine answers.
var rules []*rule

// addRule registers a rule; its severity is its catalog entry's.
func addRule(code string, check func(r *rule, c *lintCtx)) {
	r := &rule{code: code}
	if e := entry(code); e != nil {
		r.severity = e.Severity
	}
	r.check = func(c *lintCtx) { check(r, c) }
	rules = append(rules, r)
}

// RuleCodes returns the codes lint checks, in catalog order.
func RuleCodes() []string {
	var out []string
	for _, r := range rules {
		out = append(out, r.code)
	}
	return out
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func hasUpper(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

func nonASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}

func isPlainString(t sqltext.Token) bool {
	return t.Kind == sqltext.String && strings.HasPrefix(t.Text, "'")
}

func numericText(s string) bool {
	_, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil
}

var intTypes = map[string]bool{"INT": true, "INTEGER": true, "BIGINT": true, "SMALLINT": true, "TINYINT": true,
	"MEDIUMINT": true, "INT2": true, "INT4": true, "INT8": true, "SERIAL": true, "BIGSERIAL": true, "SMALLSERIAL": true,
	"SIGNED": true, "UNSIGNED": true}

// Comments: "-- baresql:ignore code1, code2" on the line of a problem or
// the line before it switches those rules off there; without codes it
// switches every rule off.
type ignoreSet map[int][]string // line -> codes ("" for all)

var ignoreRe = regexp.MustCompile(`baresql:ignore\b([^\n*]*)`)

func ignoreComments(src string) ignoreSet {
	out := ignoreSet{}
	for _, t := range sqltext.Tokenize(src) {
		if t.Kind != sqltext.Comment {
			continue
		}
		m := ignoreRe.FindStringSubmatch(t.Text)
		if m == nil {
			continue
		}
		codes := strings.FieldsFunc(m[1], func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
		if len(codes) == 0 {
			codes = []string{""}
		}
		line := t.Line + strings.Count(t.Text, "\n") // a block comment counts on its last line
		out[line] = append(out[line], codes...)
		if line != t.Line {
			out[t.Line] = append(out[t.Line], codes...)
		}
	}
	return out
}

func (s ignoreSet) covers(p Problem) bool {
	for _, line := range []int{p.Line, p.Line - 1} {
		for _, c := range s[line] {
			if c == "" || c == p.Code {
				return true
			}
		}
	}
	return false
}
