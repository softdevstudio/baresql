package sqltext

import "strings"

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
func Split(src string) []Statement {
	toks := Tokenize(src)
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
		if t.Kind == Punct && t.Text == ";" && depth == 0 {
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
