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
	if db := otherDatabase(significant(sqltext.Tokenize(sqltext.MySQLCode(stmt))), e.others); db != "" {
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

// otherDatabase returns the first database in others that the tokens name
// as the qualifier of a table, view or routine (db.t, `db`.`t`, db.t.col),
// or "".
func otherDatabase(ts []sqltext.Token, others map[string]bool) string {
	for i := 0; i+2 < len(ts); i++ {
		if isName(ts[i]) && ts[i+1].IsOp(".") && (i == 0 || !ts[i-1].IsOp(".")) {
			if n := identName(ts[i]); others[strings.ToLower(n)] {
				return n
			}
		}
	}
	return ""
}
