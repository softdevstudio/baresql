package portable

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SQLite runs through the sqlite3 program, so BareSQL needs no C compiler
// and no SQLite inside it, and the answers are those of the SQLite the
// user has. One sqlite3 process serves the whole script: after each
// statement BareSQL prints a marker on standard output (.print) and causes
// one on standard error (an unknown dot command naming the statement), so
// it knows where each statement's output and errors end.
//
// sqlite3 runs with -safe, which refuses ATTACH, VACUUM INTO, readfile(),
// writefile(), load_extension() and the dot commands that reach files or
// run programs: a script can change the scratch database and nothing else.
// The shell quits on such a statement; BareSQL reports the refusal and
// starts it again on the same database file.

// MinSQLite is the oldest sqlite3 compare works with (-safe came in 3.37).
const MinSQLite = "3.37.0"

type sqliteEngine struct {
	program string
	db      string
	dir     string
	version string
	p       *sqliteProc
	n       int
	mu      sync.Mutex
	broken  error
}

type sqliteProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
	errs  chan string
}

// FindSQLite returns the sqlite3 program to use: the given path, or
// sqlite3 on PATH.
func FindSQLite(program string) (string, error) {
	if program != "" {
		return program, nil
	}
	p, err := exec.LookPath("sqlite3")
	if err != nil {
		return "", errors.New("sqlite: no sqlite3 program on PATH (install SQLite's command-line shell, or name it with --sqlite-program)")
	}
	return p, nil
}

// OpenSQLite starts sqlite3 on a new temporary database file. With source
// set, the file starts as a copy of that database (and of its -wal file);
// the original is never opened.
func OpenSQLite(ctx context.Context, program, source string) (Engine, error) {
	program, err := FindSQLite(program)
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, program, "-version").Output()
	if err != nil {
		return nil, fmt.Errorf("sqlite: running %s -version: %w", program, err)
	}
	version := strings.TrimSpace(string(out))
	if v := firstField(version); versionLess(v, MinSQLite) {
		return nil, fmt.Errorf("sqlite: %s is SQLite %s; compare needs %s or later (for the shell's -safe mode)", program, v, MinSQLite)
	}
	dir, err := os.MkdirTemp("", "baresql-sqlite-")
	if err != nil {
		return nil, err
	}
	db := filepath.Join(dir, "compare.db")
	if source != "" {
		if err := copyFile(source, db); err != nil {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("sqlite: copying %s: %w", source, err)
		}
		if _, err := os.Stat(source + "-wal"); err == nil {
			if err := copyFile(source+"-wal", db+"-wal"); err != nil {
				os.RemoveAll(dir)
				return nil, fmt.Errorf("sqlite: copying %s-wal: %w", source, err)
			}
		}
	}
	e := &sqliteEngine{program: program, db: db, dir: dir, version: version}
	if err := e.start(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return e, nil
}

func (e *sqliteEngine) start() error {
	cmd := exec.Command(e.program, "-safe", "-batch", e.db)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("sqlite: starting %s: %w", e.program, err)
	}
	p := &sqliteProc{cmd: cmd, stdin: stdin, out: bufio.NewReader(stdout), errs: make(chan string, 64)}
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			p.errs <- sc.Text()
		}
		close(p.errs)
	}()
	// Quote mode prints every value as an SQL literal, which keeps types
	// apart (1, 1.0, '1', X'01', NULL) and survives newlines in text.
	if _, err := io.WriteString(stdin, ".headers on\n.mode quote\n"); err != nil {
		p.stop()
		return err
	}
	e.p = p
	return nil
}

func (p *sqliteProc) stop() {
	p.stdin.Close()
	waited := make(chan struct{})
	go func() {
		p.cmd.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		p.cmd.Process.Kill()
		<-waited
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (e *sqliteEngine) Name() string    { return SQLite }
func (e *sqliteEngine) Version() string { return firstField(e.version) }

func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}

// statementTimeout bounds one statement; sqlite3 has no timeout of its own.
const statementTimeout = 60 * time.Second

func (e *sqliteEngine) Run(ctx context.Context, stmt string) (*Answer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.broken != nil {
		return nil, e.broken
	}
	if why := sqliteRefusal(stmt); why != "" {
		return &Answer{Err: why}, nil
	}
	e.n++
	outMark := fmt.Sprintf("__baresql_end_%d__", e.n)
	errMark := fmt.Sprintf("baresql_end_%d", e.n)
	input := stmt + "\n;\n.print " + outMark + "\n." + errMark + "\n"
	p := e.p
	if _, err := io.WriteString(p.stdin, input); err != nil {
		return e.exited(p)
	}
	type result struct {
		out    string
		errs   []string
		exited bool
	}
	done := make(chan result, 1)
	go func() {
		var r result
		var out strings.Builder
		for {
			line, err := p.out.ReadString('\n')
			if err != nil {
				r.exited = true
				done <- r
				return
			}
			if strings.TrimRight(line, "\r\n") == outMark {
				break
			}
			out.WriteString(line)
		}
		r.out = out.String()
		for line := range p.errs {
			if strings.Contains(line, `"`+errMark+`"`) {
				done <- r
				return
			}
			r.errs = append(r.errs, line)
		}
		r.exited = true
		done <- r
	}()
	timer := time.NewTimer(statementTimeout)
	defer timer.Stop()
	var r result
	select {
	case r = <-done:
	case <-ctx.Done():
		return nil, e.fail(ctx.Err())
	case <-timer.C:
		return nil, e.fail(fmt.Errorf("sqlite: no answer after %v", statementTimeout))
	}
	if r.exited {
		return e.exited(p)
	}
	a := &Answer{}
	if len(r.errs) > 0 {
		a.Err = cleanSQLiteError(r.errs)
		return a, nil
	}
	cols, rows, err := parseQuoteMode(r.out)
	if err != nil {
		return nil, e.fail(fmt.Errorf("sqlite: reading the output: %w", err))
	}
	if cols != nil {
		a.Columns, a.Rows = cols, rows
	}
	return a, nil
}

// exited handles a sqlite3 that quit: after a safe-mode refusal the
// statement's answer is that refusal and a new sqlite3 takes over the same
// database file; anything else ends the engine.
func (e *sqliteEngine) exited(p *sqliteProc) (*Answer, error) {
	var lines []string
	for line := range p.errs {
		lines = append(lines, line)
	}
	p.stop()
	for _, l := range lines {
		if strings.Contains(l, "in safe mode") {
			if err := e.start(); err != nil {
				return nil, e.fail(err)
			}
			return &Answer{Err: cleanSQLiteError([]string{l}) + " (BareSQL runs sqlite3 in safe mode: a script reaches the scratch database only)"}, nil
		}
	}
	msg := "sqlite: sqlite3 stopped"
	if len(lines) > 0 {
		msg += ": " + strings.Join(lines, " ")
	}
	e.broken = errors.New(msg)
	return nil, e.broken
}

// cleanSQLiteError drops the "Parse error near line N:" prefix and the
// lines that point at the error, which depend on how BareSQL fed the shell.
func cleanSQLiteError(lines []string) string {
	msg := lines[0]
	for _, p := range []string{"Parse error", "Runtime error", "Error", "line "} {
		if strings.HasPrefix(msg, p) {
			if i := strings.Index(msg, ": "); i >= 0 {
				msg = msg[i+2:]
			}
			break
		}
	}
	return strings.TrimSpace(msg)
}

func (e *sqliteEngine) fail(err error) error {
	if e.broken == nil {
		e.broken = err
		if e.p != nil && e.p.cmd.Process != nil {
			e.p.cmd.Process.Kill()
		}
	}
	return err
}

func (e *sqliteEngine) Close() error {
	if e.p != nil {
		io.WriteString(e.p.stdin, ".quit\n")
		e.p.stop()
	}
	return os.RemoveAll(e.dir)
}

// sqliteRefusal says why a statement can't go to the sqlite3 shell as one
// statement, or returns "". The shell reads its input line by line and runs
// what it has as soon as SQLite's own rules (sqlite3_complete) say the
// statement is complete; a line that starts a new statement with a dot is
// one of the shell's commands. The statement must be complete only at its
// very end, or the shell would split it differently from the other engines.
func sqliteRefusal(stmt string) string {
	t := strings.TrimLeft(stmt, " \t\r\n\f\v")
	if strings.HasPrefix(t, ".") || strings.HasPrefix(t, "#") {
		return "the sqlite3 program reads a line that starts with " + t[:1] + " as one of its own commands"
	}
	lines := strings.Split(stmt, "\n")
	if commandTerminator(lines[0]) {
		return "the sqlite3 program reads a line holding only / or GO as the end of a statement"
	}
	// The shell hands SQLite the text once it is complete, and SQLite runs
	// every statement in it, so a semicolon that ends a statement by
	// SQLite's rules anywhere before the end means more than one: a # or a
	// backslash that BareSQL read as a comment or an escape can hide one.
	for i := 0; i < len(stmt); i++ {
		if stmt[i] == ';' && sqliteComplete(stmt[:i+1]) && !sqliteBlank(stmt[i+1:]) {
			return "SQLite reads this as more than one statement: a semicolon that another engine reads inside a comment or string ends it early"
		}
	}
	for i := 1; i < len(lines); i++ {
		prefix := strings.Join(lines[:i], "\n")
		if commandTerminator(lines[i]) && sqliteComplete(prefix+"\n;") {
			return "the sqlite3 program reads a line holding only / or GO as the end of a statement"
		}
	}
	if !sqliteComplete(stmt + "\n;") {
		return "incomplete as SQLite reads it: a string, name or comment that does not end, or a CREATE TRIGGER without ; END"
	}
	return ""
}

// sqliteBlank reports whether s holds only spaces and comments, as SQLite
// reads them.
func sqliteBlank(s string) bool {
	for {
		s = strings.TrimLeft(s, " \t\r\n\f")
		switch {
		case s == "":
			return true
		case strings.HasPrefix(s, "--"):
			nl := strings.IndexByte(s, '\n')
			if nl < 0 {
				return true
			}
			s = s[nl+1:]
		case strings.HasPrefix(s, "/*"):
			end := strings.Index(s[2:], "*/")
			if end < 0 {
				return true
			}
			s = s[2+end+2:]
		default:
			return false
		}
	}
}

// commandTerminator: the shell takes a line holding only "/" or "GO"
// (and spaces or comments) as a semicolon.
func commandTerminator(line string) bool {
	s := strings.TrimLeft(line, " \t\r\f\v")
	switch {
	case strings.HasPrefix(s, "/"):
		s = s[1:]
	case len(s) >= 2 && strings.EqualFold(s[:2], "go"):
		s = s[2:]
	default:
		return false
	}
	for {
		s = strings.TrimLeft(s, " \t\r\n\f\v")
		switch {
		case s == "", strings.HasPrefix(s, "--"):
			return true
		case strings.HasPrefix(s, "/*"):
			end := strings.Index(s[2:], "*/")
			if end < 0 {
				return false
			}
			s = s[2+end+2:]
		default:
			return false
		}
	}
}

// sqliteComplete is SQLite's sqlite3_complete: whether the text ends a
// statement (a semicolon outside strings and comments, or ; END ; after
// CREATE TRIGGER).
func sqliteComplete(z string) bool {
	const (
		tkSEMI = iota
		tkWS
		tkOTHER
		tkEXPLAIN
		tkCREATE
		tkTEMP
		tkTRIGGER
		tkEND
	)
	trans := [8][8]int{
		/* INVALID */ {1, 0, 2, 3, 4, 2, 2, 2},
		/* START   */ {1, 1, 2, 3, 4, 2, 2, 2},
		/* NORMAL  */ {1, 2, 2, 2, 2, 2, 2, 2},
		/* EXPLAIN */ {1, 3, 3, 2, 4, 2, 2, 2},
		/* CREATE  */ {1, 4, 2, 2, 2, 4, 5, 2},
		/* TRIGGER */ {6, 5, 5, 5, 5, 5, 5, 5},
		/* SEMI    */ {6, 6, 5, 5, 5, 5, 5, 7},
		/* END     */ {1, 7, 5, 5, 5, 5, 5, 5},
	}
	idChar := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$' || c >= 0x80
	}
	state := 0
	i := 0
	for i < len(z) {
		var tok int
		c := z[i]
		switch {
		case c == ';':
			tok, i = tkSEMI, i+1
		case c == ' ' || c == '\r' || c == '\t' || c == '\n' || c == '\f':
			tok, i = tkWS, i+1
		case c == '/' && i+1 < len(z) && z[i+1] == '*':
			end := strings.Index(z[i+2:], "*/")
			if end < 0 {
				return false
			}
			tok, i = tkWS, i+2+end+2
		case c == '-' && i+1 < len(z) && z[i+1] == '-':
			nl := strings.IndexByte(z[i:], '\n')
			if nl < 0 {
				return state == 1
			}
			tok, i = tkWS, i+nl+1
		case c == '[':
			end := strings.IndexByte(z[i+1:], ']')
			if end < 0 {
				return false
			}
			tok, i = tkOTHER, i+1+end+1
		case c == '`' || c == '"' || c == '\'':
			end := strings.IndexByte(z[i+1:], c)
			if end < 0 {
				return false
			}
			tok, i = tkOTHER, i+1+end+1
		case idChar(c):
			j := i + 1
			for j < len(z) && idChar(z[j]) {
				j++
			}
			switch strings.ToLower(z[i:j]) {
			case "create":
				tok = tkCREATE
			case "trigger":
				tok = tkTRIGGER
			case "temp", "temporary":
				tok = tkTEMP
			case "end":
				tok = tkEND
			case "explain":
				tok = tkEXPLAIN
			default:
				tok = tkOTHER
			}
			i = j
		default:
			tok, i = tkOTHER, i+1
		}
		state = trans[state][tok]
	}
	return state == 1
}

// versionLess compares dotted version numbers.
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// parseQuoteMode reads sqlite3's quote mode with headers: a line of quoted
// column names, then one line of SQL literals per row (a text value may
// hold newlines). Empty output means no rows and no column names.
func parseQuoteMode(out string) (cols []string, rows [][]Cell, err error) {
	if out == "" {
		return nil, nil, nil
	}
	p := &quoteParser{s: out}
	header, err := p.row()
	if err != nil {
		return nil, nil, err
	}
	for _, h := range header {
		cols = append(cols, h.Text)
	}
	rows = [][]Cell{}
	for !p.done() {
		r, err := p.row()
		if err != nil {
			return nil, nil, err
		}
		if len(r) != len(cols) {
			return nil, nil, fmt.Errorf("a row has %d values for %d columns", len(r), len(cols))
		}
		rows = append(rows, r)
	}
	return cols, rows, nil
}

type quoteParser struct {
	s   string
	pos int
}

func (p *quoteParser) done() bool { return p.pos >= len(p.s) }

// row reads literals separated by commas up to the end of the line.
func (p *quoteParser) row() ([]Cell, error) {
	var out []Cell
	for {
		c, err := p.literal()
		if err != nil {
			return nil, err
		}
		out = append(out, c)
		if p.done() {
			return out, nil
		}
		switch p.s[p.pos] {
		case ',':
			p.pos++
		case '\n':
			p.pos++
			return out, nil
		case '\r':
			p.pos++
			if !p.done() && p.s[p.pos] == '\n' {
				p.pos++
			}
			return out, nil
		default:
			return nil, fmt.Errorf("unexpected %q at offset %d", p.s[p.pos], p.pos)
		}
	}
}

func (p *quoteParser) literal() (Cell, error) {
	s := p.s
	switch {
	case p.done():
		return Cell{}, errors.New("missing value")
	case s[p.pos] == '\'':
		var b strings.Builder
		i := p.pos + 1
		for i < len(s) {
			if s[i] == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte('\'')
					i += 2
					continue
				}
				p.pos = i + 1
				return Cell{Text: b.String(), Kind: KindText}, nil
			}
			b.WriteByte(s[i])
			i++
		}
		return Cell{}, errors.New("unterminated text value")
	case strings.HasPrefix(s[p.pos:], "X'"):
		end := strings.IndexByte(s[p.pos+2:], '\'')
		if end < 0 {
			return Cell{}, errors.New("unterminated blob value")
		}
		c := Cell{Text: strings.ToLower(s[p.pos+2 : p.pos+2+end]), Kind: KindBytes}
		p.pos += 3 + end
		return c, nil
	case strings.HasPrefix(s[p.pos:], "NULL"):
		p.pos += 4
		return Cell{Null: true}, nil
	}
	i := p.pos
	for i < len(s) && s[i] != ',' && s[i] != '\n' && s[i] != '\r' {
		i++
	}
	tok := s[p.pos:i]
	p.pos = i
	if strings.ContainsAny(tok, ".eE") || strings.Contains(tok, "Inf") {
		return Cell{Text: shortFloat(tok), Kind: KindFloat}, nil
	}
	return Cell{Text: tok, Kind: KindInt}, nil
}

// shortFloat rewrites the shell's 17-digit floats (0.300000000000000044)
// as the shortest text that reads back as the same double
// (0.30000000000000004), the way PostgreSQL, MySQL and most clients print
// them. Whole numbers keep their ".0".
func shortFloat(tok string) string {
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil || strings.Contains(tok, "Inf") {
		return tok
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eIN") {
		s += ".0"
	}
	return s
}
