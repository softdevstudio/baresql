package pgwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// maxMessage caps one backend message. PostgreSQL's largest field is 1 GB;
// a message claiming more than that is broken or hostile.
const maxMessage = 1<<30 + 1<<20

// readMessage reads one typed backend message: a type byte, an int32 length
// (which counts itself) and the body. Bodies over 1 MB are read as they
// arrive, so a lying length can't make it allocate a gigabyte up front.
func readMessage(r io.Reader, hdr []byte) (typ byte, body []byte, err error) {
	if _, err = io.ReadFull(r, hdr[:5]); err != nil {
		return 0, nil, err
	}
	typ = hdr[0]
	n := int64(binary.BigEndian.Uint32(hdr[1:5]))
	if n < 4 || n > maxMessage {
		return 0, nil, fmt.Errorf("pgwire: bad message length %d for message %q", n, typ)
	}
	n -= 4
	if n <= 1<<20 {
		body = make([]byte, n)
		_, err = io.ReadFull(r, body)
	} else {
		var buf bytes.Buffer
		var got int64
		got, err = io.CopyN(&buf, r, n)
		if err == io.EOF && got < n {
			err = io.ErrUnexpectedEOF
		}
		body = buf.Bytes()
	}
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return typ, body, err
}

// msg builds a frontend message.
type msg struct {
	b     []byte
	typed bool
}

// begin starts a message of the given type (0 for the untyped startup,
// SSL and cancel requests).
func begin(typ byte) *msg {
	m := &msg{b: make([]byte, 0, 64), typed: typ != 0}
	if m.typed {
		m.b = append(m.b, typ)
	}
	m.b = append(m.b, 0, 0, 0, 0)
	return m
}

func (m *msg) int32(v int32) *msg  { m.b = binary.BigEndian.AppendUint32(m.b, uint32(v)); return m }
func (m *msg) int16(v int16) *msg  { m.b = binary.BigEndian.AppendUint16(m.b, uint16(v)); return m }
func (m *msg) str(s string) *msg   { m.b = append(append(m.b, s...), 0); return m }
func (m *msg) bytes(p []byte) *msg { m.b = append(m.b, p...); return m }

// done fills in the length and returns the wire bytes.
func (m *msg) done() []byte {
	start := 0
	if m.typed { // the length counts everything after the type byte
		start = 1
	}
	binary.BigEndian.PutUint32(m.b[start:], uint32(len(m.b)-start))
	return m.b
}

// reader walks a message body. Every read is bounds-checked; the first
// failure sticks, so callers check err once at the end.
type reader struct {
	b   []byte
	err error
}

var errShort = errors.New("pgwire: message too short")

func (r *reader) fail() {
	if r.err == nil {
		r.err = errShort
	}
	r.b = nil
}

func (r *reader) byte() byte {
	if len(r.b) < 1 {
		r.fail()
		return 0
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v
}

func (r *reader) int16() int16 {
	if len(r.b) < 2 {
		r.fail()
		return 0
	}
	v := int16(binary.BigEndian.Uint16(r.b))
	r.b = r.b[2:]
	return v
}

func (r *reader) int32() int32 {
	if len(r.b) < 4 {
		r.fail()
		return 0
	}
	v := int32(binary.BigEndian.Uint32(r.b))
	r.b = r.b[4:]
	return v
}

// cstr reads a NUL-terminated string.
func (r *reader) cstr() string {
	i := bytes.IndexByte(r.b, 0)
	if i < 0 {
		r.fail()
		return ""
	}
	s := string(r.b[:i])
	r.b = r.b[i+1:]
	return s
}

func (r *reader) next(n int) []byte {
	if n < 0 || len(r.b) < n {
		r.fail()
		return nil
	}
	v := r.b[:n:n]
	r.b = r.b[n:]
	return v
}

func (r *reader) rest() []byte {
	v := r.b
	r.b = nil
	return v
}
