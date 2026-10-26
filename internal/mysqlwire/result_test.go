package mysqlwire

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func colDefPacket(c Column) []byte {
	b := appendLenencBytes(nil, []byte("def"))
	for _, s := range []string{c.Schema, c.Table, c.OrgTable, c.Name, c.OrgName} {
		b = appendLenencBytes(b, []byte(s))
	}
	b = append(b, 0x0c)
	b = binary.LittleEndian.AppendUint16(b, c.Charset)
	b = binary.LittleEndian.AppendUint32(b, c.Length)
	b = append(b, c.Type)
	b = binary.LittleEndian.AppendUint16(b, c.Flags)
	return append(b, c.Decimals, 0, 0)
}

// rowPacket encodes a text-protocol row; a nil value is NULL.
func rowPacket(values ...[]byte) []byte {
	b := []byte{}
	for _, v := range values {
		if v == nil {
			b = append(b, 0xFB)
			continue
		}
		b = appendLenencBytes(b, v)
	}
	return b
}

func okPacketBytes(header byte, affected, insertID uint64, status, warnings uint16) []byte {
	b := appendLenencInt([]byte{header}, affected)
	b = appendLenencInt(b, insertID)
	b = binary.LittleEndian.AppendUint16(b, status)
	return binary.LittleEndian.AppendUint16(b, warnings)
}

func eofPacket(warnings, status uint16) []byte {
	b := binary.LittleEndian.AppendUint16([]byte{0xFE}, warnings)
	return binary.LittleEndian.AppendUint16(b, status)
}

func errPacket(code uint16, state, msg string) []byte {
	b := binary.LittleEndian.AppendUint16([]byte{0xFF}, code)
	return append(append(b, '#'), state+msg...)
}

// colID and colName describe an INT and a VARCHAR column as MySQL does.
func colID() Column {
	return Column{Schema: "app", Table: "t", OrgTable: "t", Name: "id", OrgName: "id", Charset: 63, Length: 11, Type: 3, Flags: 0x4203}
}

func colName() Column {
	return Column{Schema: "app", Table: "t", OrgTable: "t", Name: "name", OrgName: "name", Charset: 255, Length: 160, Type: 253}
}

func TestParseErr(t *testing.T) {
	e, err := parseErr(errPacket(1064, "42000", "You have an error in your SQL syntax"))
	if err != nil || *e != (Error{1064, "42000", "You have an error in your SQL syntax"}) {
		t.Fatalf("got %+v, %v", e, err)
	}
	if got := e.Error(); got != "ERROR 1064 (42000): You have an error in your SQL syntax" {
		t.Errorf("Error() = %q", got)
	}
	// Without the '#' marker the state defaults to HY000 and the rest is the message.
	e, err = parseErr(append([]byte{0xFF, 0x15, 0x04}, "Access denied"...))
	if err != nil || *e != (Error{1045, "HY000", "Access denied"}) {
		t.Errorf("no SQL state: %+v, %v", e, err)
	}
	// A '#' with fewer than five state bytes after it is message text.
	e, err = parseErr(append([]byte{0xFF, 0x01, 0x00}, "#42"...))
	if err != nil || e.SQLState != "HY000" || e.Message != "#42" {
		t.Errorf("short state: %+v, %v", e, err)
	}
	for _, b := range [][]byte{nil, {0xFF}, {0xFF, 0x01}, {0x00, 0x01, 0x00}} {
		if _, err := parseErr(b); err == nil {
			t.Errorf("parseErr(% x) accepted", b)
		}
	}
}

func TestParseOKAndEOF(t *testing.T) {
	ok, err := parseOK(append(okPacketBytes(0x00, 1<<40, 77, 0x0022, 3), "Rows matched: 2"...))
	if err != nil || ok != (okPacket{1 << 40, 77, 0x0022, 3}) {
		t.Errorf("parseOK = %+v, %v", ok, err)
	}
	ok, err = parseOK(okPacketBytes(0xFE, 0, 0, serverMoreResultsExists, 1))
	if err != nil || ok.status != serverMoreResultsExists || ok.warnings != 1 {
		t.Errorf("parseOK(0xFE) = %+v, %v", ok, err)
	}
	full := okPacketBytes(0x00, 5, 6, 2, 1)
	for n := range len(full) {
		if _, err := parseOK(full[:n]); err == nil {
			t.Errorf("OK cut to %d bytes accepted", n)
		}
	}
	status, warnings, err := parseEOF(eofPacket(4, 0x000a))
	if err != nil || status != 0x000a || warnings != 4 {
		t.Errorf("parseEOF = %#x, %d, %v", status, warnings, err)
	}
	if _, _, err := parseEOF([]byte{0xFE, 1, 0}); err == nil {
		t.Error("short EOF accepted")
	}
}

func TestParseColumnDef(t *testing.T) {
	for _, want := range []Column{colID(), colName(), {Name: "x", Type: 255, Charset: 63, Length: 1<<32 - 1, Decimals: 31}} {
		got, err := parseColumnDef(colDefPacket(want))
		if err != nil || got != want {
			t.Errorf("parseColumnDef: %+v, %v; want %+v", got, err, want)
		}
	}
	full := colDefPacket(colName())
	for n := range len(full) {
		if _, err := parseColumnDef(full[:n]); err == nil {
			t.Errorf("column definition cut to %d bytes accepted", n)
		}
	}
	// A fixed-fields length under 10 cannot hold them.
	short := bytes.Replace(full, []byte{0x0c}, []byte{0x09}, 1)
	if _, err := parseColumnDef(short); err == nil {
		t.Error("fixed-fields length 9 accepted")
	}
}

func TestParseRow(t *testing.T) {
	row, err := parseRow(rowPacket([]byte("42"), nil, []byte{}, []byte{0x00, 0xFB, 0xFF}), 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []Value{{Raw: []byte("42")}, {Null: true}, {Raw: []byte{}}, {Raw: []byte{0x00, 0xFB, 0xFF}}}
	for i := range want {
		if row[i].Null != want[i].Null || !bytes.Equal(row[i].Raw, want[i].Raw) {
			t.Errorf("value %d = %+v, want %+v", i, row[i], want[i])
		}
	}
	if row[1].String() != "NULL" || row[0].String() != "42" || row[2].String() != "" {
		t.Errorf("String() = %q %q %q", row[0], row[1], row[2])
	}
	bad := []struct {
		name string
		b    []byte
		n    int
	}{
		{"too few values", rowPacket([]byte("a")), 2},
		{"too many values", rowPacket([]byte("a"), []byte("b")), 1},
		{"value longer than the packet", []byte{0x05, 'a'}, 1},
		{"0xFF prefix", []byte{0xFF}, 1},
	}
	for _, tc := range bad {
		if _, err := parseRow(tc.b, tc.n); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

func TestColumnTypes(t *testing.T) {
	names := map[byte]string{0: "DECIMAL", 3: "LONG", 8: "LONGLONG", 15: "VARCHAR", 16: "BIT", 245: "JSON",
		246: "NEWDECIMAL", 252: "BLOB", 253: "VAR_STRING", 254: "STRING", 255: "GEOMETRY", 14: "TYPE_14", 242: "TYPE_242"}
	for code, want := range names {
		if got := (Column{Type: code}).TypeName(); got != want {
			t.Errorf("TypeName(%d) = %q, want %q", code, got, want)
		}
	}
	binary := []struct {
		typ     byte
		charset uint16
		want    bool
	}{
		{252, 63, true},   // BLOB
		{254, 63, true},   // BINARY(n)
		{253, 63, true},   // VARBINARY(n)
		{255, 63, true},   // GEOMETRY
		{252, 45, false},  // TEXT
		{253, 255, false}, // VARCHAR
		{3, 63, false},    // INT: numbers report the binary charset too
		{245, 63, false},  // JSON, which MySQL marks binary but sends as text
		{16, 63, false},   // BIT
	}
	for _, tc := range binary {
		if got := (Column{Type: tc.typ, Charset: tc.charset}).IsBinary(); got != tc.want {
			t.Errorf("IsBinary(type %d, charset %d) = %v", tc.typ, tc.charset, got)
		}
	}
}

// pipeConn returns a Conn that talks through net.Pipe to serve, which
// plays the server: it reads the client's packets and writes replies.
// caps are the capabilities in effect for the session.
func pipeConn(t *testing.T, caps uint32, serve ...func(s *packetIO) error) *Conn {
	t.Helper()
	cli, srv := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer srv.Close()
		s := newPacketIO(srv, srv)
		for _, f := range serve {
			if f(s) != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cli.Close()
		<-done
	})
	return &Conn{raw: cli, pio: newPacketIO(cli, cli), caps: caps}
}

// answer serves one command: it reads COM_QUERY, checks its text if want
// is set, and writes replies.
func answer(t *testing.T, want string, replies ...[]byte) func(s *packetIO) error {
	return func(s *packetIO) error {
		s.seq = 0
		cmd, err := s.readPacket()
		if err != nil {
			return err
		}
		if want != "" && string(cmd) != "\x03"+want {
			t.Errorf("server got command %q, want COM_QUERY %q", cmd, want)
		}
		for _, r := range replies {
			if err := s.writePacket(r); err != nil {
				return err
			}
		}
		return nil
	}
}

// resultSet encodes a result set the way a server with CLIENT_DEPRECATE_EOF
// (deprecateEOF) or without it sends one.
func resultSet(deprecateEOF bool, cols []Column, rows [][]byte, warnings, status uint16) [][]byte {
	out := [][]byte{appendLenencInt(nil, uint64(len(cols)))}
	for _, c := range cols {
		out = append(out, colDefPacket(c))
	}
	if !deprecateEOF {
		out = append(out, eofPacket(0, status))
	}
	out = append(out, rows...)
	if deprecateEOF {
		return append(out, okPacketBytes(0xFE, 0, 0, status, warnings))
	}
	return append(out, eofPacket(warnings, status))
}

func TestQueryResultSet(t *testing.T) {
	for _, deprecateEOF := range []bool{true, false} {
		caps := uint32(clientCaps)
		if !deprecateEOF {
			caps &^= clientDeprecateEOF
		}
		rows := [][]byte{rowPacket([]byte("1"), []byte("Grüße")), rowPacket(nil, []byte{})}
		c := pipeConn(t, caps,
			answer(t, "SELECT id, name FROM t", resultSet(deprecateEOF, []Column{colID(), colName()}, rows, 1, 0x0002)...),
			answer(t, "DELETE FROM t", okPacketBytes(0x00, 2, 0, 0x0002, 0)),
		)
		res, err := c.Query(context.Background(), "SELECT id, name FROM t")
		if err != nil {
			t.Fatalf("deprecateEOF=%v: %v", deprecateEOF, err)
		}
		if len(res) != 1 {
			t.Fatalf("%d results", len(res))
		}
		r := res[0]
		if len(r.Columns) != 2 || r.Columns[0] != colID() || r.Columns[1] != colName() {
			t.Errorf("columns %+v", r.Columns)
		}
		if len(r.Rows) != 2 || r.Rows[0][0].String() != "1" || r.Rows[0][1].String() != "Grüße" ||
			!r.Rows[1][0].Null || r.Rows[1][1].Null || len(r.Rows[1][1].Raw) != 0 {
			t.Errorf("rows %+v", r.Rows)
		}
		if r.Warnings != 1 || r.Status != 0x0002 {
			t.Errorf("warnings %d, status %#x", r.Warnings, r.Status)
		}
		res, err = c.Query(context.Background(), "DELETE FROM t")
		if err != nil || len(res) != 1 || res[0].AffectedRows != 2 || res[0].Columns != nil {
			t.Errorf("deprecateEOF=%v: second statement: %+v, %v", deprecateEOF, res, err)
		}
	}
}

func TestQueryMultipleResults(t *testing.T) {
	more := uint16(0x0002 | serverMoreResultsExists)
	var replies [][]byte
	replies = append(replies, resultSet(true, []Column{colID()}, [][]byte{rowPacket([]byte("1"))}, 0, more)...)
	replies = append(replies, resultSet(true, []Column{colName()}, [][]byte{rowPacket([]byte("x")), rowPacket([]byte("y"))}, 0, more)...)
	replies = append(replies, okPacketBytes(0x00, 0, 0, 0x0002, 0))
	c := pipeConn(t, clientCaps, answer(t, "CALL p()", replies...))
	res, err := c.Query(context.Background(), "CALL p()")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || len(res[0].Rows) != 1 || len(res[1].Rows) != 2 || res[2].Columns != nil {
		t.Fatalf("results %+v", res)
	}
}

// TestQueryRowStartingWithFE sends a row whose first value is longer than
// 16 MiB: its length prefix is 0xFE, like the packet that ends a result
// set, and only the packet length tells them apart.
func TestQueryRowStartingWithFE(t *testing.T) {
	big := pattern(1<<24 + 100)
	for _, deprecateEOF := range []bool{true, false} {
		caps := uint32(clientCaps)
		if !deprecateEOF {
			caps &^= clientDeprecateEOF
		}
		row := rowPacket(big, []byte("tail"))
		if row[0] != 0xFE {
			t.Fatalf("row starts with %#x", row[0])
		}
		rows := [][]byte{row, rowPacket([]byte("small"), nil)}
		c := pipeConn(t, caps, answer(t, "", resultSet(deprecateEOF, []Column{colName(), colName()}, rows, 0, 0x0002)...))
		res, err := c.Query(context.Background(), "SELECT")
		if err != nil {
			t.Fatalf("deprecateEOF=%v: %v", deprecateEOF, err)
		}
		r := res[0]
		if len(r.Rows) != 2 || !bytes.Equal(r.Rows[0][0].Raw, big) || r.Rows[0][1].String() != "tail" || r.Rows[1][0].String() != "small" {
			t.Errorf("deprecateEOF=%v: %d rows, first value intact: %v", deprecateEOF, len(r.Rows), len(r.Rows) > 0 && bytes.Equal(r.Rows[0][0].Raw, big))
		}
	}
}

func TestQueryServerErrors(t *testing.T) {
	c := pipeConn(t, clientCaps,
		answer(t, "SELEC 1", errPacket(1064, "42000", "syntax")),
		// The statement fails after the columns and a row went out.
		answer(t, "SELECT slow()", append(resultSet(true, []Column{colID()}, [][]byte{rowPacket([]byte("1"))}, 0, 2)[:3],
			errPacket(3024, "HY000", "Query execution was interrupted"))...),
		answer(t, "SELECT 1", resultSet(true, []Column{colID()}, [][]byte{rowPacket([]byte("1"))}, 0, 2)...),
	)
	for _, tc := range []struct {
		sql  string
		code uint16
	}{{"SELEC 1", 1064}, {"SELECT slow()", 3024}} {
		res, err := c.Query(context.Background(), tc.sql)
		var e *Error
		if !errors.As(err, &e) || e.Code != tc.code || res != nil {
			t.Fatalf("%s: %v, %v", tc.sql, res, err)
		}
	}
	// ERR packets leave the session in step.
	if res, err := c.Query(context.Background(), "SELECT 1"); err != nil || res[0].Rows[0][0].String() != "1" {
		t.Fatalf("after errors: %v, %v", res, err)
	}
}

func TestQueryLocalInfileRefused(t *testing.T) {
	infile := func(reply []byte) func(s *packetIO) error {
		return func(s *packetIO) error {
			s.seq = 0
			if _, err := s.readPacket(); err != nil {
				return err
			}
			if err := s.writePacket(append([]byte{0xFB}, "/etc/passwd"...)); err != nil {
				return err
			}
			// The client must answer at once with an empty file: one empty
			// packet, next in sequence.
			seq := s.seq
			data, err := s.readPacket()
			if err != nil {
				return err
			}
			if len(data) != 0 || seq != 2 {
				t.Errorf("client answered with %d bytes at sequence %d, want an empty packet at 2", len(data), seq)
			}
			return s.writePacket(reply)
		}
	}
	c := pipeConn(t, clientCaps,
		infile(okPacketBytes(0x00, 0, 0, 2, 0)),
		infile(errPacket(1148, "42000", "The used command is not allowed")),
		answer(t, "SELECT 1", resultSet(true, []Column{colID()}, [][]byte{rowPacket([]byte("1"))}, 0, 2)...),
	)
	_, err := c.Query(context.Background(), "LOAD DATA LOCAL INFILE '/etc/passwd' INTO TABLE t")
	if err == nil || !strings.Contains(err.Error(), `refused the server's request to read local file "/etc/passwd"`) {
		t.Errorf("refusal: %v", err)
	}
	_, err = c.Query(context.Background(), "LOAD DATA LOCAL INFILE '/etc/passwd' INTO TABLE t")
	var e *Error
	if err == nil || !strings.Contains(err.Error(), "refused") || !errors.As(err, &e) || e.Code != 1148 {
		t.Errorf("refusal answered by ERR: %v", err)
	}
	if res, err := c.Query(context.Background(), "SELECT 1"); err != nil || res[0].Rows[0][0].String() != "1" {
		t.Errorf("after refusals: %v, %v", res, err)
	}
}

func TestQueryProtocolErrorBreaksConn(t *testing.T) {
	c := pipeConn(t, clientCaps, answer(t, "", appendLenencInt(nil, 1), []byte{0x03, 'd'}))
	if _, err := c.Query(context.Background(), "SELECT"); err == nil {
		t.Fatal("malformed column definition accepted")
	}
	if _, err := c.Query(context.Background(), "SELECT 1"); err == nil || !strings.Contains(err.Error(), "unusable") {
		t.Errorf("query on a broken Conn: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close on a broken Conn: %v", err)
	}
}

// stall reads one command and never answers.
func stall(s *packetIO) error {
	s.seq = 0
	if _, err := s.readPacket(); err != nil {
		return err
	}
	_, err := s.readPacket()
	return err
}

func TestQueryContext(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		c := pipeConn(t, clientCaps, stall)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := c.Query(ctx, "SELECT SLEEP(5)")
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
			t.Fatalf("after %v: %v", time.Since(start), err)
		}
		if _, err := c.Query(context.Background(), "SELECT 1"); err == nil {
			t.Error("Conn usable after an interrupted query")
		}
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		c := pipeConn(t, clientCaps, stall)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		if _, err := c.Query(ctx, "SELECT SLEEP(5)"); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
		if _, err := c.Query(context.Background(), "SELECT 1"); err == nil {
			t.Error("Conn usable after a cancelled query")
		}
	})
	t.Run("done before the call", func(t *testing.T) {
		c := pipeConn(t, clientCaps, answer(t, "SELECT 1", resultSet(true, []Column{colID()}, [][]byte{rowPacket([]byte("1"))}, 0, 2)...))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := c.Query(ctx, "SELECT 1"); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
		// Nothing was sent, so the Conn still works.
		if res, err := c.Query(context.Background(), "SELECT 1"); err != nil || len(res) != 1 {
			t.Errorf("after an early cancel: %v, %v", res, err)
		}
	})
}

func TestQueryAfterClose(t *testing.T) {
	quit := make(chan []byte, 1)
	c := pipeConn(t, clientCaps, func(s *packetIO) error {
		s.seq = 0
		b, err := s.readPacket()
		quit <- b
		return err
	})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if b := <-quit; !bytes.Equal(b, []byte{comQuit}) {
		t.Errorf("server got % x, want COM_QUIT", b)
	}
	if _, err := c.Query(context.Background(), "SELECT 1"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Query after Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
