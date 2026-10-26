package sqltext

import "strings"

// MySQLCode returns the statement as MySQL and MariaDB read it, for checks
// that must see what those servers will run: comments are blanked out, the
// code inside executable comments (/*! ... */ and MariaDB's /*M! ... */) is
// kept without its markers, string literals become empty strings (their text never
// matters to such checks, and MySQL's backslash escapes decide where they
// end), and a -- that MySQL reads as two minus signs becomes "- -". The
// result keeps every line break, so token lines still match the source.
func MySQLCode(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	inExec := false // inside /*! ... */, whose text is code
	blank := func(s string) {
		for _, r := range s {
			if r == '\n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		}
	}
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case inExec && strings.HasPrefix(src[i:], "*/"):
			inExec = false
			b.WriteString("  ")
			i += 2
		case c == '/' && strings.HasPrefix(src[i:], "/*!"), c == '/' && strings.HasPrefix(src[i:], "/*M!"):
			j := i + 3
			if src[i+2] == 'M' {
				j++
			}
			for j < len(src) && src[j] >= '0' && src[j] <= '9' {
				j++
			}
			blank(src[i:j])
			i = j
			inExec = true
		case c == '/' && strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				blank(src[i:])
				return b.String()
			}
			blank(src[i : i+2+end+2])
			i += 2 + end + 2
		case c == '#', c == '-' && strings.HasPrefix(src[i:], "--") && (i+2 >= len(src) || src[i+2] <= ' '):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				blank(src[i:])
				return b.String()
			}
			blank(src[i : i+end])
			i += end
		case c == '-' && strings.HasPrefix(src[i:], "--"):
			b.WriteString("- -")
			i += 2
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(src) {
				if src[j] == '\\' {
					j += 2
					continue
				}
				if src[j] == c {
					if j+1 < len(src) && src[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(src) {
				// Unterminated: the server refuses the statement.
				blank(src[i:])
				return b.String()
			}
			b.WriteString("''")
			blank(src[i+2 : j+1])
			i = j + 1
		case c == '`':
			j := i + 1
			for j < len(src) {
				if src[j] == '`' {
					if j+1 < len(src) && src[j+1] == '`' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(src) {
				blank(src[i:])
				return b.String()
			}
			b.WriteString(src[i : j+1])
			i = j + 1
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}
