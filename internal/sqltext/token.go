// Package sqltext splits SQL scripts into statements and statements into
// tokens, for all four engines BareSQL Portable knows (SQLite, PostgreSQL,
// MySQL, MariaDB). It is a lexer, not a parser: it knows quoting,
// comments and nesting well enough to find statement boundaries and to let
// lint rules look at the tokens.
package sqltext

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind is the type of a token.
type Kind int

const (
	Space       Kind = iota // whitespace
	Comment                 // -- ..., /* ... */, # ... (MySQL)
	Word                    // keyword or bare identifier
	QuotedIdent             // "name", `name` or [name]
	String                  // '...', E'...', N'...', X'...', $tag$...$tag$
	Number                  // 12, 1.5, .5, 1e10, 0x1F
	Param                   // ?, $1, :name, @var
	Operator                // + - * / || :: -> = <> ...
	Punct                   // ( ) , ; .
	Invalid                 // an unterminated string, identifier or comment
)

// Token is one piece of SQL text.
type Token struct {
	Kind Kind
	Text string
	Pos  int // byte offset in the source
	Line int // 1-based
	Col  int // 1-based, in characters
}

// Upper is the token text in upper case, for keyword checks.
func (t Token) Upper() string { return strings.ToUpper(t.Text) }

// Is reports whether the token is the given keyword (case-insensitive).
func (t Token) Is(word string) bool { return t.Kind == Word && strings.EqualFold(t.Text, word) }

// IsOp reports whether the token is one of the given operators or
// punctuation marks.
func (t Token) IsOp(ops ...string) bool {
	if t.Kind != Operator && t.Kind != Punct {
		return false
	}
	for _, o := range ops {
		if t.Text == o {
			return true
		}
	}
	return false
}

// Significant reports whether the token is neither space nor comment.
func (t Token) Significant() bool { return t.Kind != Space && t.Kind != Comment }

// StringValue is the content of a plain '...' string with ” unescaped.
// For other string forms it returns the text between the quotes as is.
func (t Token) StringValue() string {
	s := t.Text
	if t.Kind != String {
		return s
	}
	if len(s) > 0 && s[0] != '\'' && s[0] != '$' {
		s = s[1:] // E'..', N'..', X'..', B'..'
	}
	if strings.HasPrefix(s, "$") {
		if i := strings.Index(s[1:], "$"); i >= 0 {
			tag := s[:i+2]
			return strings.TrimSuffix(strings.TrimPrefix(s, tag), tag)
		}
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

// Tokenize splits SQL text into tokens. Nothing is lost: the token texts
// joined give back the input.
func Tokenize(src string) []Token {
	l := &lexer{src: src, line: 1, col: 1}
	for l.pos < len(src) {
		l.next()
	}
	return l.toks
}

type lexer struct {
	src       string
	pos       int
	line, col int
	toks      []Token
}

func (l *lexer) emit(kind Kind, end int) {
	text := l.src[l.pos:end]
	l.toks = append(l.toks, Token{Kind: kind, Text: text, Pos: l.pos, Line: l.line, Col: l.col})
	for _, r := range text {
		if r == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
	}
	l.pos = end
}

func (l *lexer) peek(off int) byte {
	if l.pos+off < len(l.src) {
		return l.src[l.pos+off]
	}
	return 0
}

func isWordStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isWordPart(r rune) bool {
	return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (l *lexer) next() {
	s, p := l.src, l.pos
	c := s[p]
	switch {
	case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
		e := p
		for e < len(s) && strings.IndexByte(" \t\n\r\f\v", s[e]) >= 0 {
			e++
		}
		l.emit(Space, e)
	case c == '-' && l.peek(1) == '-', c == '#' && !(l.peek(1) == '>' || l.peek(1) == '-'):
		// "--" comments everywhere; "#" comments are MySQL's ("#>" and
		// "#-" are PostgreSQL JSON operators).
		e := strings.IndexByte(s[p:], '\n')
		if e < 0 {
			e = len(s)
		} else {
			e += p
		}
		l.emit(Comment, e)
	case c == '/' && l.peek(1) == '*':
		l.blockComment()
	case c == '\'':
		l.quoted(p, '\'', String, false)
	case (c == 'E' || c == 'e') && l.peek(1) == '\'':
		l.quoted(p+1, '\'', String, true) // PostgreSQL escape string: backslashes escape
	case (c == 'N' || c == 'n' || c == 'X' || c == 'x' || c == 'B' || c == 'b') && l.peek(1) == '\'':
		l.quoted(p+1, '\'', String, false)
	case c == '"':
		l.quoted(p, '"', QuotedIdent, false)
	case c == '`':
		l.quoted(p, '`', QuotedIdent, false)
	case c == '[':
		// [name] is an identifier on SQLite; elsewhere [ is an array
		// subscript, which never holds a newline or a semicolon.
		e := strings.IndexByte(s[p:], ']')
		if e > 1 && !strings.ContainsAny(s[p+1:p+e], "\n;'\"[") && l.prevAllowsBracketIdent() {
			l.emit(QuotedIdent, p+e+1)
		} else {
			l.emit(Punct, p+1)
		}
	case c == '$':
		l.dollar()
	case c >= '0' && c <= '9', c == '.' && l.peek(1) >= '0' && l.peek(1) <= '9':
		l.number()
	case c == '?':
		e := p + 1
		for e < len(s) && s[e] >= '0' && s[e] <= '9' { // SQLite ?NNN
			e++
		}
		l.emit(Param, e)
	case (c == ':' || c == '@') && p+1 < len(s) && (isWordStart(rune(s[p+1])) || s[p+1] == '@'):
		if c == ':' && l.peek(1) == ':' {
			l.emit(Operator, p+2)
			return
		}
		e := p + 1
		for e < len(s) && (s[e] == '@' || isWordPart(rune(s[e])) || s[e] == '.') {
			e++
		}
		l.emit(Param, e)
	default:
		r, size := utf8.DecodeRuneInString(s[p:])
		if isWordStart(r) {
			e := p + size
			for e < len(s) {
				r2, sz := utf8.DecodeRuneInString(s[e:])
				if !isWordPart(r2) {
					break
				}
				e += sz
			}
			l.emit(Word, e)
			return
		}
		if strings.IndexByte("(),;.]", c) >= 0 {
			l.emit(Punct, p+1)
			return
		}
		for _, op := range operators {
			if strings.HasPrefix(s[p:], op) {
				l.emit(Operator, p+len(op))
				return
			}
		}
		l.emit(Operator, p+size)
	}
}

// operators, longest first.
var operators = []string{
	"->>", "#>>", "<=>", "<<=", ">>=", "!~*", "~~*", "@@@",
	"||", "::", "->", "#>", "#-", "<>", "!=", "<=", ">=", "<<", ">>", "==", "&&", "!~", "~*", "~~", "@>", "<@", "?|", "?&", ":=",
	"+", "-", "*", "/", "%", "=", "<", ">", "!", "~", "&", "|", "^", "@", ":",
}

// prevAllowsBracketIdent: "[" starts an identifier where a name can stand
// (after FROM, JOIN, SELECT, a comma, an operator ...), not after a name
// or a closing parenthesis, where it is a subscript.
func (l *lexer) prevAllowsBracketIdent() bool {
	for i := len(l.toks) - 1; i >= 0; i-- {
		t := l.toks[i]
		if !t.Significant() {
			continue
		}
		switch t.Kind {
		case Word:
			_, kw := identLeadingKeywords[strings.ToUpper(t.Text)]
			return kw
		case QuotedIdent, String, Number, Param:
			return false
		case Punct:
			return t.Text != ")" && t.Text != "]"
		}
		return true
	}
	return true
}

var identLeadingKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "JOIN": true, "INTO": true, "UPDATE": true, "TABLE": true, "ON": true,
	"WHERE": true, "AND": true, "OR": true, "BY": true, "SET": true, "AS": true, "DISTINCT": true,
	"NOT": true, "IN": true, "EXISTS": true, "WHEN": true, "THEN": true, "ELSE": true, "RETURNING": true,
	"INDEX": true, "VIEW": true, "VALUES": true, "HAVING": true,
}

func (l *lexer) blockComment() {
	s := l.src
	depth, e := 0, l.pos
	for e < len(s) {
		switch {
		case strings.HasPrefix(s[e:], "/*"):
			depth++
			e += 2
		case strings.HasPrefix(s[e:], "*/"):
			depth--
			e += 2
			if depth == 0 {
				l.emit(Comment, e)
				return
			}
		default:
			e++
		}
	}
	l.emit(Invalid, len(s))
}

// quoted reads a quoted string or identifier whose opening quote is at q;
// a doubled quote stands for itself, and with backslashes set a backslash
// escapes the next character.
//
// MySQL and MariaDB also read \' as a quote inside '...' and "...". A quote
// after an odd run of backslashes that is directly followed by more text
// ('it\'s') is taken that way; one followed by a space, a comma or the like
// ('C:\', ...) ends the string, as on the other engines. A string that would
// run to the end of the input is read again with backslash escapes.
func (l *lexer) quoted(q int, quote byte, kind Kind, backslashes bool) {
	end := scanQuoted(l.src, q, quote, backslashes, !backslashes)
	if end < 0 && !backslashes {
		end = scanQuoted(l.src, q, quote, true, false)
	}
	if end < 0 {
		l.emit(Invalid, len(l.src))
		return
	}
	l.emit(kind, end)
}

// scanQuoted returns the offset just past the closing quote, or -1. With
// guess set, a backslash-quote followed by more text switches to backslash
// escapes.
func scanQuoted(s string, q int, quote byte, backslashes, guess bool) int {
	e := q + 1
	for e < len(s) {
		switch {
		case backslashes && s[e] == '\\' && e+1 < len(s):
			e += 2
		case s[e] == quote && e+1 < len(s) && s[e+1] == quote:
			e += 2
		case s[e] == quote && guess && !backslashes && oddBackslashes(s, q+1, e) && !endsLiteral(s, e+1):
			backslashes = true
			e++
		case s[e] == quote:
			return e + 1
		default:
			e++
		}
	}
	return -1
}

// oddBackslashes reports whether s[from:at] ends in an odd number of
// backslashes.
func oddBackslashes(s string, from, at int) bool {
	n := 0
	for i := at - 1; i >= from && s[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

// endsLiteral reports whether what follows a closing quote at i can
// follow a string literal.
func endsLiteral(s string, i int) bool {
	return i >= len(s) || strings.IndexByte(" \t\r\n\f\v,);|=<>!+-*/%:]", s[i]) >= 0
}

// dollar reads $1 (a parameter) or a PostgreSQL dollar-quoted string.
func (l *lexer) dollar() {
	s, p := l.src, l.pos
	e := p + 1
	if e < len(s) && s[e] >= '0' && s[e] <= '9' {
		for e < len(s) && s[e] >= '0' && s[e] <= '9' {
			e++
		}
		l.emit(Param, e)
		return
	}
	for e < len(s) && (s[e] == '_' || s[e] >= 'a' && s[e] <= 'z' || s[e] >= 'A' && s[e] <= 'Z' || s[e] >= '0' && s[e] <= '9') {
		e++
	}
	if e < len(s) && s[e] == '$' {
		tag := s[p : e+1]
		if end := strings.Index(s[e+1:], tag); end >= 0 {
			l.emit(String, e+1+end+len(tag))
			return
		}
		l.emit(Invalid, len(s))
		return
	}
	l.emit(Operator, p+1)
}

func (l *lexer) number() {
	s, p := l.src, l.pos
	e := p
	if s[e] == '0' && e+1 < len(s) && (s[e+1] == 'x' || s[e+1] == 'X') {
		e += 2
		for e < len(s) && strings.IndexByte("0123456789abcdefABCDEF", s[e]) >= 0 {
			e++
		}
		l.emit(Number, e)
		return
	}
	for e < len(s) && s[e] >= '0' && s[e] <= '9' {
		e++
	}
	if e < len(s) && s[e] == '.' {
		e++
		for e < len(s) && s[e] >= '0' && s[e] <= '9' {
			e++
		}
	}
	if e < len(s) && (s[e] == 'e' || s[e] == 'E') {
		f := e + 1
		if f < len(s) && (s[f] == '+' || s[f] == '-') {
			f++
		}
		if f < len(s) && s[f] >= '0' && s[f] <= '9' {
			e = f
			for e < len(s) && s[e] >= '0' && s[e] <= '9' {
				e++
			}
		}
	}
	l.emit(Number, e)
}
