package mysqlwire

import (
	"bytes"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
)

// pattern returns n bytes that differ between neighbouring packets, so a
// payload put back together in the wrong order or at the wrong offset
// does not compare equal.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i) ^ byte(i>>8) ^ byte(i>>16)
	}
	return b
}

func TestLenencInt(t *testing.T) {
	cases := []struct {
		v    uint64
		wire []byte
	}{
		{0, []byte{0x00}},
		{250, []byte{0xFA}},
		{251, []byte{0xFC, 0xFB, 0x00}},
		{0xFFFF, []byte{0xFC, 0xFF, 0xFF}},
		{0x10000, []byte{0xFD, 0x00, 0x00, 0x01}},
		{0xFFFFFF, []byte{0xFD, 0xFF, 0xFF, 0xFF}},
		{0x1000000, []byte{0xFE, 0, 0, 0, 1, 0, 0, 0, 0}},
		{1<<64 - 1, []byte{0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}},
	}
	for _, tc := range cases {
		if got := appendLenencInt(nil, tc.v); !bytes.Equal(got, tc.wire) {
			t.Errorf("appendLenencInt(%d) = % x, want % x", tc.v, got, tc.wire)
		}
		c := cursor{b: tc.wire}
		if got := c.lenencInt(); got != tc.v || c.bad || len(c.b) != 0 {
			t.Errorf("lenencInt(% x) = %d (bad %v, %d left), want %d", tc.wire, got, c.bad, len(c.b), tc.v)
		}
	}
	// 0xFB is NULL and 0xFF an error marker, not integers; the others are
	// cut short.
	for _, wire := range [][]byte{{}, {0xFB}, {0xFF}, {0xFC, 1}, {0xFD, 1, 2}, {0xFE, 1, 2, 3, 4, 5, 6, 7}} {
		c := cursor{b: wire}
		if c.lenencInt(); !c.bad {
			t.Errorf("lenencInt(% x) accepted", wire)
		}
	}
}

func TestLenencBytes(t *testing.T) {
	for _, n := range []int{0, 1, 250, 251, 0xFFFF, 0x10000} {
		s := pattern(n)
		c := cursor{b: append(appendLenencBytes(nil, s), 0x99)}
		got := c.lenencBytes()
		if c.bad || !bytes.Equal(got, s) || !bytes.Equal(c.b, []byte{0x99}) {
			t.Fatalf("length %d: got %d bytes (bad %v), left % x", n, len(got), c.bad, c.b)
		}
		if cap(got) != len(got) {
			t.Errorf("length %d: capacity %d reaches past the string", n, cap(got))
		}
	}
	for _, wire := range [][]byte{
		{0x05, 'a', 'b'}, // longer than the data
		{0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // 2^64-1 bytes
	} {
		c := cursor{b: wire}
		if got := c.lenencBytes(); got != nil || !c.bad {
			t.Errorf("lenencBytes(% x) = %q, bad %v", wire, got, c.bad)
		}
	}
}

func TestCursorStaysBad(t *testing.T) {
	c := cursor{b: []byte{1, 2, 3}}
	if c.u32(); !c.bad {
		t.Fatal("u32 of 3 bytes accepted")
	}
	// Once bad, every read yields zero values, even ones that would fit.
	if v := c.u8(); v != 0 || !c.bad {
		t.Errorf("u8 after failure = %d", v)
	}
	c = cursor{b: []byte("abc")}
	if c.nulBytes(); !c.bad {
		t.Error("nulBytes without NUL accepted")
	}
	c = cursor{b: []byte("ab\x00cd")}
	if got := c.nulBytes(); string(got) != "ab" || string(c.rest()) != "cd" || c.bad {
		t.Errorf("nulBytes = %q, bad %v", got, c.bad)
	}
}

// walkPackets splits a byte stream into packets by reading the headers
// directly, independent of readPacket.
func walkPackets(t *testing.T, wire []byte, firstSeq byte) (sizes []int, payload []byte) {
	t.Helper()
	seq := firstSeq
	for len(wire) > 0 {
		if len(wire) < 4 {
			t.Fatalf("%d stray bytes", len(wire))
		}
		n := int(wire[0]) | int(wire[1])<<8 | int(wire[2])<<16
		if wire[3] != seq {
			t.Fatalf("packet %d has sequence %d, want %d", len(sizes), wire[3], seq)
		}
		seq++
		sizes = append(sizes, n)
		payload = append(payload, wire[4:4+n]...)
		wire = wire[4+n:]
	}
	return sizes, payload
}

func TestWritePacketSplits(t *testing.T) {
	const m = maxPacketPayload
	cases := []struct {
		n     int
		sizes []int
	}{
		{0, []int{0}},
		{1, []int{1}},
		{m - 1, []int{m - 1}},
		{m, []int{m, 0}}, // a full packet needs an empty one after it
		{m + 1, []int{m, 1}},
		{2 * m, []int{m, m, 0}},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		p := newPacketIO(nil, &buf)
		p.seq = 7
		payload := pattern(tc.n)
		if err := p.writePacket(payload); err != nil {
			t.Fatal(err)
		}
		sizes, got := walkPackets(t, buf.Bytes(), 7)
		if !slices.Equal(sizes, tc.sizes) || !bytes.Equal(got, payload) {
			t.Errorf("payload of %d: packets %v, want %v (payload intact: %v)", tc.n, sizes, tc.sizes, bytes.Equal(got, payload))
		}
		if want := byte(7 + len(tc.sizes)); p.seq != want {
			t.Errorf("payload of %d: next sequence %d, want %d", tc.n, p.seq, want)
		}
	}
}

// frame builds packets by hand, one per payload, numbered from seq.
func frame(seq byte, payloads ...[]byte) []byte {
	var b []byte
	for _, p := range payloads {
		n := len(p)
		b = append(b, byte(n), byte(n>>8), byte(n>>16), seq)
		b = append(b, p...)
		seq++
	}
	return b
}

func TestReadPacketJoins(t *testing.T) {
	const m = maxPacketPayload
	big := pattern(2*m + 10)
	cases := []struct {
		name  string
		parts [][]byte
		want  []byte
	}{
		{"empty", [][]byte{{}}, nil},
		{"small", [][]byte{[]byte("hello")}, []byte("hello")},
		{"exactly one full packet", [][]byte{big[:m], {}}, big[:m]},
		{"full packet plus one byte", [][]byte{big[:m], big[m : m+1]}, big[:m+1]},
		{"three packets", [][]byte{big[:m], big[m : 2*m], big[2*m:]}, big},
	}
	for _, tc := range cases {
		// A second payload after the first shows where reading stopped.
		wire := frame(4, append(tc.parts, []byte("next"))...)
		p := newPacketIO(bytes.NewReader(wire), io.Discard)
		p.seq = 4
		got, err := p.readPacket()
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Fatalf("%s: got %d bytes, %v; want %d bytes", tc.name, len(got), err, len(tc.want))
		}
		if next, err := p.readPacket(); err != nil || string(next) != "next" {
			t.Fatalf("%s: following payload %q, %v", tc.name, next, err)
		}
	}
}

// TestPacketsOverPipe echoes payloads around the split size through a
// synchronous pipe: the client writes, the server reads and writes back.
// Both ends keep one sequence counter, as in the protocol.
func TestPacketsOverPipe(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()
	sizes := []int{maxPacketPayload - 1, maxPacketPayload, maxPacketPayload + 1, 0, 3}
	pc, ps := newPacketIO(cli, cli), newPacketIO(srv, srv)
	errc := make(chan error, 1)
	go func() {
		for range sizes {
			p, err := ps.readPacket()
			if err == nil {
				err = ps.writePacket(p)
			}
			if err != nil {
				errc <- err
				return
			}
		}
		errc <- nil
	}()
	for _, n := range sizes {
		want := pattern(n)
		if err := pc.writePacket(want); err != nil {
			t.Fatal(err)
		}
		got, err := pc.readPacket()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("payload of %d: echo of %d bytes, %v", n, len(got), err)
		}
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if pc.seq != ps.seq {
		t.Errorf("sequence ids drifted apart: client %d, server %d", pc.seq, ps.seq)
	}
}

func TestReadPacketErrors(t *testing.T) {
	cases := []struct {
		name string
		wire []byte
		want string
	}{
		{"wrong sequence", frame(5, []byte("x")), "sequence 5, want 0"},
		{"no data", nil, io.ErrUnexpectedEOF.Error()},
		{"short header", []byte{1, 0}, io.ErrUnexpectedEOF.Error()},
		{"short payload", []byte{5, 0, 0, 0, 'a', 'b'}, io.ErrUnexpectedEOF.Error()},
		{"continuation missing", frame(0, pattern(maxPacketPayload)), io.ErrUnexpectedEOF.Error()},
		{"huge length, little data", []byte{0xFF, 0xFF, 0xFF, 0, 1, 2, 3}, io.ErrUnexpectedEOF.Error()},
	}
	for _, tc := range cases {
		p := newPacketIO(bytes.NewReader(tc.wire), io.Discard)
		if _, err := p.readPacket(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}
