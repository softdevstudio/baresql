package audit

import (
	"context"
	"fmt"
	"strings"

	"github.com/softdevstudio/baresql/internal/pgwire"
)

// Sampling reads up to N rows of each table and materialized view, inside
// the read-only transaction, to see what the columns really hold. Values
// stay in memory only while their table is being classified; reports carry
// counts, never values.
//
// What is never read: views (reading one runs its query, which can call
// functions), foreign tables (reading one connects to another server),
// partitions (their partitioned table is read instead), binary columns,
// virtual generated columns, and types whose text output isn't C code.

// Value limits per column: enough for an address or a token, and for a
// JSON document's first few kilobytes.
const (
	textLimit = 256
	jsonLimit = 4096
	maxCols   = 1000 // columns per sampling query (PostgreSQL allows 1664)
)

func quoteAlways(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// sampleable lists the columns of rel that sampling reads.
func sampleable(rel *Relation) []*Column {
	if rel.Kind != 'r' && rel.Kind != 'p' && rel.Kind != 'm' || rel.IsPartition {
		return nil
	}
	var out []*Column
	for _, c := range rel.Columns {
		if c.Readable && !c.Binary && c.Generated != 'v' {
			out = append(out, c)
		}
	}
	return out
}

// sampleRelation reads up to rows rows of the given columns. A failure (no privilege, row
// security on the auditor, a timeout) is returned as an error and leaves
// the transaction usable: each read runs in its own savepoint.
func sampleRelation(ctx context.Context, c *pgwire.Conn, s *Snapshot, rel *Relation, cols []*Column, rows int) (map[int]*valueStats, error) {
	out := map[int]*valueStats{}
	for start := 0; start < len(cols); start += maxCols {
		part := cols[start:min(start+maxCols, len(cols))]
		var exprs []string
		for _, col := range part {
			limit := textLimit
			if col.JSON {
				limit = jsonLimit
			}
			exprs = append(exprs, fmt.Sprintf("pg_catalog.left(%s::pg_catalog.text, %d)", quoteAlways(col.Name), limit))
		}
		schema := s.Schemas[rel.Schema].Name
		sql := fmt.Sprintf("SELECT %s FROM %s.%s LIMIT %d", strings.Join(exprs, ", "),
			quoteAlways(schema), quoteAlways(rel.Name), rows)
		res, err := savepointQuery(ctx, c, sql)
		if err != nil {
			return nil, err
		}
		for _, col := range part {
			out[col.Attnum] = newStats()
		}
		for _, row := range res.Rows {
			for i, col := range part {
				if i < len(row) && !row[i].Null {
					out[col.Attnum].add(row[i].String(), col.JSON)
				}
			}
		}
	}
	return out, nil
}

func savepointQuery(ctx context.Context, c *pgwire.Conn, sql string) (*pgwire.Result, error) {
	if err := c.Exec(ctx, "SAVEPOINT baresql_sample"); err != nil {
		return nil, err
	}
	res, err := c.QueryArgs(ctx, sql)
	if err != nil {
		if rerr := c.Exec(ctx, "ROLLBACK TO SAVEPOINT baresql_sample; RELEASE SAVEPOINT baresql_sample"); rerr != nil {
			return nil, rerr
		}
		return nil, err
	}
	if err := c.Exec(ctx, "RELEASE SAVEPOINT baresql_sample"); err != nil {
		return nil, err
	}
	return res, nil
}

// sampleError explains in plain words why a relation wasn't sampled.
func sampleError(err error) string {
	switch {
	case pgwire.IsCode(err, "42501"):
		return "the auditor's login may not read it"
	case pgwire.IsCode(err, "57014"):
		return "reading the sample took longer than the statement timeout"
	case pgwire.IsCode(err, "55P03"):
		return "the table was locked by another session"
	case pgwire.IsCode(err, "55000"):
		return "the materialized view has not been populated"
	}
	if e, ok := err.(*pgwire.Error); ok && strings.Contains(e.Message, "row-level security") {
		return "row security applies to the auditor's login"
	}
	return err.Error()
}
