package portable

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/softdevstudio/baresql/internal/mysqlwire"
	"github.com/softdevstudio/baresql/internal/sqltext"
)

type myEngine struct {
	c       *mysqlwire.Conn
	cfg     mysqlwire.Config
	name    string
	version string
	db      string
	// others are the other databases on the server (lower case), which
	// no statement may name.
	others map[string]bool
}

// myStatementLimit bounds one statement on the server (MySQL limits only
// SELECT this way, MariaDB every statement); myClientLimit is the last
// resort for the rest, after which the session is killed.
const (
	myStatementLimit = 60 * time.Second
	myClientLimit    = 75 * time.Second
)

// OpenMySQL connects to MySQL or MariaDB and creates the scratch database
// the script runs in (utf8mb4, the server's default collation for it). It
// is dropped at Close. name is MySQL or MariaDB; the server must agree.
func OpenMySQL(ctx context.Context, cfg mysqlwire.Config, name string) (Engine, error) {
	c, err := mysqlwire.Connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if c.IsMariaDB() != (name == MariaDB) {
		c.Close()
		return nil, fmt.Errorf("%s: the server at that address is %s %s", name, map[bool]string{true: "MariaDB", false: "MySQL"}[c.IsMariaDB()], c.ServerVersion())
	}
	e := &myEngine{c: c, cfg: cfg, name: name, version: c.ServerVersion(), db: scratchName()}
	if _, err := c.Query(ctx, "CREATE DATABASE `"+e.db+"` CHARACTER SET utf8mb4"); err != nil {
		c.Close()
		return nil, fmt.Errorf("%s: creating the scratch database: %w", name, err)
	}
	if _, err := c.Query(ctx, "USE `"+e.db+"`"); err != nil {
		e.Close()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	res, err := c.Query(ctx, "SHOW DATABASES")
	if err != nil || len(res) == 0 {
		e.Close()
		return nil, fmt.Errorf("%s: listing the databases: %w", name, err)
	}
	e.others = map[string]bool{}
	for _, row := range res[0].Rows {
		if n := strings.ToLower(row[0].String()); n != strings.ToLower(e.db) && n != "information_schema" {
			e.others[n] = true
		}
	}
	limit := fmt.Sprintf("SET SESSION max_execution_time = %d", myStatementLimit.Milliseconds())
	if c.IsMariaDB() {
		limit = fmt.Sprintf("SET SESSION max_statement_time = %d", int(myStatementLimit.Seconds()))
	}
	c.Query(ctx, limit) // older servers lack the setting; the client limit still holds
	return e, nil
}

func (e *myEngine) Name() string    { return e.name }
func (e *myEngine) Version() string { return e.version }

func (e *myEngine) Run(ctx context.Context, stmt string) (*Answer, error) {
	// Changes on MySQL and MariaDB are not undone, so compare refuses every
	// statement that names another database, read the way the server reads
	// it (the code in /*! ... */ comments included).
	if db := otherDatabase(significant(sqltext.Tokenize(sqltext.MySQLCode(stmt))), e.others, e.localTable(ctx)); db != "" {
		return &Answer{Err: "BareSQL compare refuses statements that name another database (" + db + "): it works only in its own scratch database"}, nil
	}
	qctx, cancel := context.WithTimeout(ctx, myClientLimit)
	defer cancel()
	res, err := e.c.Query(qctx, stmt)
	if err != nil {
		var me *mysqlwire.Error
		if errors.As(err, &me) {
			return &Answer{Err: me.Message}, nil
		}
		return nil, err
	}
	a := &Answer{}
	var last *mysqlwire.Result
	warnings := 0
	for _, r := range res {
		warnings += int(r.Warnings)
		if r.Columns != nil {
			last = r
		}
	}
	if warnings > 0 {
		if w, err := e.c.Query(ctx, "SHOW WARNINGS"); err == nil && len(w) > 0 {
			for _, row := range w[0].Rows {
				if len(row) >= 3 {
					a.Notes = append(a.Notes, row[2].String())
				}
			}
		}
	}
	if last == nil {
		return a, nil
	}
	kinds := make([]Kind, len(last.Columns))
	for i, col := range last.Columns {
		a.Columns = append(a.Columns, col.Name)
		kinds[i] = myKind(col)
	}
	a.Rows = [][]Cell{}
	for _, row := range last.Rows {
		cells := make([]Cell, len(row))
		for i, v := range row {
			if v.Null {
				cells[i] = Cell{Null: true, Kind: kinds[i]}
				continue
			}
			text := string(v.Raw)
			if kinds[i] == KindBytes {
				text = hexString(v.Raw)
			}
			cells[i] = Cell{Text: text, Kind: kinds[i]}
		}
		a.Rows = append(a.Rows, cells)
	}
	return a, nil
}

func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(digits[c>>4])
		sb.WriteByte(digits[c&15])
	}
	return sb.String()
}

func myKind(c mysqlwire.Column) Kind {
	switch c.Type {
	case 1, 2, 3, 8, 9, 13: // TINY, SHORT, LONG, LONGLONG, INT24, YEAR
		return KindInt
	case 0, 246: // DECIMAL, NEWDECIMAL
		return KindNumeric
	case 4, 5: // FLOAT, DOUBLE
		return KindFloat
	case 10, 14: // DATE, NEWDATE
		return KindDate
	case 11:
		return KindTime
	case 7, 12: // TIMESTAMP, DATETIME
		return KindTimestamp
	case 245:
		return KindJSON
	case 16: // BIT
		return KindBytes
	}
	if c.IsBinary() {
		return KindBytes
	}
	return KindText
}

// localTable reports whether the scratch database holds a table or view
// of that name (asked only when a name is also another database's).
func (e *myEngine) localTable(ctx context.Context) func(string) bool {
	return func(name string) bool {
		res, err := e.c.Query(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND LOWER(table_name) = '"+strings.ReplaceAll(strings.ToLower(name), "'", "''")+"'")
		return err == nil && len(res) > 0 && len(res[0].Rows) > 0 && res[0].Rows[0][0].String() != "0"
	}
}

// Close drops the scratch database. When the session was lost (a statement
// ran past the client limit), a new one kills the old session first.
func (e *myEngine) Close() error {
	drop := "DROP DATABASE IF EXISTS `" + e.db + "`"
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := e.c.Query(ctx, drop)
	if err == nil {
		return e.c.Close()
	}
	var me *mysqlwire.Error
	if !errors.As(err, &me) {
		id := e.c.ConnectionID()
		e.c.Close()
		c, cerr := mysqlwire.Connect(ctx, e.cfg)
		if cerr != nil {
			return fmt.Errorf("could not drop the scratch database %s: %w", e.db, cerr)
		}
		defer c.Close()
		c.Query(ctx, fmt.Sprintf("KILL %d", id))
		c.Query(ctx, "SET SESSION lock_wait_timeout = 10")
		if _, err = c.Query(ctx, drop); err == nil {
			return nil
		}
	} else {
		e.c.Close()
	}
	return fmt.Errorf("could not drop the scratch database %s: %w", e.db, err)
}

// otherDatabase returns the first database in others that the statement
// names. A name right where a table goes (after FROM, JOIN, INTO, UPDATE,
// TABLE ... and in their comma lists) qualifies a table, so its qualifier
// is a database. Elsewhere x.y is a table's column, unless x is no table
// or alias of the statement and none of the scratch database (local); a
// third part, db.t.col, always names a database.
func otherDatabase(ts []sqltext.Token, others map[string]bool, local func(string) bool) string {
	isCreate := len(ts) > 0 && ts[0].Is("CREATE")
	tableAt := map[int]bool{}
	names := map[string]bool{} // tables and aliases the statement uses unqualified
	skip := map[string]bool{"IF": true, "NOT": true, "EXISTS": true, "ONLY": true, "IGNORE": true, "INTO": true,
		"LOW_PRIORITY": true, "DELAYED": true, "HIGH_PRIORITY": true, "QUICK": true, "LATERAL": true, "TEMPORARY": true}
	starts := map[string]bool{"FROM": true, "JOIN": true, "INTO": true, "UPDATE": true, "TABLE": true, "TABLES": true,
		"TRUNCATE": true, "REPLACE": true, "INSERT": true, "TO": true, "USING": true, "CALL": true, "DELETE": true}
	for i := 0; i < len(ts); i++ {
		w := ts[i].Upper()
		if ts[i].Kind != sqltext.Word || !(starts[w] || w == "ON" && isCreate) {
			continue
		}
		j := i + 1
		for {
			for j < len(ts) && ts[j].Kind == sqltext.Word && skip[ts[j].Upper()] {
				j++
			}
			if j >= len(ts) || !isName(ts[j]) {
				break
			}
			tableAt[j] = true
			k := j
			for k+2 < len(ts) && ts[k+1].IsOp(".") && isName(ts[k+2]) {
				k += 2
			}
			if k == j {
				names[strings.ToLower(identName(ts[j]))] = true
			}
			k++
			if k < len(ts) && ts[k].Is("AS") {
				k++
			}
			if k < len(ts) && isName(ts[k]) && !(ts[k].Kind == sqltext.Word && tableRefEnd[ts[k].Upper()]) {
				names[strings.ToLower(identName(ts[k]))] = true
				k++
			}
			if k < len(ts) && ts[k].IsOp(",") {
				j = k + 1
				continue
			}
			break
		}
	}
	for i := 0; i+2 < len(ts); i++ {
		if !isName(ts[i]) || !ts[i+1].IsOp(".") || i > 0 && ts[i-1].IsOp(".") {
			continue
		}
		n := identName(ts[i])
		x := strings.ToLower(n)
		if !others[x] {
			continue
		}
		threePart := i+4 < len(ts) && ts[i+3].IsOp(".") && isName(ts[i+4])
		if threePart || tableAt[i] || !names[x] && (local == nil || !local(x)) {
			return n
		}
	}
	return ""
}

// tableRefEnd: words that end a table reference instead of naming an alias.
var tableRefEnd = map[string]bool{
	"WHERE": true, "SET": true, "ON": true, "USING": true, "JOIN": true, "INNER": true, "LEFT": true, "RIGHT": true,
	"FULL": true, "CROSS": true, "NATURAL": true, "STRAIGHT_JOIN": true, "GROUP": true, "ORDER": true, "HAVING": true,
	"LIMIT": true, "UNION": true, "VALUES": true, "VALUE": true, "SELECT": true, "TO": true, "AS": true, "PARTITION": true,
	"WINDOW": true, "FOR": true, "LOCK": true, "INTO": true, "RETURNING": true, "DEFAULT": true, "FROM": true,
	"OUTER": true, "USE": true, "FORCE": true, "IGNORE": true, "WITH": true, "LIKE": true, "ENGINE": true,
}
