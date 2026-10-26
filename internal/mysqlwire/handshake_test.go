package mysqlwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// hsSpec describes an initial handshake packet; build lays it out the way
// MySQL and MariaDB do.
type hsSpec struct {
	version  string
	connID   uint32
	caps     uint32
	charset  byte
	scramble []byte // 20 bytes
	plugin   string
	reserved [10]byte
	noNUL    bool // end the plugin name without a NUL (Bug#59453)
}

func (h hsSpec) build() []byte {
	b := append([]byte{10}, h.version...)
	b = append(b, 0)
	b = binary.LittleEndian.AppendUint32(b, h.connID)
	b = append(b, h.scramble[:8]...)
	b = append(b, 0)
	b = binary.LittleEndian.AppendUint16(b, uint16(h.caps))
	b = append(b, h.charset)
	b = binary.LittleEndian.AppendUint16(b, 0x0002) // SERVER_STATUS_AUTOCOMMIT
	b = binary.LittleEndian.AppendUint16(b, uint16(h.caps>>16))
	b = append(b, byte(len(h.scramble)+1))
	b = append(b, h.reserved[:]...)
	b = append(append(b, h.scramble[8:]...), 0)
	b = append(b, h.plugin...)
	if !h.noNUL {
		b = append(b, 0)
	}
	return b
}

// Capabilities as the local servers announce them: MySQL 8.0 with TLS,
// and MariaDB 10.11 without, which also clears bit 0 (CLIENT_MYSQL) to
// say that it has extended capabilities.
const (
	mysqlServerCaps   = 0xdfffffff
	mariadbServerCaps = 0x81fff7fe
)

func mysqlHandshake() hsSpec {
	return hsSpec{
		version:  "8.0.46-0ubuntu0.24.04.4",
		connID:   23,
		caps:     mysqlServerCaps,
		charset:  255,
		scramble: []byte("Kq%7#vB1oP]x&e9?w2Lz"),
		plugin:   pluginCachingSHA2,
	}
}

func mariadbHandshake() hsSpec {
	return hsSpec{
		version:  "5.5.5-10.11.14-MariaDB-0ubuntu0.24.04.1",
		connID:   0x01020304,
		caps:     mariadbServerCaps,
		charset:  8,
		scramble: []byte("'D|v{Xn<6\"G,a]R0kSj^"),
		plugin:   pluginNative,
		reserved: [10]byte{6: 0x1d}, // MariaDB extended capabilities
	}
}

func TestParseHandshake(t *testing.T) {
	cases := []struct {
		name    string
		spec    hsSpec
		version string
		mariaDB bool
	}{
		{"mysql", mysqlHandshake(), "8.0.46-0ubuntu0.24.04.4", false},
		{"mariadb", mariadbHandshake(), "10.11.14-MariaDB-0ubuntu0.24.04.1", true},
	}
	for _, tc := range cases {
		hs, err := parseHandshake(tc.spec.build())
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if hs.version != tc.spec.version || hs.connID != tc.spec.connID || hs.caps != tc.spec.caps ||
			!bytes.Equal(hs.scramble, tc.spec.scramble) || hs.plugin != tc.spec.plugin {
			t.Errorf("%s: got %+v", tc.name, hs)
		}
		if v, m := serverVersion(hs.version); v != tc.version || m != tc.mariaDB {
			t.Errorf("%s: serverVersion = %q, %v", tc.name, v, m)
		}
	}
}

func TestParseHandshakeQuirks(t *testing.T) {
	spec := mysqlHandshake()
	spec.noNUL = true
	hs, err := parseHandshake(spec.build())
	if err != nil || hs.plugin != pluginCachingSHA2 {
		t.Errorf("plugin name without NUL: %+v, %v", hs, err)
	}

	spec = mysqlHandshake()
	spec.caps &^= clientPluginAuth
	if _, err := parseHandshake(spec.build()); err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("server without plugin auth: %v", err)
	}

	b := mysqlHandshake().build()
	b[0] = 9
	if _, err := parseHandshake(b); err == nil || !strings.Contains(err.Error(), "version 9") {
		t.Errorf("protocol 9: %v", err)
	}
}

func TestParseHandshakeTruncated(t *testing.T) {
	full := mariadbHandshake().build()
	// Everything up to the end of the scramble is required; past that the
	// plugin name may lack its NUL, so a cut there still parses.
	required := len(full) - len(pluginNative) - 1
	for n := range len(full) {
		hs, err := parseHandshake(full[:n])
		if n < required && err == nil {
			t.Errorf("handshake cut to %d bytes parsed: %+v", n, hs)
		}
	}
}

func TestParseHandshakeRefusal(t *testing.T) {
	cases := []struct {
		packet []byte
		want   Error
	}{
		// Before the login the server leaves out the SQL state.
		{append([]byte{0xFF, 0x10, 0x04}, "Too many connections"...), Error{1040, "HY000", "Too many connections"}},
		{append([]byte{0xFF, 0x6a, 0x04}, "#HY000Host '10.0.0.9' is not allowed to connect"...), Error{1130, "HY000", "Host '10.0.0.9' is not allowed to connect"}},
	}
	for _, tc := range cases {
		_, err := parseHandshake(tc.packet)
		var e *Error
		if !errors.As(err, &e) || *e != tc.want {
			t.Errorf("refusal % x: %v, want %+v", tc.packet[:3], err, tc.want)
		}
	}
	if _, err := parseHandshake([]byte{0xFF, 0x10}); err == nil {
		t.Error("truncated ERR accepted")
	}
}

func TestServerVersion(t *testing.T) {
	cases := []struct {
		raw, version string
		mariaDB      bool
	}{
		{"5.5.5-10.11.14-MariaDB-0ubuntu0.24.04.1", "10.11.14-MariaDB-0ubuntu0.24.04.1", true},
		{"11.4.5-MariaDB-ubu2404", "11.4.5-MariaDB-ubu2404", true}, // newer releases drop the prefix
		{"12.3.1-MariaDB", "12.3.1-MariaDB", true},
		{"8.0.46-0ubuntu0.24.04.4", "8.0.46-0ubuntu0.24.04.4", false},
		{"9.7.0", "9.7.0", false},
		{"26.7.0", "26.7.0", false}, // calendar versioning after 9.7
		{"5.5.5-log", "5.5.5-log", false},
	}
	for _, tc := range cases {
		if v, m := serverVersion(tc.raw); v != tc.version || m != tc.mariaDB {
			t.Errorf("serverVersion(%q) = %q, %v; want %q, %v", tc.raw, v, m, tc.version, tc.mariaDB)
		}
	}
}

func TestHandshakeResponse(t *testing.T) {
	auth := []byte{1, 2, 3}
	caps := uint32(clientCaps | clientConnectWithDB | clientSSL)
	b, err := handshakeResponse(caps, "root", "app", pluginCachingSHA2, auth)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b[:32], appendClientHeader(nil, caps)) {
		t.Errorf("response and SSLRequest differ in their first 32 bytes")
	}
	c := cursor{b: b}
	if got := c.u32(); got != caps {
		t.Errorf("caps %#x, want %#x", got, caps)
	}
	if got := c.u32(); got != maxClientPacket {
		t.Errorf("max packet %d", got)
	}
	if got := c.u8(); got != charsetUTF8MB4 {
		t.Errorf("charset %d, want 45", got)
	}
	if got := c.take(23); !bytes.Equal(got, make([]byte, 23)) {
		t.Errorf("filler % x", got)
	}
	if got := c.nulBytes(); string(got) != "root" {
		t.Errorf("user %q", got)
	}
	if got := c.lenencBytes(); !bytes.Equal(got, auth) {
		t.Errorf("auth % x", got)
	}
	if got := c.nulBytes(); string(got) != "app" {
		t.Errorf("database %q", got)
	}
	if got := c.nulBytes(); string(got) != pluginCachingSHA2 || len(c.b) != 0 || c.bad {
		t.Errorf("plugin %q, %d bytes left, bad %v", got, len(c.b), c.bad)
	}

	// No database without CLIENT_CONNECT_WITH_DB, and a one-byte auth
	// length without CLIENT_PLUGIN_AUTH_LENENC_CLIENT_DATA.
	old := uint32(clientCaps &^ clientPluginAuthLenenc)
	b, err = handshakeResponse(old, "u", "ignored", pluginNative, auth)
	if err != nil {
		t.Fatal(err)
	}
	want := append(appendClientHeader(nil, old), "u\x00\x03\x01\x02\x03mysql_native_password\x00"...)
	if !bytes.Equal(b, want) {
		t.Errorf("old-style response\n got % x\nwant % x", b, want)
	}
	if _, err := handshakeResponse(old, "u", "", pluginClear, make([]byte, 256)); err == nil {
		t.Error("256-byte auth reply accepted without lenenc support")
	}
}
