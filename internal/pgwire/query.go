package pgwire

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Result is what one statement returned. Every value is in text format.
type Result struct {
	Columns []Column
	Rows    [][]Value
	// Tag is the command tag, for example "SELECT 3" or "INSERT 0 1".
	Tag string
	// Notices are the warnings and notices the statement raised.
	Notices []*Error
}

// Column describes one result column (RowDescription).
type Column struct {
	Name     string
	TableOID uint32 // 0 when the column isn't a plain table column
	Attnum   int16
	TypeOID  uint32
	TypeSize int16
	TypeMod  int32
}

// Value is one field. Raw is the server's text output; Null is set for SQL
// NULL (Raw is then nil).
type Value struct {
	Null bool
	Raw  []byte
}

func (v Value) String() string { return string(v.Raw) }

// RowsAffected is the row count at the end of the command tag, or 0.
func (r *Result) RowsAffected() int64 {
	i := strings.LastIndexByte(r.Tag, ' ')
	if i < 0 {
		return 0
	}
	n, _ := strconv.ParseInt(r.Tag[i+1:], 10, 64)
	return n
}

// cancelGrace is how long a cancelled query may take to stop before the
// connection is given up.
const cancelGrace = 10 * time.Second

// Query runs sql with the simple query protocol. sql may hold several
// statements; there is one Result per statement that completed. On a
// server error the remaining statements don't run, and the error is a
// *Error. If ctx ends first, the server is asked to cancel the query; the
// error then wraps ctx.Err(), and the connection stays usable when the
// server stopped in time.
func (c *Conn) Query(ctx context.Context, sql string) ([]*Result, error) {
	if c.broken != nil {
		return nil, c.broken
	}
	if strings.IndexByte(sql, 0) >= 0 {
		return nil, errors.New("pgwire: query contains a NUL byte")
	}
	return c.run(ctx, begin('Q').str(sql).done())
}

// QueryArgs runs one statement with the extended protocol, passing args as
// text parameters $1, $2, ... A nil arg is NULL. Give parameters a type in
// the SQL when the server can't infer one ($1::oid).
func (c *Conn) QueryArgs(ctx context.Context, sql string, args ...any) (*Result, error) {
	if c.broken != nil {
		return nil, c.broken
	}
	if len(args) > 65535 {
		return nil, errors.New("pgwire: too many parameters")
	}
	if strings.IndexByte(sql, 0) >= 0 {
		return nil, errors.New("pgwire: query contains a NUL byte")
	}
	var out []byte
	out = append(out, begin('P').str("").str(sql).int16(0).done()...)
	b := begin('B').str("").str("").int16(0).int16(int16(len(args)))
	for i, a := range args {
		s, null, err := textArg(a)
		if err != nil {
			return nil, fmt.Errorf("pgwire: parameter $%d: %w", i+1, err)
		}
		if null {
			b.int32(-1)
		} else {
			b.int32(int32(len(s))).bytes([]byte(s))
		}
	}
	b.int16(0)
	out = append(out, b.done()...)
	out = append(out, begin('D').bytes([]byte{'P'}).str("").done()...)
	out = append(out, begin('E').str("").int32(0).done()...)
	out = append(out, begin('S').done()...)
	res, err := c.run(ctx, out)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return &Result{}, nil
	}
	return res[0], nil
}

// Exec runs sql (one or more statements) and keeps only the error.
func (c *Conn) Exec(ctx context.Context, sql string) error {
	_, err := c.Query(ctx, sql)
	return err
}

func textArg(a any) (s string, null bool, err error) {
	switch v := a.(type) {
	case nil:
		return "", true, nil
	case string:
		return v, false, nil
	case []byte:
		if v == nil {
			return "", true, nil
		}
		return string(v), false, nil
	case bool:
		if v {
			return "t", false, nil
		}
		return "f", false, nil
	case int:
		return strconv.FormatInt(int64(v), 10), false, nil
	case int16:
		return strconv.FormatInt(int64(v), 10), false, nil
	case int32:
		return strconv.FormatInt(int64(v), 10), false, nil
	case int64:
		return strconv.FormatInt(v, 10), false, nil
	case uint32:
		return strconv.FormatUint(uint64(v), 10), false, nil
	case uint64:
		return strconv.FormatUint(v, 10), false, nil
	}
	return "", false, fmt.Errorf("unsupported type %T", a)
}

// run sends a request and reads until ReadyForQuery.
func (c *Conn) run(ctx context.Context, req []byte) (results []*Result, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(fired)
		c.nc.SetDeadline(time.Now().Add(cancelGrace))
		c.cancelRequest()
	})
	defer func() {
		if !stop() {
			<-fired
			if c.broken == nil {
				c.nc.SetDeadline(time.Time{})
			}
			if err != nil {
				err = fmt.Errorf("pgwire: query stopped: %w (%w)", ctx.Err(), err)
			} else {
				err = fmt.Errorf("pgwire: query stopped: %w", ctx.Err())
			}
		}
	}()

	if err := c.write(req); err != nil {
		return nil, c.fail(err)
	}
	var (
		cur     *Result
		notices []*Error
		first   error
	)
	for {
		typ, body, err := c.read()
		if err != nil {
			return nil, c.fail(err)
		}
		switch typ {
		case 'T':
			cols, err := parseRowDescription(body)
			if err != nil {
				return nil, c.fail(err)
			}
			cur = &Result{Columns: cols}
		case 'D':
			if cur == nil {
				return nil, c.fail(errors.New("pgwire: data row without a row description"))
			}
			row, err := parseDataRow(body, len(cur.Columns))
			if err != nil {
				return nil, c.fail(err)
			}
			cur.Rows = append(cur.Rows, row)
		case 'C', 's':
			if cur == nil {
				cur = &Result{}
			}
			if typ == 'C' {
				r := &reader{b: body}
				cur.Tag = r.cstr()
			}
			cur.Notices, notices = notices, nil
			if first == nil {
				results = append(results, cur)
			}
			cur = nil
		case 'I', '1', '2', 'n', '3':
			// EmptyQueryResponse, ParseComplete, BindComplete, NoData,
			// CloseComplete: nothing to keep.
		case 'E':
			if first == nil {
				e := parseError(body)
				first = e
			}
			cur = nil
		case 'N':
			notices = append(notices, parseError(body))
		case 'S', 'A':
			c.handleAsync(typ, body)
		case 'G':
			// COPY FROM STDIN: BareSQL never sends data, so refuse.
			if err := c.write(begin('f').str("baresql does not send COPY data").done()); err != nil {
				return nil, c.fail(err)
			}
		case 'H':
			// COPY TO STDOUT: read and drop the data.
			serr, err := c.drainCopy()
			if err != nil {
				return nil, c.fail(err)
			}
			if serr != nil && first == nil {
				first = serr
			}
		case 'W':
			return nil, c.fail(errors.New("pgwire: the server started COPY BOTH, which is not supported"))
		case 'Z':
			c.status = firstByte(body)
			return results, first
		default:
			return nil, c.fail(fmt.Errorf("pgwire: unexpected message %q", typ))
		}
	}
}

// drainCopy reads COPY OUT data up to CopyDone. A server error ends the
// copy early; it is returned as serr, and ReadyForQuery still follows.
func (c *Conn) drainCopy() (serr *Error, err error) {
	for {
		typ, body, err := c.read()
		if err != nil {
			return nil, err
		}
		switch typ {
		case 'd': // CopyData
		case 'c': // CopyDone
			return nil, nil
		case 'E':
			return parseError(body), nil
		case 'N', 'S', 'A':
			c.handleAsync(typ, body)
		default:
			return nil, fmt.Errorf("pgwire: unexpected message %q during COPY", typ)
		}
	}
}

// fail marks the connection unusable after a protocol or network error and
// closes it, so the next call fails fast instead of reading garbage.
func (c *Conn) fail(err error) error {
	if c.broken == nil {
		c.broken = fmt.Errorf("pgwire: connection is unusable after an earlier error: %w", err)
		c.nc.Close()
	}
	return err
}

func parseRowDescription(body []byte) ([]Column, error) {
	r := &reader{b: body}
	n := int(r.int16())
	if n < 0 {
		return nil, errors.New("pgwire: negative column count")
	}
	// Each column takes at least 19 bytes; check before allocating.
	if n*19 > len(r.b) {
		return nil, errShort
	}
	cols := make([]Column, n)
	for i := range cols {
		cols[i] = Column{
			Name:     r.cstr(),
			TableOID: uint32(r.int32()),
			Attnum:   r.int16(),
			TypeOID:  uint32(r.int32()),
			TypeSize: r.int16(),
			TypeMod:  r.int32(),
		}
		r.int16() // format code: always text here
	}
	return cols, r.err
}

func parseDataRow(body []byte, want int) ([]Value, error) {
	r := &reader{b: body}
	n := int(r.int16())
	if r.err != nil {
		return nil, r.err
	}
	if n != want {
		return nil, fmt.Errorf("pgwire: data row has %d fields, the description %d", n, want)
	}
	row := make([]Value, n)
	for i := range row {
		l := r.int32()
		if l == -1 {
			row[i] = Value{Null: true}
			continue
		}
		v := r.next(int(l))
		if r.err != nil {
			return nil, r.err
		}
		row[i] = Value{Raw: v}
	}
	return row, r.err
}
