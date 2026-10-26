package mysqlwire

import (
	"encoding/binary"
	"fmt"
	"strconv"
)

const (
	comQuit  = 0x01
	comQuery = 0x03

	// serverMoreResultsExists is the status flag that announces another
	// result after this one.
	serverMoreResultsExists = 0x0008

	// charsetBinary is the collation id of the binary character set.
	charsetBinary = 63
)

// Result is one result of a statement: a result set with Columns and
// Rows, or the outcome of a statement that returns no rows.
type Result struct {
	Columns      []Column
	Rows         [][]Value
	AffectedRows uint64
	LastInsertID uint64
	Warnings     uint16
	Status       uint16 // SERVER_STATUS_* flags
}

// Column describes one column of a result set.
type Column struct {
	Schema, Table, OrgTable, Name, OrgName string

	Charset  uint16 // collation id; 63 is binary
	Length   uint32 // maximum length of a value
	Type     byte   // MYSQL_TYPE_* code
	Flags    uint16 // NOT_NULL_FLAG, UNSIGNED_FLAG, BINARY_FLAG, ...
	Decimals uint8
}

// TypeName returns the MYSQL_TYPE_* name of Type without the prefix, or
// TYPE_<n> for a code not listed here.
func (c Column) TypeName() string {
	switch c.Type {
	case 0:
		return "DECIMAL"
	case 1:
		return "TINY"
	case 2:
		return "SHORT"
	case 3:
		return "LONG"
	case 4:
		return "FLOAT"
	case 5:
		return "DOUBLE"
	case 6:
		return "NULL"
	case 7:
		return "TIMESTAMP"
	case 8:
		return "LONGLONG"
	case 9:
		return "INT24"
	case 10:
		return "DATE"
	case 11:
		return "TIME"
	case 12:
		return "DATETIME"
	case 13:
		return "YEAR"
	case 15:
		return "VARCHAR"
	case 16:
		return "BIT"
	case 245:
		return "JSON"
	case 246:
		return "NEWDECIMAL"
	case 247:
		return "ENUM"
	case 248:
		return "SET"
	case 249:
		return "TINY_BLOB"
	case 250:
		return "MEDIUM_BLOB"
	case 251:
		return "LONG_BLOB"
	case 252:
		return "BLOB"
	case 253:
		return "VAR_STRING"
	case 254:
		return "STRING"
	case 255:
		return "GEOMETRY"
	}
	return "TYPE_" + strconv.Itoa(int(c.Type))
}

// IsBinary reports whether the column carries bytes rather than text: a
// string or blob type (VARCHAR, VAR_STRING, STRING, ENUM, SET, the BLOB
// types, GEOMETRY) in the binary character set. Numeric and temporal
// columns report charset 63 as well but hold text. BIT values are raw
// bytes too; BIT is not a string type, so check Type for it.
func (c Column) IsBinary() bool {
	if c.Charset != charsetBinary {
		return false
	}
	switch c.Type {
	case 15, 247, 248, 249, 250, 251, 252, 253, 254, 255:
		return true
	}
	return false
}

// Value is one field of a row.
type Value struct {
	Null bool
	Raw  []byte // the text-protocol bytes exactly as the server sent them
}

// String returns the value as text, or "NULL" for SQL NULL.
func (v Value) String() string {
	if v.Null {
		return "NULL"
	}
	return string(v.Raw)
}

// Error is a server ERR packet. The Conn stays usable after one.
type Error struct {
	Code     uint16
	SQLState string // "HY000" when the server sent none
	Message  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("ERROR %d (%s): %s", e.Code, e.SQLState, e.Message)
}

// parseErr decodes an ERR packet. Before the login the server cannot know
// whether the client speaks protocol 4.1, so it leaves out the '#' and
// SQL state; the client then reports HY000, as libmysqlclient does.
func parseErr(b []byte) (*Error, error) {
	if len(b) < 3 || b[0] != 0xFF {
		return nil, errMalformed("ERR")
	}
	e := &Error{Code: binary.LittleEndian.Uint16(b[1:3]), SQLState: "HY000"}
	msg := b[3:]
	if len(msg) >= 6 && msg[0] == '#' {
		e.SQLState, msg = string(msg[1:6]), msg[6:]
	}
	e.Message = string(msg)
	return e, nil
}

// okPacket holds the fields of an OK packet this package uses. The info
// text that may follow them is left unread.
type okPacket struct {
	affectedRows, lastInsertID uint64
	status, warnings           uint16
}

// parseOK decodes an OK packet, whose header is 0x00, or 0xFE where it
// ends a result set under CLIENT_DEPRECATE_EOF.
func parseOK(b []byte) (okPacket, error) {
	c := cursor{b: b}
	c.u8()
	ok := okPacket{affectedRows: c.lenencInt(), lastInsertID: c.lenencInt()}
	ok.status = c.u16()
	ok.warnings = c.u16()
	if c.bad {
		return okPacket{}, errMalformed("OK")
	}
	return ok, nil
}

// parseEOF decodes an EOF packet: 0xFE, the warning count, status flags.
func parseEOF(b []byte) (status, warnings uint16, err error) {
	c := cursor{b: b}
	c.u8()
	warnings = c.u16()
	status = c.u16()
	if c.bad {
		return 0, 0, errMalformed("EOF")
	}
	return status, warnings, nil
}

// parseColumnDef decodes a ColumnDefinition41 packet.
func parseColumnDef(b []byte) (Column, error) {
	c := cursor{b: b}
	c.lenencBytes() // catalog, always "def"
	col := Column{
		Schema:   string(c.lenencBytes()),
		Table:    string(c.lenencBytes()),
		OrgTable: string(c.lenencBytes()),
		Name:     string(c.lenencBytes()),
		OrgName:  string(c.lenencBytes()),
	}
	// The fixed-length fields: 10 bytes, then 2 bytes of filler.
	fixed := c.lenencInt()
	if fixed < 10 || fixed > uint64(len(c.b)) {
		return Column{}, errMalformed("column definition")
	}
	col.Charset = c.u16()
	col.Length = c.u32()
	col.Type = c.u8()
	col.Flags = c.u16()
	col.Decimals = c.u8()
	c.take(int(fixed) - 10)
	if c.bad {
		return Column{}, errMalformed("column definition")
	}
	return col, nil
}

// parseRow decodes a text-protocol row of n values: length-encoded
// strings, with 0xFB for NULL. Raw slices point into b.
func parseRow(b []byte, n int) ([]Value, error) {
	c := cursor{b: b}
	row := make([]Value, n)
	for i := range row {
		if len(c.b) > 0 && c.b[0] == 0xFB {
			c.take(1)
			row[i].Null = true
			continue
		}
		row[i].Raw = c.lenencBytes()
	}
	if c.bad || len(c.b) != 0 {
		return nil, errMalformed("row")
	}
	return row, nil
}

// isTerminator reports whether pkt ends a result set. A row starts with
// 0xFE as well when its first value is 16 MiB or longer, but such a row
// fills at least one whole packet, so the length tells them apart.
func isTerminator(pkt []byte) bool {
	return len(pkt) > 0 && pkt[0] == 0xFE && len(pkt) < maxPacketPayload
}

// query sends sql as COM_QUERY and reads every result. I/O and protocol
// errors break the connection; ERR packets leave it in step.
func (c *Conn) query(sql string) ([]*Result, error) {
	c.pio.seq = 0
	if err := c.writePacket(append([]byte{comQuery}, sql...)); err != nil {
		return nil, err
	}
	var results []*Result
	var refused error
	for {
		pkt, err := c.readPacket()
		if err != nil {
			return nil, err
		}
		if pkt[0] == 0xFB {
			// LOAD DATA LOCAL INFILE wants a file from the client. An
			// empty packet marks the end of an empty file, after which the
			// server finishes the statement; report the refusal then.
			refused = fmt.Errorf("mysqlwire: refused the server's request to read local file %q", pkt[1:])
			if err := c.writePacket(nil); err != nil {
				return nil, err
			}
			continue
		}
		res, more, err := c.readResult(pkt)
		if err != nil {
			if refused != nil {
				return nil, fmt.Errorf("%w: %w", refused, err)
			}
			return nil, err
		}
		results = append(results, res)
		if !more {
			break
		}
	}
	if refused != nil {
		return nil, refused
	}
	return results, nil
}

// readResult reads one result whose first packet is first: an OK packet
// or a result set. The bool reports that another result follows.
func (c *Conn) readResult(first []byte) (*Result, bool, error) {
	switch first[0] {
	case 0x00:
		ok, err := parseOK(first)
		if err != nil {
			return nil, false, c.fail(err)
		}
		res := &Result{AffectedRows: ok.affectedRows, LastInsertID: ok.lastInsertID, Warnings: ok.warnings, Status: ok.status}
		return res, ok.status&serverMoreResultsExists != 0, nil
	case 0xFF:
		return nil, false, c.serverError(first)
	}
	cur := cursor{b: first}
	n := cur.lenencInt()
	if cur.bad || len(cur.b) != 0 || n == 0 {
		return nil, false, c.fail(errMalformed("column count"))
	}
	// Each column costs the server a packet, so n cannot outrun the data;
	// it only must not size an allocation up front.
	res := &Result{Columns: make([]Column, 0, min(n, 4096))}
	for range n {
		pkt, err := c.readPacket()
		if err != nil {
			return nil, false, err
		}
		col, err := parseColumnDef(pkt)
		if err != nil {
			return nil, false, c.fail(err)
		}
		res.Columns = append(res.Columns, col)
	}
	if c.caps&clientDeprecateEOF == 0 {
		// Without CLIENT_DEPRECATE_EOF an EOF packet ends the column list.
		pkt, err := c.readPacket()
		if err != nil {
			return nil, false, err
		}
		if !isTerminator(pkt) {
			return nil, false, c.fail(errMalformed("EOF"))
		}
	}
	for {
		pkt, err := c.readPacket()
		if err != nil {
			return nil, false, err
		}
		if isTerminator(pkt) {
			status, warnings, err := c.parseTerminator(pkt)
			if err != nil {
				return nil, false, c.fail(err)
			}
			res.Status, res.Warnings = status, warnings
			return res, status&serverMoreResultsExists != 0, nil
		}
		if pkt[0] == 0xFF {
			// The statement failed while sending rows, killed or timed
			// out say; the ERR packet ends the response.
			return nil, false, c.serverError(pkt)
		}
		row, err := parseRow(pkt, len(res.Columns))
		if err != nil {
			return nil, false, c.fail(err)
		}
		res.Rows = append(res.Rows, row)
	}
}

// parseTerminator decodes the packet that ends a result set: an EOF
// packet, or under CLIENT_DEPRECATE_EOF an OK packet with header 0xFE.
func (c *Conn) parseTerminator(pkt []byte) (status, warnings uint16, err error) {
	if c.caps&clientDeprecateEOF != 0 {
		ok, err := parseOK(pkt)
		return ok.status, ok.warnings, err
	}
	return parseEOF(pkt)
}

// serverError decodes an ERR packet. A malformed one breaks the
// connection like any other protocol error.
func (c *Conn) serverError(pkt []byte) error {
	e, err := parseErr(pkt)
	if err != nil {
		return c.fail(err)
	}
	return e
}
