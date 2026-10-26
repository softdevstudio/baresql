// Package mysqlwire is a small MySQL and MariaDB client for the text
// protocol: it logs in, runs statements with COM_QUERY and returns every
// value as the text the server sent, with the column metadata.
package mysqlwire

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
)

const (
	// maxPacketPayload is the most a single packet carries. A packet of
	// exactly this size means the payload continues in the next one.
	maxPacketPayload = 1<<24 - 1

	// maxPayload caps a reassembled payload: max_allowed_packet tops out
	// at 1 GiB, so anything longer is a corrupt or hostile stream.
	maxPayload = 1 << 30

	// readStep is how far readN allocates ahead of the bytes that have
	// actually arrived, so a lying length header costs little memory.
	readStep = 1 << 20
)

// packetIO frames payloads as MySQL packets: a 3-byte little-endian
// length, a sequence id, then the payload. The sequence id restarts at 0
// with every command and counts the packets in both directions; during
// login it carries on across the TLS upgrade.
type packetIO struct {
	r   *bufio.Reader
	w   *bufio.Writer
	seq uint8
}

func newPacketIO(r io.Reader, w io.Writer) *packetIO {
	return &packetIO{r: bufio.NewReader(r), w: bufio.NewWriter(w)}
}

// readPacket returns the next payload, joined from as many packets as it
// spans.
func (p *packetIO) readPacket() ([]byte, error) {
	var payload []byte
	for {
		var hdr [4]byte
		if err := readFull(p.r, hdr[:]); err != nil {
			return nil, fmt.Errorf("mysqlwire: read: %w", err)
		}
		n := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16
		if hdr[3] != p.seq {
			return nil, fmt.Errorf("mysqlwire: packet sequence %d, want %d", hdr[3], p.seq)
		}
		p.seq++
		if len(payload)+n > maxPayload {
			return nil, errors.New("mysqlwire: payload larger than 1 GiB")
		}
		var err error
		if payload, err = readN(p.r, payload, n); err != nil {
			return nil, fmt.Errorf("mysqlwire: read: %w", err)
		}
		if n < maxPacketPayload {
			return payload, nil
		}
	}
}

// writePacket sends payload in as many packets as it needs and flushes.
// A payload that fills its last packet exactly gets an empty packet after
// it, which is how the reader learns that the payload ended.
func (p *packetIO) writePacket(payload []byte) error {
	for {
		n := min(len(payload), maxPacketPayload)
		hdr := [4]byte{byte(n), byte(n >> 8), byte(n >> 16), p.seq}
		p.seq++
		// bufio.Writer keeps its first error and returns it from Flush,
		// so the check there covers these writes.
		p.w.Write(hdr[:])
		p.w.Write(payload[:n])
		payload = payload[n:]
		if n < maxPacketPayload {
			break
		}
	}
	if err := p.w.Flush(); err != nil {
		return fmt.Errorf("mysqlwire: write: %w", err)
	}
	return nil
}

// readN appends n bytes from r to buf, growing buf readStep at a time.
func readN(r io.Reader, buf []byte, n int) ([]byte, error) {
	for n > 0 {
		step := min(n, readStep)
		start := len(buf)
		buf = slices.Grow(buf, step)[:start+step]
		if err := readFull(r, buf[start:]); err != nil {
			return nil, err
		}
		n -= step
	}
	return buf, nil
}

// readFull is io.ReadFull, except that a stream ending exactly on a
// boundary is unexpected too: a server never just stops mid-exchange.
func readFull(r io.Reader, b []byte) error {
	_, err := io.ReadFull(r, b)
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// errMalformed reports a packet that does not parse as what the protocol
// says comes next.
func errMalformed(what string) error {
	return fmt.Errorf("mysqlwire: malformed %s packet", what)
}

// cursor reads fields off a payload. A read past the end marks the cursor
// bad and yields zero values, so a parser reads all its fields and checks
// bad once at the end.
type cursor struct {
	b   []byte
	bad bool
}

// take returns the next n bytes. The result's capacity ends with it, so
// appending to it cannot overwrite the rest of the payload.
func (c *cursor) take(n int) []byte {
	if c.bad || n < 0 || n > len(c.b) {
		c.bad = true
		return nil
	}
	v := c.b[:n:n]
	c.b = c.b[n:]
	return v
}

func (c *cursor) u8() byte {
	if v := c.take(1); len(v) == 1 {
		return v[0]
	}
	return 0
}

func (c *cursor) u16() uint16 {
	if v := c.take(2); len(v) == 2 {
		return binary.LittleEndian.Uint16(v)
	}
	return 0
}

func (c *cursor) u24() uint32 {
	if v := c.take(3); len(v) == 3 {
		return uint32(v[0]) | uint32(v[1])<<8 | uint32(v[2])<<16
	}
	return 0
}

func (c *cursor) u32() uint32 {
	if v := c.take(4); len(v) == 4 {
		return binary.LittleEndian.Uint32(v)
	}
	return 0
}

func (c *cursor) u64() uint64 {
	if v := c.take(8); len(v) == 8 {
		return binary.LittleEndian.Uint64(v)
	}
	return 0
}

// lenencInt reads a length-encoded integer. 0xFB, which marks NULL in a
// row, and 0xFF do not start one.
func (c *cursor) lenencInt() uint64 {
	switch b := c.u8(); b {
	case 0xFC:
		return uint64(c.u16())
	case 0xFD:
		return uint64(c.u24())
	case 0xFE:
		return c.u64()
	case 0xFB, 0xFF:
		c.bad = true
		return 0
	default:
		return uint64(b)
	}
}

// lenencBytes reads a length-encoded string.
func (c *cursor) lenencBytes() []byte {
	n := c.lenencInt()
	if n > uint64(len(c.b)) {
		c.bad = true
		return nil
	}
	return c.take(int(n))
}

// nulBytes reads a NUL-terminated string.
func (c *cursor) nulBytes() []byte {
	i := bytes.IndexByte(c.b, 0)
	if i < 0 {
		c.bad = true
		return nil
	}
	v := c.take(i)
	c.take(1)
	return v
}

// rest returns whatever is left.
func (c *cursor) rest() []byte {
	return c.take(len(c.b))
}

func appendLenencInt(b []byte, v uint64) []byte {
	switch {
	case v < 0xFB:
		return append(b, byte(v))
	case v <= 0xFFFF:
		return append(b, 0xFC, byte(v), byte(v>>8))
	case v <= 0xFFFFFF:
		return append(b, 0xFD, byte(v), byte(v>>8), byte(v>>16))
	}
	return binary.LittleEndian.AppendUint64(append(b, 0xFE), v)
}

func appendLenencBytes(b, s []byte) []byte {
	return append(appendLenencInt(b, uint64(len(s))), s...)
}
