package portable

import (
	"context"
	"fmt"
	"strings"

	"github.com/softdevstudio/baresql/internal/pgwire"
)

type pgEngine struct {
	c       *pgwire.Conn
	version string
}

// OpenPostgres connects and opens the transaction everything runs in. The
// script's tables go into a scratch schema that is first on the
// search_path, so they never meet tables of the same name; the
// transaction is rolled back at Close.
func OpenPostgres(ctx context.Context, cfg pgwire.Config) (Engine, error) {
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["application_name"] = "baresql compare"
	c, err := pgwire.Connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	e := &pgEngine{c: c, version: c.ServerVersion()}
	schema := scratchName()
	setup := `BEGIN;
		SET LOCAL TimeZone = 'UTC';
		SET LOCAL DateStyle = 'ISO, YMD';
		SET LOCAL IntervalStyle = 'postgres';
		SET LOCAL client_min_messages = warning;
		SET LOCAL statement_timeout = '60s'`
	if err := c.Exec(ctx, setup); err != nil {
		c.Close()
		return nil, fmt.Errorf("postgres: %w", err)
	}
	// A login that may not create schemas still gets the rolled-back
	// transaction, in its default schema.
	if err := c.Exec(ctx, "SAVEPOINT baresql_schema; CREATE SCHEMA "+schema+"; RELEASE SAVEPOINT baresql_schema"); err == nil {
		if err := c.Exec(ctx, "SELECT pg_catalog.set_config('search_path', '"+schema+", ' || pg_catalog.current_setting('search_path'), true)"); err != nil {
			c.Close()
			return nil, fmt.Errorf("postgres: %w", err)
		}
	} else if err := c.Exec(ctx, "ROLLBACK TO SAVEPOINT baresql_schema"); err != nil {
		c.Close()
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return e, nil
}

func (e *pgEngine) Name() string    { return Postgres }
func (e *pgEngine) Version() string { return e.version }

func (e *pgEngine) Run(ctx context.Context, stmt string) (*Answer, error) {
	if err := e.c.Exec(ctx, "SAVEPOINT baresql_stmt"); err != nil {
		return nil, err
	}
	res, err := e.c.Query(ctx, stmt)
	if err != nil {
		var pe *pgwire.Error
		if !asPgError(err, &pe) {
			return nil, err
		}
		if rerr := e.c.Exec(ctx, "ROLLBACK TO SAVEPOINT baresql_stmt"); rerr != nil {
			return nil, rerr
		}
		return &Answer{Err: pe.Message}, nil
	}
	if err := e.c.Exec(ctx, "RELEASE SAVEPOINT baresql_stmt"); err != nil {
		return nil, err
	}
	a := &Answer{}
	if len(res) == 0 {
		return a, nil
	}
	r := res[len(res)-1]
	for _, n := range r.Notices {
		if n.Severity == "WARNING" {
			a.Notes = append(a.Notes, n.Message)
		}
	}
	if r.Columns == nil {
		return a, nil
	}
	kinds := make([]Kind, len(r.Columns))
	for i, col := range r.Columns {
		a.Columns = append(a.Columns, col.Name)
		kinds[i] = pgKind(col.TypeOID)
	}
	a.Rows = [][]Cell{}
	for _, row := range r.Rows {
		cells := make([]Cell, len(row))
		for i, v := range row {
			if v.Null {
				cells[i] = Cell{Null: true, Kind: kinds[i]}
				continue
			}
			text := v.String()
			if kinds[i] == KindBytes {
				text = strings.TrimPrefix(text, `\x`)
			}
			cells[i] = Cell{Text: text, Kind: kinds[i]}
		}
		a.Rows = append(a.Rows, cells)
	}
	return a, nil
}

func asPgError(err error, target **pgwire.Error) bool {
	for err != nil {
		if pe, ok := err.(*pgwire.Error); ok {
			*target = pe
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func pgKind(oid uint32) Kind {
	switch oid {
	case 16:
		return KindBool
	case 20, 21, 23, 26, 28:
		return KindInt
	case 1700:
		return KindNumeric
	case 700, 701:
		return KindFloat
	case 1082:
		return KindDate
	case 1083, 1266:
		return KindTime
	case 1114, 1184:
		return KindTimestamp
	case 114, 3802:
		return KindJSON
	case 17:
		return KindBytes
	}
	return KindText
}

func (e *pgEngine) Close() error {
	e.c.Exec(context.Background(), "ROLLBACK")
	return e.c.Close()
}
