package portable

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/softdevstudio/baresql/internal/mysqlwire"
	"github.com/softdevstudio/baresql/internal/sqltext"
)

type myEngine struct {
	c       *mysqlwire.Conn
	name    string
	version string
	db      string
}

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
	e := &myEngine{c: c, name: name, version: c.ServerVersion(), db: scratchName()}
	if _, err := c.Query(ctx, "CREATE DATABASE `"+e.db+"` CHARACTER SET utf8mb4"); err != nil {
		c.Close()
		return nil, fmt.Errorf("%s: creating the scratch database: %w", name, err)
	}
	if _, err := c.Query(ctx, "USE `"+e.db+"`"); err != nil {
		e.Close()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return e, nil
}

func (e *myEngine) Name() string    { return e.name }
func (e *myEngine) Version() string { return e.version }

func (e *myEngine) Run(ctx context.Context, stmt string) (*Answer, error) {
	// DDL on MySQL and MariaDB is not undone, so compare refuses to change
	// tables outside its scratch database.
	if sts := sqltext.Split(stmt); len(sts) == 1 {
		if db, table := qualifiedTarget(sts[0].Significant()); db != "" && !strings.EqualFold(db, e.db) {
			return &Answer{Err: "BareSQL compare refuses to change " + db + "." + table + ": it changes only its own scratch database " + e.db}, nil
		}
	}
	res, err := e.c.Query(ctx, stmt)
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

func (e *myEngine) Close() error {
	e.c.Query(context.Background(), "DROP DATABASE IF EXISTS `"+e.db+"`")
	return e.c.Close()
}
