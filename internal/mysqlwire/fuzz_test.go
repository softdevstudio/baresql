package mysqlwire

import (
	"bytes"
	"io"
	"net"
	"slices"
	"testing"
	"time"
)

// The fuzz targets feed arbitrary server bytes to the parsers. They check
// that nothing panics and that what parses stays within its input.

func FuzzReadPacket(f *testing.F) {
	f.Add(frame(0, []byte("hello"), []byte{}))
	f.Add(frame(0, []byte{0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}))
	f.Add(frame(2, []byte("out of sequence")))
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0x00, 1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		p := newPacketIO(bytes.NewReader(data), io.Discard)
		total := 0
		for range 64 {
			pkt, err := p.readPacket()
			if err != nil {
				return
			}
			if total += len(pkt) + 4; total > len(data) {
				t.Fatalf("read %d bytes of payload and headers from %d bytes", total, len(data))
			}
		}
	})
}

func FuzzParseHandshake(f *testing.F) {
	f.Add(mysqlHandshake().build())
	f.Add(mariadbHandshake().build())
	spec := mysqlHandshake()
	spec.noNUL = true
	f.Add(spec.build())
	f.Add(append([]byte{0xFF, 0x10, 0x04}, "Too many connections"...))
	f.Fuzz(func(t *testing.T, data []byte) {
		hs, err := parseHandshake(data)
		if err != nil {
			return
		}
		if len(hs.version)+len(hs.scramble)+len(hs.plugin) > len(data) {
			t.Fatalf("fields longer than the %d-byte input: %+v", len(data), hs)
		}
		serverVersion(hs.version)
	})
}

func FuzzParseColumnDef(f *testing.F) {
	f.Add(colDefPacket(colID()))
	f.Add(colDefPacket(colName()))
	f.Add(colDefPacket(Column{}))
	f.Fuzz(func(t *testing.T, data []byte) {
		col, err := parseColumnDef(data)
		if err != nil {
			return
		}
		if n := len(col.Schema) + len(col.Table) + len(col.OrgTable) + len(col.Name) + len(col.OrgName); n > len(data) {
			t.Fatalf("names of %d bytes from %d bytes", n, len(data))
		}
		col.TypeName()
		col.IsBinary()
	})
}

func FuzzParseRow(f *testing.F) {
	f.Add(uint8(2), rowPacket([]byte("1"), []byte("abc")))
	f.Add(uint8(3), rowPacket(nil, []byte{}, nil))
	f.Add(uint8(1), []byte{0xFC, 0x02, 0x00, 'h', 'i'})
	f.Add(uint8(1), []byte{0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, n uint8, data []byte) {
		row, err := parseRow(data, int(n))
		if err != nil {
			return
		}
		size := 0
		for _, v := range row {
			size += len(v.Raw)
			if v.Null != (v.String() == "NULL" && v.Raw == nil) {
				t.Fatalf("value %+v", v)
			}
		}
		if len(row) != int(n) || size > len(data) {
			t.Fatalf("%d values of %d bytes from %d bytes", len(row), size, len(data))
		}
	})
}

func FuzzParseOKErr(f *testing.F) {
	f.Add(okPacketBytes(0x00, 3, 7, 0x0002, 1))
	f.Add(okPacketBytes(0xFE, 0, 0, 0x000a, 0))
	f.Add(eofPacket(1, 0x0002))
	f.Add(errPacket(1064, "42000", "syntax"))
	f.Add(append([]byte{0xFF, 0x10, 0x04}, "Too many connections"...))
	f.Add(authSwitch(pluginNative, []byte("12345678901234567890")))
	f.Fuzz(func(t *testing.T, data []byte) {
		parseOK(data)
		parseEOF(data)
		if e, err := parseErr(data); err == nil {
			if len(e.SQLState) != 5 || len(e.Message) > len(data) {
				t.Fatalf("parseErr = %+v", e)
			}
			_ = e.Error()
		}
		if len(data) > 0 && data[0] == 0xFE {
			parseAuthSwitch(data)
		}
	})
}

// streamConn is a net.Conn that reads a fixed byte stream and swallows
// writes, so whole exchanges can be fuzzed without a peer.
type streamConn struct{ io.Reader }

func (streamConn) Write(p []byte) (int, error)      { return len(p), nil }
func (streamConn) Close() error                     { return nil }
func (streamConn) LocalAddr() net.Addr              { return nil }
func (streamConn) RemoteAddr() net.Addr             { return nil }
func (streamConn) SetDeadline(time.Time) error      { return nil }
func (streamConn) SetReadDeadline(time.Time) error  { return nil }
func (streamConn) SetWriteDeadline(time.Time) error { return nil }

func fuzzConn(data []byte, caps uint32) *Conn {
	sc := streamConn{bytes.NewReader(data)}
	return &Conn{cfg: Config{User: "u", Password: "pw", TLS: "disable"}, raw: sc, pio: newPacketIO(sc, sc), caps: caps}
}

// FuzzQueryResponse feeds the server's side of a COM_QUERY exchange, which
// starts at sequence 1.
func FuzzQueryResponse(f *testing.F) {
	more := uint16(2 | serverMoreResultsExists)
	f.Add(true, frame(1, resultSet(true, []Column{colID(), colName()}, [][]byte{rowPacket([]byte("1"), nil)}, 1, 2)...))
	f.Add(false, frame(1, resultSet(false, []Column{colID()}, [][]byte{rowPacket([]byte("7"))}, 0, 2)...))
	f.Add(true, frame(1, append(resultSet(true, []Column{colID()}, nil, 0, more), okPacketBytes(0, 1, 2, 2, 0))...))
	f.Add(true, slices.Concat(frame(1, append([]byte{0xFB}, "/etc/passwd"...)), frame(3, okPacketBytes(0, 0, 0, 2, 0))))
	f.Add(true, frame(1, errPacket(1064, "42000", "syntax")))
	f.Fuzz(func(t *testing.T, deprecateEOF bool, data []byte) {
		caps := uint32(clientCaps)
		if !deprecateEOF {
			caps &^= clientDeprecateEOF
		}
		c := fuzzConn(data, caps)
		res, err := c.query("SELECT 1")
		if err == nil && len(res) == 0 {
			t.Fatal("no results and no error")
		}
	})
}

// FuzzLogin feeds the server's side of a whole login: handshake, auth
// exchange and the replies to the two session statements. The seeds leave
// gaps in the sequence ids where the client's packets go.
func FuzzLogin(f *testing.F) {
	ok := okPacketBytes(0x00, 0, 0, 2, 0)
	setup := append(frame(1, ok), frame(1, ok)...)
	_, pemKey := rsaPEM(f)
	seeds := [][]byte{
		slices.Concat(frame(0, mariadbHandshake().build()), frame(2, ok), setup),
		slices.Concat(frame(0, mysqlHandshake().build()), frame(2, []byte{0x01, cachingFastAuthOK}, ok), setup),
		slices.Concat(frame(0, mysqlHandshake().build()), frame(2, []byte{0x01, cachingFullAuth}),
			frame(4, append([]byte{0x01}, pemKey...)), frame(6, ok), setup),
		slices.Concat(frame(0, mysqlHandshake().build()), frame(2, authSwitch(pluginNative, []byte("abcdefghij0123456789"))), frame(4, ok), setup),
		slices.Concat(frame(0, mysqlHandshake().build()), frame(2, errPacket(1045, "28000", "Access denied"))),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzConn(data, 0).login(nil)
	})
}
