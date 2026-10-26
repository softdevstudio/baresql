package sqltext

import (
	"regexp"
	"strings"
)

// Statement is one SQL statement of a script.
type Statement struct {
	Text      string  // from its first significant token up to (not including) the ;, comments included
	Tokens    []Token // the tokens of Text, spaces and comments included
	Line, Col int     // where it starts
	Pos       int     // byte offset where it starts
}

// Significant returns the statement's tokens without spaces and comments.
func (s Statement) Significant() []Token {
	var out []Token
	for _, t := range s.Tokens {
		if t.Significant() {
			out = append(out, t)
		}
	}
	return out
}

// Split cuts a script into statements at semicolons outside strings,
// identifiers, comments and the BEGIN ... END bodies of triggers and
// procedures. Empty statements are dropped.
//
// A line DELIMITER // (the mysql program's command) makes // end the
// statements that follow, until DELIMITER ; puts the semicolon back; the
// DELIMITER lines themselves are no statements.
func Split(src string) []Statement {
	locs := delimiterLine.FindAllStringSubmatchIndex(src, -1)
	if len(locs) == 0 {
		return splitTokens(src, Tokenize(src), true)
	}
	var out []Statement
	delim, prev := ";", 0
	for _, m := range locs {
		if !atBoundary(src[prev:m[0]], delim) {
			continue // inside a statement, such as a column named delimiter
		}
		out = append(out, splitSegment(src, prev, m[0], delim)...)
		delim, prev = src[m[2]:m[3]], m[1]
	}
	return append(out, splitSegment(src, prev, len(src), delim)...)
}

// atBoundary reports whether seg ends between statements: after its last
// delimiter there is nothing but space and comments.
func atBoundary(seg, delim string) bool {
	if delim == ";" {
		var last Token
		for _, t := range Tokenize(seg) {
			if t.Significant() {
				last = t
			}
		}
		return last.Text == "" || last.Kind == Punct && last.Text == ";"
	}
	rest := seg
	if stops := delimiterStops(seg, delim); len(stops) > 0 {
		rest = seg[stops[len(stops)-1]+len(delim):]
	}
	for _, t := range Tokenize(rest) {
		if t.Significant() {
			return false
		}
	}
	return true
}

var delimiterLine = regexp.MustCompile(`(?im)^[ \t]*DELIMITER[ \t]+(\S+)[ \t]*\r?$`)

// splitSegment splits src[from:to], where statements end with delim.
func splitSegment(src string, from, to int, delim string) []Statement {
	if delim == ";" {
		return splitTokens(src, tokenizeAt(src, from, to), true)
	}
	var out []Statement
	start := from
	for _, end := range delimiterStops(src[from:to], delim) {
		out = append(out, splitTokens(src, tokenizeAt(src, start, from+end), false)...)
		start = from + end + len(delim)
	}
	return append(out, splitTokens(src, tokenizeAt(src, start, to), false)...)
}

// delimiterStops finds delim outside quotes and comments, the way the
// mysql program does: '...', "..." and `...` with backslash escapes, and
// /* */, # and -- comments.
func delimiterStops(s, delim string) []int {
	var out []int
	for i := 0; i < len(s); {
		switch c := s[i]; {
		case strings.HasPrefix(s[i:], delim):
			out = append(out, i)
			i += len(delim)
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < len(s) && s[j] != c {
				if s[j] == '\\' && c != '`' {
					j++
				}
				j++
			}
			i = j + 1
		case strings.HasPrefix(s[i:], "/*"):
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return out
			}
			i += 2 + end + 2
		case c == '#', strings.HasPrefix(s[i:], "-- "), strings.HasPrefix(s[i:], "--\t"), strings.HasPrefix(s[i:], "--\n"):
			nl := strings.IndexByte(s[i:], '\n')
			if nl < 0 {
				return out
			}
			i += nl
		default:
			i++
		}
	}
	return out
}

// tokenizeAt tokenizes src[from:to] with positions, lines and columns that
// count from the start of src.
func tokenizeAt(src string, from, to int) []Token {
	line, col := 1, 1
	for _, r := range src[:from] {
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	ts := Tokenize(src[from:to])
	for i := range ts {
		if ts[i].Line == 1 {
			ts[i].Col += col - 1
		}
		ts[i].Line += line - 1
		ts[i].Pos += from
	}
	return ts
}

// splitTokens cuts tokens of src into statements; with semicolons false
// the tokens are one statement whatever they hold.
func splitTokens(src string, toks []Token, semicolons bool) []Statement {
	var out []Statement
	start := -1 // index of the first significant token of the current statement
	depth := 0  // BEGIN ... END nesting inside a trigger or procedure body
	caseDepth := 0
	routine := false // the statement creates a trigger, procedure, function or event
	words := 0       // significant words seen in the current statement
	flush := func(end int) {
		if start < 0 {
			return
		}
		// The statement runs to the semicolon: comments before it are part
		// of it (engines read "SELECT 1 --1" and "SELECT 1 # x" differently),
		// trailing white space is not.
		last := end - 1
		for last >= start && toks[last].Kind == Space {
			last--
		}
		if last >= start {
			ts := append([]Token(nil), toks[start:last+1]...)
			out = append(out, Statement{Text: src[toks[start].Pos : toks[last].Pos+len(toks[last].Text)],
				Tokens: ts, Line: toks[start].Line, Col: toks[start].Col, Pos: toks[start].Pos})
		}
		start, depth, caseDepth, routine, words = -1, 0, 0, false, 0
	}
	for i, t := range toks {
		if !t.Significant() {
			continue
		}
		if t.Kind == Punct && t.Text == ";" && depth == 0 && semicolons {
			flush(i)
			continue
		}
		if start < 0 {
			start = i
		}
		if t.Kind != Word {
			continue
		}
		words++
		u := t.Upper()
		if words <= 6 && (u == "TRIGGER" || u == "PROCEDURE" || u == "FUNCTION" || u == "EVENT") && startsWithCreate(toks, start) {
			routine = true
		}
		if !routine {
			continue
		}
		switch u {
		case "BEGIN":
			depth++
		case "CASE":
			if depth > 0 {
				caseDepth++
			}
		case "END":
			if caseDepth > 0 {
				caseDepth--
			} else if depth > 0 && !followedBy(toks, i, "IF", "LOOP", "WHILE", "REPEAT", "CASE") {
				depth--
			}
		}
	}
	flush(len(toks))
	return out
}

func startsWithCreate(toks []Token, start int) bool {
	return start >= 0 && toks[start].Is("CREATE")
}

// followedBy reports whether the next significant word after i is one of
// words (END IF, END LOOP ... close inner blocks, not the body).
func followedBy(toks []Token, i int, words ...string) bool {
	for j := i + 1; j < len(toks); j++ {
		if !toks[j].Significant() {
			continue
		}
		for _, w := range words {
			if toks[j].Is(w) {
				return true
			}
		}
		return false
	}
	return false
}

// FirstWord is the statement's first keyword, upper-cased: SELECT, WITH,
// INSERT, CREATE ...
func (s Statement) FirstWord() string {
	for _, t := range s.Tokens {
		if t.Significant() {
			if t.Kind == Word {
				return t.Upper()
			}
			if t.Text == "(" {
				return "("
			}
			return ""
		}
	}
	return ""
}

// ReturnsRows reports whether the statement is a query: SELECT, WITH ...
// SELECT, VALUES, TABLE, or a data change with RETURNING.
func (s Statement) ReturnsRows() bool {
	switch s.FirstWord() {
	case "SELECT", "VALUES", "TABLE", "(", "SHOW", "EXPLAIN", "PRAGMA":
		return true
	case "WITH":
		// The statement after the WITH list decides: a SELECT, or a data
		// change with RETURNING.
		switch s.mainWord() {
		case "SELECT", "VALUES", "TABLE", "(":
			return true
		case "INSERT", "UPDATE", "DELETE", "REPLACE", "MERGE":
			return s.topLevelWord("RETURNING")
		}
		return false
	case "INSERT", "UPDATE", "DELETE", "REPLACE":
		return hasWord(s, "RETURNING")
	}
	return false
}

// mainWord is the first keyword after a WITH list: the word at
// parenthesis depth 0 that follows a closing parenthesis.
func (s Statement) mainWord() string {
	depth := 0
	afterClose := false
	for _, t := range s.Significant() {
		switch {
		case t.IsOp("("):
			if depth == 0 && afterClose {
				return "("
			}
			depth++
			afterClose = false
		case t.IsOp(")"):
			depth--
			afterClose = depth == 0
		case depth == 0 && afterClose && t.Kind == Word:
			return t.Upper()
		case depth == 0:
			afterClose = false
		}
	}
	return ""
}

// topLevelWord reports whether the word appears outside parentheses.
func (s Statement) topLevelWord(word string) bool {
	depth := 0
	for _, t := range s.Significant() {
		switch {
		case t.IsOp("("):
			depth++
		case t.IsOp(")"):
			depth--
		case depth == 0 && t.Is(word):
			return true
		}
	}
	return false
}

func hasWord(s Statement, words ...string) bool {
	for _, t := range s.Tokens {
		if t.Kind != Word {
			continue
		}
		for _, w := range words {
			if strings.EqualFold(t.Text, w) {
				return true
			}
		}
	}
	return false
}
