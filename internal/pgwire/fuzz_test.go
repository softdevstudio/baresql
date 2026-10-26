package pgwire

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// None of these may panic or hang, whatever the bytes.

func FuzzParseDSN(f *testing.F) {
	for _, s := range []string{
		"postgres://u:p@h:5432/d?sslmode=require",
		"host=h port=1 user='a b' password=x\\ y",
		"postgresql://[::1]/d?host=/tmp",
		"host='unterminated",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		t.Setenv("PGPASSFILE", "/nonexistent")
		ParseDSN(s)
	})
}

func FuzzReadMessage(f *testing.F) {
	f.Add([]byte{'Z', 0, 0, 0, 5, 'I'})
	f.Add([]byte{'D', 0, 0, 0, 4})
	f.Add([]byte{'D', 0x7f, 0xff, 0xff, 0xff, 1, 2})
	f.Fuzz(func(t *testing.T, b []byte) {
		readMessage(bytes.NewReader(b), make([]byte, 5))
	})
}

func FuzzParseError(f *testing.F) {
	f.Add([]byte("SERROR\x00C42601\x00Msyntax\x00P12\x00\x00"))
	f.Add([]byte("S"))
	f.Fuzz(func(t *testing.T, b []byte) {
		e := parseError(b)
		_ = e.Error()
	})
}

func FuzzParseRowDescription(f *testing.F) {
	f.Add([]byte{0, 1, 'a', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 25, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0})
	f.Add([]byte{0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		parseRowDescription(b)
	})
}

func FuzzParseDataRow(f *testing.F) {
	f.Add([]byte{0, 2, 0, 0, 0, 1, 'x', 0xff, 0xff, 0xff, 0xff}, uint8(2))
	f.Add([]byte{0, 1, 0xff, 0xff, 0xff, 0xfb}, uint8(1))
	f.Fuzz(func(t *testing.T, b []byte, n uint8) {
		parseDataRow(b, int(n))
	})
}

func FuzzSCRAMServerFirst(f *testing.F) {
	f.Add("r=abcdef,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096")
	f.Add("r=abc")
	f.Fuzz(func(t *testing.T, msg string) {
		s := &scram{password: "pw", nonce: "abc", gs2: "n,,", maxIter: 64}
		s.clientFirst()
		if _, err := s.clientFinal([]byte(msg)); err == nil {
			s.verifyServerFinal([]byte(msg))
		}
	})
}

// scriptConn is a net.Conn that reads from fixed bytes and swallows writes.
type scriptConn struct{ r io.Reader }

func (c *scriptConn) Read(p []byte) (int, error)       { return c.r.Read(p) }
func (c *scriptConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *scriptConn) Close() error                     { return nil }
func (c *scriptConn) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *scriptConn) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *scriptConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptConn) SetWriteDeadline(time.Time) error { return nil }

// FuzzServerBytes feeds arbitrary server output to a whole login and query.
func FuzzServerBytes(f *testing.F) {
	login := []byte{'R', 0, 0, 0, 8, 0, 0, 0, 0, 'Z', 0, 0, 0, 5, 'I'}
	f.Add(login)
	f.Add(append(append([]byte{}, login...), 'T', 0, 0, 0, 6, 0, 0, 'C', 0, 0, 0, 13, 'S', 'E', 'L', 'E', 'C', 'T', ' ', '0', 0, 'Z', 0, 0, 0, 5, 'I'))
	f.Add([]byte{'R', 0, 0, 0, 12, 0, 0, 0, 5, 1, 2, 3, 4})
	f.Add([]byte("R\x00\x00\x00\x17\x00\x00\x00\x0aSCRAM-SHA-256\x00\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		nc := &scriptConn{r: bytes.NewReader(b)}
		c := &Conn{
			cfg:          Config{User: "u", Password: "p", Database: "d", SSLMode: "disable", ChannelBinding: "prefer"},
			nc:           nc,
			br:           bufio.NewReader(nc),
			params:       map[string]string{},
			scramMaxIter: 64,
		}
		c.br = bufio.NewReader(nc)
		if err := c.startup(); err != nil {
			return
		}
		c.Query(context.Background(), "SELECT 1")
		c.QueryArgs(context.Background(), "SELECT $1", "x")
	})
}
