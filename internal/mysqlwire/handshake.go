package mysqlwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Capability flags, CLIENT_* in the MySQL source.
const (
	clientLongPassword     = 1 << 0
	clientLongFlag         = 1 << 2
	clientConnectWithDB    = 1 << 3
	clientProtocol41       = 1 << 9
	clientSSL              = 1 << 11
	clientTransactions     = 1 << 13
	clientSecureConnection = 1 << 15
	clientMultiResults     = 1 << 17
	clientPluginAuth       = 1 << 19
	clientPluginAuthLenenc = 1 << 21
	clientDeprecateEOF     = 1 << 24
)

// clientCaps are the capabilities this client asks for, apart from
// CLIENT_CONNECT_WITH_DB and CLIENT_SSL, which depend on the Config.
// CLIENT_MULTI_STATEMENTS and CLIENT_LOCAL_FILES stay off on purpose: one
// statement per COM_QUERY, and the server never gets to read local files.
const clientCaps = clientLongPassword | clientLongFlag | clientProtocol41 |
	clientTransactions | clientSecureConnection | clientMultiResults |
	clientPluginAuth | clientPluginAuthLenenc | clientDeprecateEOF

// serverCapsNeeded rules out servers older than MySQL 5.5.7, which predate
// pluggable authentication.
const serverCapsNeeded = clientProtocol41 | clientSecureConnection | clientPluginAuth

const (
	// charsetUTF8MB4 is utf8mb4_general_ci, which every server version
	// this package supports knows. SET NAMES utf8mb4 after the login then
	// moves the session to the server's default utf8mb4 collation.
	charsetUTF8MB4 = 45

	// maxClientPacket goes in the handshake response. It is the ceiling
	// of max_allowed_packet, so it never limits anything.
	maxClientPacket = 1 << 30
)

// handshake is the server's initial handshake packet, protocol version 10.
type handshake struct {
	version  string
	connID   uint32
	caps     uint32
	scramble []byte
	plugin   string
}

// parseHandshake decodes the initial handshake. A server that refuses the
// connection outright, with too many connections say, sends an ERR packet
// instead, returned as *Error.
func parseHandshake(b []byte) (*handshake, error) {
	if len(b) > 0 && b[0] == 0xFF {
		e, err := parseErr(b)
		if err != nil {
			return nil, err
		}
		return nil, e
	}
	c := cursor{b: b}
	proto := c.u8()
	if c.bad {
		return nil, errMalformed("handshake")
	}
	if proto != 10 {
		return nil, fmt.Errorf("mysqlwire: unsupported handshake protocol version %d", proto)
	}
	hs := &handshake{version: string(c.nulBytes()), connID: c.u32()}
	scramble1 := c.take(8)
	c.take(1) // filler
	hs.caps = uint32(c.u16())
	c.take(1) // the server's default collation
	c.take(2) // status flags
	hs.caps |= uint32(c.u16()) << 16
	authLen := int(c.u8())
	c.take(10) // reserved; MariaDB keeps extended capabilities in the last 4 bytes
	if c.bad {
		return nil, errMalformed("handshake")
	}
	if hs.caps&serverCapsNeeded != serverCapsNeeded {
		return nil, errors.New("mysqlwire: server too old: need MySQL 5.5.7 or later, or MariaDB")
	}
	// The second part of the scramble is at least 13 bytes, the last a NUL.
	scramble2 := c.take(max(13, authLen-8))
	// Bug#59453: some servers end the plugin name without a NUL.
	name, _, _ := bytes.Cut(c.rest(), []byte{0})
	if c.bad {
		return nil, errMalformed("handshake")
	}
	hs.scramble = bytes.TrimSuffix(slices.Concat(scramble1, scramble2), []byte{0})
	hs.plugin = string(name)
	return hs, nil
}

// serverVersion strips the "5.5.5-" that MariaDB puts in front of its
// version for the sake of old MySQL replication clients.
func serverVersion(raw string) (version string, mariaDB bool) {
	if !strings.Contains(raw, "MariaDB") {
		return raw, false
	}
	return strings.TrimPrefix(raw, "5.5.5-"), true
}

// appendClientHeader appends the 32 bytes that open both the SSLRequest
// and the handshake response.
func appendClientHeader(b []byte, caps uint32) []byte {
	b = binary.LittleEndian.AppendUint32(b, caps)
	b = binary.LittleEndian.AppendUint32(b, maxClientPacket)
	b = append(b, charsetUTF8MB4)
	return append(b, make([]byte, 23)...)
}

// handshakeResponse builds HandshakeResponse41 carrying the first auth
// reply for plugin.
func handshakeResponse(caps uint32, user, db, plugin string, auth []byte) ([]byte, error) {
	b := appendClientHeader(nil, caps)
	b = append(append(b, user...), 0)
	if caps&clientPluginAuthLenenc != 0 {
		b = appendLenencBytes(b, auth)
	} else {
		if len(auth) > 255 {
			return nil, errors.New("mysqlwire: auth reply too long for this server")
		}
		b = append(append(b, byte(len(auth))), auth...)
	}
	if caps&clientConnectWithDB != 0 {
		b = append(append(b, db...), 0)
	}
	return append(append(b, plugin...), 0), nil
}
