package mysqlwire

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
)

// Auth plugins this client speaks.
const (
	pluginNative      = "mysql_native_password"
	pluginCachingSHA2 = "caching_sha2_password"
	pluginSHA256      = "sha256_password"
	pluginClear       = "mysql_clear_password"
)

// One-byte messages of the caching_sha2_password and sha256_password
// exchanges.
const (
	cachingFastAuthOK = 3 // server: the cached hash matched, OK follows
	cachingFullAuth   = 4 // server: send the password itself
	cachingRequestKey = 2 // client: send me your RSA public key
	sha256RequestKey  = 1 // client: the same, for sha256_password
)

// scrambleNative is the mysql_native_password reply:
// SHA1(password) XOR SHA1(scramble + SHA1(SHA1(password))).
func scrambleNative(scramble []byte, password string) []byte {
	if password == "" {
		return nil
	}
	stage1 := sha1.Sum([]byte(password))
	stage2 := sha1.Sum(stage1[:])
	h := sha1.New()
	h.Write(scramble)
	h.Write(stage2[:])
	out := h.Sum(nil)
	for i := range out {
		out[i] ^= stage1[i]
	}
	return out
}

// scrambleCachingSHA2 is the caching_sha2_password reply:
// SHA256(password) XOR SHA256(SHA256(SHA256(password)) + scramble).
func scrambleCachingSHA2(scramble []byte, password string) []byte {
	if password == "" {
		return nil
	}
	stage1 := sha256.Sum256([]byte(password))
	stage2 := sha256.Sum256(stage1[:])
	h := sha256.New()
	h.Write(stage2[:])
	h.Write(scramble)
	out := h.Sum(nil)
	for i := range out {
		out[i] ^= stage1[i]
	}
	return out
}

// encryptPassword answers a request for the password on a connection that
// is neither TLS nor a unix socket: the NUL-terminated password XORed with
// the scramble, RSA-OAEP (SHA-1) encrypted under the server's public key.
// The XOR ties the ciphertext to this session's scramble.
func encryptPassword(password string, scramble, pemKey []byte) ([]byte, error) {
	if len(scramble) == 0 {
		return nil, errors.New("mysqlwire: server sent an empty scramble")
	}
	block, _ := pem.Decode(pemKey)
	if block == nil {
		return nil, errors.New("mysqlwire: server sent no PEM public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("mysqlwire: server public key: %w", err)
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("mysqlwire: server public key is not RSA")
	}
	plain := append([]byte(password), 0)
	for i := range plain {
		plain[i] ^= scramble[i%len(scramble)]
	}
	enc, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, pub, plain, nil)
	if err != nil {
		return nil, fmt.Errorf("mysqlwire: encrypt password: %w", err)
	}
	return enc, nil
}

// errUnsupportedPlugin names the plugin, which is what it takes to pick
// another account or have its plugin changed.
func errUnsupportedPlugin(name string) error {
	return fmt.Errorf("mysqlwire: unsupported auth plugin %q", name)
}

// secureTransport reports whether a password may travel in clear: over
// TLS, or over a unix socket, which never leaves the host.
func (c *Conn) secureTransport() bool {
	return c.tlsActive || c.unixSocket
}

// authReply returns the first reply for plugin. awaitingKey reports that
// the reply asks for the server's RSA public key.
func (c *Conn) authReply(plugin string, scramble []byte) (reply []byte, awaitingKey bool, err error) {
	pw := c.cfg.Password
	switch plugin {
	case pluginNative:
		return scrambleNative(scramble, pw), false, nil
	case pluginCachingSHA2:
		return scrambleCachingSHA2(scramble, pw), false, nil
	case pluginSHA256:
		switch {
		case pw == "":
			return nil, false, nil
		case c.tlsActive:
			// Unlike caching_sha2_password, the server takes this plugin's
			// password in clear only over TLS, not over a unix socket.
			return append([]byte(pw), 0), false, nil
		}
		return []byte{sha256RequestKey}, true, nil
	case pluginClear:
		if !c.secureTransport() {
			return nil, false, errors.New("mysqlwire: refusing to send the password in clear (mysql_clear_password) over an unencrypted connection")
		}
		if pw == "" {
			return nil, false, nil
		}
		return append([]byte(pw), 0), false, nil
	}
	return nil, false, errUnsupportedPlugin(plugin)
}

// authenticate reads the server's answer to the handshake response and
// carries the exchange on, through auth switches and the extra round
// trips of caching_sha2_password and sha256_password, until the server
// accepts or refuses the login.
func (c *Conn) authenticate(plugin string, scramble []byte, awaitingKey bool) error {
	for {
		pkt, err := c.readPacket()
		if err != nil {
			return err
		}
		switch pkt[0] {
		case 0x00:
			c.authPlugin = plugin
			return nil
		case 0xFF:
			return c.serverError(pkt)
		case 0xFE:
			if plugin, scramble, err = parseAuthSwitch(pkt); err != nil {
				return err
			}
			var reply []byte
			if reply, awaitingKey, err = c.authReply(plugin, scramble); err != nil {
				return err
			}
			if err := c.writePacket(reply); err != nil {
				return err
			}
		default:
			// More plugin data. MySQL 8 always puts 0x01 in front of it;
			// MySQL 5.7 and MariaDB send it bare unless its first byte
			// would read as a packet type, which they escape with 0x01.
			data := pkt
			if pkt[0] == 0x01 {
				data = pkt[1:]
			}
			if awaitingKey, err = c.authMoreData(plugin, scramble, data, awaitingKey); err != nil {
				return err
			}
		}
	}
}

// authMoreData acts on plugin data from the server and reports whether a
// request for the server's public key is now outstanding.
func (c *Conn) authMoreData(plugin string, scramble, data []byte, awaitingKey bool) (bool, error) {
	if awaitingKey {
		enc, err := encryptPassword(c.cfg.Password, scramble, data)
		if err != nil {
			return false, err
		}
		return false, c.writePacket(enc)
	}
	if plugin == pluginCachingSHA2 && len(data) == 1 {
		switch data[0] {
		case cachingFastAuthOK:
			return false, nil
		case cachingFullAuth:
			// The server has no cached hash for the account (first login
			// since it started or ran FLUSH PRIVILEGES) and needs the
			// password itself.
			c.fullAuth = true
			if c.secureTransport() {
				return false, c.writePacket(append([]byte(c.cfg.Password), 0))
			}
			return true, c.writePacket([]byte{cachingRequestKey})
		}
	}
	return false, fmt.Errorf("mysqlwire: unexpected data in the %s exchange", plugin)
}

// parseAuthSwitch decodes an AuthSwitchRequest: 0xFE, the plugin name,
// then the plugin's data, which is the new scramble and a NUL. A lone 0xFE
// is the pre-4.1 request for mysql_old_password.
func parseAuthSwitch(b []byte) (plugin string, scramble []byte, err error) {
	if len(b) == 1 {
		return "", nil, errUnsupportedPlugin("mysql_old_password")
	}
	c := cursor{b: b[1:]}
	name := c.nulBytes()
	data := c.rest()
	if c.bad {
		return "", nil, errMalformed("auth switch")
	}
	return string(name), bytes.TrimSuffix(slices.Clone(data), []byte{0}), nil
}
