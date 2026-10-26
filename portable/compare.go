package portable

import (
	"context"
	"fmt"
	"strings"

	"github.com/softdevstudio/baresql/internal/sqltext"
)

// Comparison is the outcome of running one script on several engines.
type Comparison struct {
	Engines    []EngineInfo      `json:"engines"`
	Statements []StatementResult `json:"statements"`
	Summary    CompareSummary    `json:"summary"`
}

type EngineInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type CompareSummary struct {
	Statements int `json:"statements"`
	Queries    int `json:"queries"` // statements that return rows
	Same       int `json:"same"`    // answered alike everywhere
	Differ     int `json:"differ"`  // statements whose answers or errors differ
	Skipped    int `json:"skipped"` // transaction control, which compare doesn't run
	Failed     int `json:"failed"`  // refused by every engine
}

// StatementResult is one statement with every engine's answer.
type StatementResult struct {
	File    string             `json:"file"`
	Line    int                `json:"line"`
	SQL     string             `json:"sql"`
	Query   bool               `json:"query"`
	Skipped string             `json:"skipped,omitempty"`
	Answers map[string]*Answer `json:"answers,omitempty"`
	// Groups lists the engines that answered alike, one group per answer.
	Groups [][]string `json:"groups,omitempty"`
	// Differences says in words how the answers differ.
	Differences []string `json:"differences,omitempty"`
	// Rules are the lint rules that point at this statement, which often
	// explain the difference.
	Rules []string `json:"rules,omitempty"`
}

// Differs reports whether the engines disagreed.
func (s StatementResult) Differs() bool { return len(s.Groups) > 1 }

// Script is SQL to compare, with the name it came from.
type Script struct {
	Name string
	SQL  string
}

// Compare runs every statement of the scripts on every engine, in order.
func Compare(ctx context.Context, engines []Engine, scripts []Script) (*Comparison, error) {
	if len(engines) < 2 {
		return nil, fmt.Errorf("compare needs at least two engines")
	}
	cmp := &Comparison{}
	for _, e := range engines {
		cmp.Engines = append(cmp.Engines, EngineInfo{Name: e.Name(), Version: e.Version()})
	}
	split := make([][]sqltext.Statement, len(scripts))
	var all []sqltext.Statement
	for i, sc := range scripts {
		split[i] = sqltext.Split(sc.SQL)
		all = append(all, split[i]...)
	}
	schema := SchemaOf(all)
	for i, sc := range scripts {
		for _, st := range split[i] {
			sr := StatementResult{File: sc.Name, Line: st.Line, SQL: st.Text, Query: st.ReturnsRows()}
			for _, p := range Lint(sc.Name, st, schema) {
				sr.Rules = appendOnce(sr.Rules, p.Code)
			}
			cmp.Summary.Statements++
			if why := skipReason(st); why != "" {
				sr.Skipped = why
				cmp.Summary.Skipped++
				cmp.Statements = append(cmp.Statements, sr)
				continue
			}
			sr.Answers = map[string]*Answer{}
			for _, e := range engines {
				a, err := e.Run(ctx, st.Text)
				if err != nil {
					return nil, fmt.Errorf("%s, at %s line %d: %w", EngineTitle(e.Name()), sc.Name, st.Line, err)
				}
				// The scratch database's random name says nothing to the
				// reader and would make every report different.
				a.Err = scratchRe.ReplaceAllString(a.Err, "baresql_compare")
				for i, n := range a.Notes {
					a.Notes[i] = scratchRe.ReplaceAllString(n, "baresql_compare")
				}
				sr.Answers[e.Name()] = a
			}
			judge(&sr, engines, st)
			if sr.Query {
				cmp.Summary.Queries++
			}
			allFailed := true
			for _, a := range sr.Answers {
				if a.Err == "" {
					allFailed = false
				}
			}
			switch {
			case sr.Differs():
				cmp.Summary.Differ++
			case allFailed:
				cmp.Summary.Failed++
			default:
				cmp.Summary.Same++
			}
			cmp.Statements = append(cmp.Statements, sr)
		}
	}
	return cmp, nil
}

func appendOnce(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

// skipReason says why compare does not run a statement, or returns "".
// Every engine works in a throwaway space (a transaction rolled back at the
// end, a scratch database dropped at the end, a temporary file), and
// compare keeps scripts inside it: it skips statements that end the
// transaction, leave the scratch database, or reach the server, its files
// or its accounts.
func skipReason(st sqltext.Statement) string {
	for _, t := range st.Tokens {
		if t.Kind == sqltext.Invalid {
			return "an unterminated string, name or comment at line " + fmt.Sprint(t.Line)
		}
	}
	ts := st.Significant()
	if len(ts) == 0 {
		return "empty"
	}
	word := func(i int) string {
		if i < len(ts) && ts[i].Kind == sqltext.Word {
			return ts[i].Upper()
		}
		return ""
	}
	first, second := word(0), word(1)
	switch {
	case transactionControl(first):
		return "transaction control: compare runs each engine in its own throwaway transaction"
	case first == "USE" || first == "CONNECT":
		return "USE would leave compare's scratch database"
	case first == "GRANT" || first == "REVOKE" || first == "SHUTDOWN" || first == "KILL" || first == "FLUSH" ||
		first == "RESET" || first == "PURGE" || first == "INSTALL" || first == "UNINSTALL" || first == "CHANGE" ||
		first == "CHECKPOINT" || first == "LOAD" || first == "COPY" || first == "IMPORT" || first == "LOCK" ||
		first == "UNLOCK" || first == "VACUUM" || first == "CLUSTER" || first == "REINDEX" || first == "SECURITY":
		return first + " reaches beyond compare's scratch space"
	case (first == "START" || first == "STOP") && (second == "SLAVE" || second == "REPLICA" || second == "GROUP_REPLICATION"):
		return "replication control reaches beyond compare's scratch space"
	case first == "ALTER" && second == "SYSTEM", first == "SET" && (second == "GLOBAL" || second == "PERSIST" || second == "PERSIST_ONLY" || second == "PASSWORD" || second == "ROLE" || second == "SESSION_AUTHORIZATION" || strings.HasPrefix(strings.ToUpper(tokenText(ts, 1)), "@@GLOBAL")):
		return "this changes the server or the login, beyond compare's scratch space"
	case first == "CREATE" || first == "DROP" || first == "ALTER" || first == "RENAME":
		obj := ""
	words:
		for i := 1; i < len(ts) && i < 5; i++ {
			switch word(i) {
			case "OR", "REPLACE", "IF", "NOT", "EXISTS", "UNIQUE", "TEMPORARY", "TEMP":
			default:
				obj = word(i)
				break words
			}
		}
		switch obj {
		case "DATABASE", "SCHEMA", "USER", "ROLE", "TABLESPACE", "SERVER", "EXTENSION", "LOGFILE", "RESOURCE", "SUBSCRIPTION", "PUBLICATION":
			return obj + " statements reach beyond compare's scratch space"
		}
	}
	for i, t := range ts {
		if t.Is("INTO") && (word(i+1) == "OUTFILE" || word(i+1) == "DUMPFILE") {
			return "SELECT ... INTO " + word(i+1) + " writes a file on the server"
		}
	}
	return ""
}

func tokenText(ts []sqltext.Token, i int) string {
	if i < len(ts) {
		return ts[i].Text
	}
	return ""
}

// qualifiedTarget returns the qualifier and name of a qualified table
// (db.table) that a statement changes, or "".
func qualifiedTarget(ts []sqltext.Token) (string, string) {
	if len(ts) == 0 {
		return "", ""
	}
	switch strings.ToUpper(ts[0].Text) {
	case "INSERT", "UPDATE", "DELETE", "REPLACE", "CREATE", "DROP", "ALTER", "TRUNCATE", "RENAME", "MERGE", "WITH":
	default:
		return "", ""
	}
	for i, t := range ts {
		if !(t.Is("INTO") || t.Is("UPDATE") || t.Is("TABLE") || t.Is("FROM") && ts[0].Is("DELETE") || t.Is("VIEW") || t.Is("INDEX") || t.Is("ON") || t.Is("TO") || t.Is("TRIGGER") || t.Is("JOIN") && ts[0].Is("UPDATE")) {
			continue
		}
		j := i + 1
		for j < len(ts) && (ts[j].Is("IF") || ts[j].Is("NOT") || ts[j].Is("EXISTS") || ts[j].Is("ONLY")) {
			j++
		}
		if j+2 < len(ts) && isName(ts[j]) && ts[j+1].IsOp(".") && isName(ts[j+2]) && !(j+3 < len(ts) && ts[j+3].IsOp(".")) {
			if t.Is("ON") && !(ts[0].Is("CREATE") || ts[0].Is("DROP")) {
				continue
			}
			return identName(ts[j]), identName(ts[j+2])
		}
	}
	return "", ""
}

// ordered reports whether the statement sorts its result (an ORDER BY
// outside any parentheses), so row order is part of the answer.
func ordered(st sqltext.Statement) bool {
	depth := 0
	sig := st.Significant()
	for i, t := range sig {
		switch {
		case t.IsOp("("):
			depth++
		case t.IsOp(")"):
			depth--
		case depth == 0 && t.Is("ORDER") && i+1 < len(sig) && sig[i+1].Is("BY"):
			return true
		}
	}
	return false
}

// judge groups the engines by answer and says how the answers differ.
func judge(sr *StatementResult, engines []Engine, st sqltext.Statement) {
	sorted := ordered(st)
	var names []string
	for _, e := range engines {
		names = append(names, e.Name())
	}
	var groups [][]string
	for _, n := range names {
		placed := false
		for gi, g := range groups {
			if answersEqual(sr.Answers[g[0]], sr.Answers[n], sorted) {
				groups[gi] = append(groups[gi], n)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []string{n})
		}
	}
	sr.Groups = groups
	if len(groups) > 1 {
		sr.Differences = describe(sr.Answers, groups, sorted)
	} else if names := columnCase(sr.Answers, names); names != "" {
		// Same values, but a client reading columns by name sees a change.
		sr.Groups = splitByColumnNames(sr.Answers, names0(engines))
		sr.Differences = []string{names}
	}
}

func names0(engines []Engine) []string {
	var out []string
	for _, e := range engines {
		out = append(out, e.Name())
	}
	return out
}

func answersEqual(a, b *Answer, sorted bool) bool {
	if (a.Err != "") != (b.Err != "") {
		return false
	}
	if a.Err != "" {
		return true // both refused
	}
	if (a.Columns == nil) != (b.Columns == nil) {
		// One engine returned rows and the other a plain status; an empty
		// SQLite result has no column names either.
		return len(a.Rows) == 0 && len(b.Rows) == 0
	}
	if a.Columns != nil && b.Columns != nil && len(a.Columns) != len(b.Columns) {
		return false
	}
	ra, rb := a.Rows, b.Rows
	if !sorted {
		ra, rb = sortedRows(ra), sortedRows(rb)
	}
	return rowsEqual(ra, rb)
}

// columnCase reports result columns whose names differ only in case
// (PostgreSQL folds unquoted aliases to lower case).
func columnCase(answers map[string]*Answer, names []string) string {
	var ref *Answer
	refName := ""
	for _, n := range names {
		if a := answers[n]; a != nil && a.Err == "" && a.Columns != nil {
			if ref == nil {
				ref, refName = a, n
				continue
			}
			if len(a.Columns) != len(ref.Columns) {
				continue
			}
			for i := range a.Columns {
				if a.Columns[i] != ref.Columns[i] && strings.EqualFold(a.Columns[i], ref.Columns[i]) {
					return fmt.Sprintf("column %d is named %s on %s and %s on %s", i+1, ref.Columns[i], EngineTitle(refName), a.Columns[i], EngineTitle(n))
				}
			}
		}
	}
	return ""
}

func splitByColumnNames(answers map[string]*Answer, names []string) [][]string {
	var groups [][]string
	key := func(a *Answer) string { return strings.Join(a.Columns, "\x00") }
	for _, n := range names {
		a := answers[n]
		placed := false
		for gi, g := range groups {
			if b := answers[g[0]]; a.Columns == nil || b.Columns == nil || key(a) == key(b) {
				groups[gi] = append(groups[gi], n)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []string{n})
		}
	}
	return groups
}

// describe puts the differences into words.
func describe(answers map[string]*Answer, groups [][]string, sorted bool) []string {
	var out []string
	var failed, ok []string
	for _, g := range groups {
		if answers[g[0]].Err != "" {
			failed = append(failed, g...)
		} else {
			ok = append(ok, g...)
		}
	}
	if len(failed) > 0 && len(ok) > 0 {
		out = append(out, "refused by "+titles(failed)+", answered by "+titles(ok))
	}
	// Compare the answering groups with the first of them.
	var answering [][]string
	for _, g := range groups {
		if answers[g[0]].Err == "" {
			answering = append(answering, g)
		}
	}
	for i := 1; i < len(answering); i++ {
		a, b := answers[answering[0][0]], answers[answering[i][0]]
		out = append(out, whatDiffers(a, b, sorted, titles(answering[0]), titles(answering[i]))...)
	}
	return dedup(out)
}

func whatDiffers(a, b *Answer, sorted bool, an, bn string) []string {
	var out []string
	if len(a.Rows) != len(b.Rows) {
		return append(out, fmt.Sprintf("%d rows on %s, %d on %s", len(a.Rows), an, len(b.Rows), bn))
	}
	if a.Columns != nil && b.Columns != nil && len(a.Columns) != len(b.Columns) {
		return append(out, fmt.Sprintf("%d columns on %s, %d on %s", len(a.Columns), an, len(b.Columns), bn))
	}
	if sorted && rowsEqual(sortedRows(a.Rows), sortedRows(b.Rows)) {
		return append(out, "the same rows in a different order")
	}
	ra, rb := a.Rows, b.Rows
	if !sorted {
		ra, rb = sortedRows(ra), sortedRows(rb)
	}
	for i := range ra {
		for j := range ra[i] {
			x, y := ra[i][j], rb[i][j]
			if cellsEqual(x, y) {
				continue
			}
			switch {
			case x.Null && !y.Null && y.Text == "", y.Null && !x.Null && x.Text == "":
				out = append(out, "NULL on one side, empty text on the other")
			case !x.Null && !y.Null && x.Kind == KindText && y.Kind == KindText && strings.EqualFold(x.Text, y.Text):
				out = append(out, "the same text in different case")
			default:
				out = append(out, "different values")
			}
		}
	}
	if len(out) == 0 {
		out = append(out, "different answers")
	}
	return out
}

func titles(names []string) string {
	var t []string
	for _, n := range names {
		t = append(t, EngineTitle(n))
	}
	switch len(t) {
	case 0:
		return ""
	case 1:
		return t[0]
	}
	return strings.Join(t[:len(t)-1], ", ") + " and " + t[len(t)-1]
}

func dedup(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
