// Package pgwire is a small PostgreSQL client speaking protocol 3.0: TLS
// (with libpq's sslmode rules), SCRAM-SHA-256 with channel binding, MD5 and
// cleartext passwords, unix sockets, simple and extended queries with every
// value as text. It is what BareSQL needs and no more.
package pgwire

import (
	"bufio"
	"context"
	"crypto/md5"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	protocol30     = 196608 // 3.0
	sslRequestCode = 80877103
	cancelCode     = 80877102
)

// Conn is one connection to a PostgreSQL server. It is not safe for
// concurrent use; open one Conn per goroutine.
type Conn struct {
	cfg    Config
	nc     net.Conn
	br     *bufio.Reader
	hdr    [5]byte
	params map[string]string
	pid    uint32
	key    []byte
	status byte // transaction status from the last ReadyForQuery: I, T or E
	tlsOn  bool
	auth   string
	broken error // set when the connection can't be used any more

	scramMaxIter int // 0 means maxSCRAMIterations; fuzz tests lower it
}

// Connect dials the server, negotiates TLS as cfg.SSLMode says and logs in.
// cfg.ConnectTimeout and ctx both bound the whole setup.
func Connect(ctx context.Context, cfg Config) (*Conn, error) {
	if err := cfg.fillDefaults(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	nc, err := dial(ctx, cfg)
	if err != nil {
		return nil, err
	}
	c := &Conn{cfg: cfg, nc: nc, params: map[string]string{}}
	// A deadline in the past wakes up any read or write in progress.
	fired := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		nc.SetDeadline(time.Unix(1, 0))
		close(fired)
	})
	err = c.startup()
	if !stop() {
		<-fired
		err = fmt.Errorf("pgwire: connecting to %s: %w", cfg.Redacted(), ctx.Err())
	}
	if err != nil {
		c.nc.Close()
		return nil, err
	}
	return c, nil
}

func dial(ctx context.Context, cfg Config) (net.Conn, error) {
	var d net.Dialer
	if cfg.IsUnixSocket() {
		path := cfg.Host
		if !strings.HasPrefix(path, "@") {
			path = filepath.Join(cfg.Host, ".s.PGSQL."+strconv.Itoa(cfg.Port))
		}
		nc, err := d.DialContext(ctx, "unix", path)
		if err != nil {
			return nil, fmt.Errorf("pgwire: connecting to socket %s: %w", path, err)
		}
		return nc, nil
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("pgwire: connecting to %s: %w", addr, err)
	}
	return nc, nil
}

func (c *Conn) startup() error {
	if c.cfg.SSLMode != "disable" && !c.cfg.IsUnixSocket() {
		if err := c.negotiateTLS(); err != nil {
			return err
		}
	}
	c.br = bufio.NewReaderSize(c.nc, 32<<10)

	m := begin(0).int32(protocol30).str("user").str(c.cfg.User)
	if c.cfg.Database != "" {
		m.str("database").str(c.cfg.Database)
	}
	m.str("client_encoding").str("UTF8")
	if _, ok := c.cfg.Params["application_name"]; !ok {
		m.str("application_name").str("baresql")
	}
	for k, v := range c.cfg.Params {
		if k == "user" || k == "database" || k == "client_encoding" {
			continue
		}
		m.str(k).str(v)
	}
	m.b = append(m.b, 0)
	if err := c.write(m.done()); err != nil {
		return err
	}
	if err := c.authenticate(); err != nil {
		return err
	}
	// Then ParameterStatus and BackendKeyData until ReadyForQuery.
	for {
		typ, body, err := c.read()
		if err != nil {
			return err
		}
		switch typ {
		case 'Z':
			c.status = firstByte(body)
			return nil
		case 'E':
			return parseError(body)
		case 'K':
			r := &reader{b: body}
			c.pid = uint32(r.int32())
			c.key = append([]byte(nil), r.rest()...)
		case 'S', 'N':
			c.handleAsync(typ, body)
		default:
			return fmt.Errorf("pgwire: unexpected message %q during startup", typ)
		}
	}
}

// negotiateTLS sends SSLRequest and, if the server agrees, wraps the
// connection. The server's one-byte answer is read straight from the
// socket: nothing the server sends before the handshake can end up treated
// as encrypted data (CVE-2021-23222).
func (c *Conn) negotiateTLS() error {
	if err := c.write(begin(0).int32(sslRequestCode).done()); err != nil {
		return err
	}
	var b [1]byte
	if _, err := io.ReadFull(c.nc, b[:]); err != nil {
		return fmt.Errorf("pgwire: reading the answer to SSLRequest: %w", err)
	}
	switch b[0] {
	case 'N':
		if c.cfg.SSLMode == "prefer" {
			return nil
		}
		return fmt.Errorf("pgwire: sslmode=%s, but the server does not offer TLS", c.cfg.SSLMode)
	case 'S':
	case 'E':
		// A server too old to know SSLRequest answers with an error.
		return errors.New("pgwire: the server rejected SSLRequest")
	default:
		return fmt.Errorf("pgwire: unexpected answer %q to SSLRequest", b[0])
	}
	tc, err := c.tlsConfig()
	if err != nil {
		return err
	}
	conn := tls.Client(c.nc, tc)
	if err := conn.Handshake(); err != nil {
		return fmt.Errorf("pgwire: TLS handshake: %w", err)
	}
	c.nc = conn
	c.tlsOn = true
	return nil
}

func (c *Conn) tlsConfig() (*tls.Config, error) {
	cfg := c.cfg
	serverName := cfg.ServerName
	if serverName == "" {
		serverName = cfg.Host
	}
	tc := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}
	if cfg.SSLCert != "" || cfg.SSLKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.SSLCert, cfg.SSLKey)
		if err != nil {
			return nil, fmt.Errorf("pgwire: client certificate: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	mode := cfg.SSLMode
	if mode == "require" && cfg.SSLRootCert != "" {
		mode = "verify-ca" // libpq: require with a root certificate verifies the chain
	}
	if mode == "prefer" || mode == "require" {
		tc.InsecureSkipVerify = true
		return tc, nil
	}
	var roots *x509.CertPool
	if cfg.SSLRootCert == "system" {
		var err error
		if roots, err = x509.SystemCertPool(); err != nil {
			return nil, fmt.Errorf("pgwire: system certificates: %w", err)
		}
	} else {
		path := cfg.SSLRootCert
		if path == "" {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, ".postgresql", "root.crt")
		}
		pem, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("pgwire: sslmode=%s needs a root certificate (sslrootcert): %w", mode, err)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("pgwire: no certificates in %s", path)
		}
	}
	if mode == "verify-full" {
		tc.RootCAs = roots
		return tc, nil
	}
	// verify-ca: check the chain, not the name.
	tc.InsecureSkipVerify = true
	tc.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("pgwire: server sent no certificate")
		}
		inter := x509.NewCertPool()
		for _, ic := range cs.PeerCertificates[1:] {
			inter.AddCert(ic)
		}
		_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
		return err
	}
	return tc, nil
}

func (c *Conn) authenticate() error {
	for {
		typ, body, err := c.read()
		if err != nil {
			return err
		}
		switch typ {
		case 'E':
			return parseError(body)
		case 'N':
			continue
		case 'v':
			// NegotiateProtocolVersion: we asked for 3.0 with no extensions,
			// so there is nothing to give up; carry on.
			continue
		case 'R':
		default:
			return fmt.Errorf("pgwire: unexpected message %q during login", typ)
		}
		r := &reader{b: body}
		switch code := r.int32(); code {
		case 0:
			if c.auth == "" {
				c.auth = "trust"
			}
			return nil
		case 3:
			c.auth = "password"
			if err := c.write(begin('p').str(c.cfg.Password).done()); err != nil {
				return err
			}
		case 5:
			salt := r.next(4)
			if r.err != nil {
				return r.err
			}
			c.auth = "md5"
			if err := c.write(begin('p').str(md5Password(c.cfg.User, c.cfg.Password, salt)).done()); err != nil {
				return err
			}
		case 10:
			var mechs []string
			for r.err == nil && len(r.b) > 1 {
				mechs = append(mechs, r.cstr())
			}
			if err := c.scramLogin(mechs); err != nil {
				return err
			}
		case 2:
			return errors.New("pgwire: the server asks for Kerberos V5, which is not supported")
		case 7, 8, 9:
			return errors.New("pgwire: the server asks for GSSAPI/SSPI login, which is not supported")
		default:
			return fmt.Errorf("pgwire: the server asks for an unsupported login method (%d)", code)
		}
	}
}

func md5Password(user, password string, salt []byte) string {
	inner := md5.Sum([]byte(password + user))
	h := md5.New()
	h.Write([]byte(hex.EncodeToString(inner[:])))
	h.Write(salt)
	return "md5" + hex.EncodeToString(h.Sum(nil))
}

func (c *Conn) scramLogin(mechs []string) error {
	var plus, plain bool
	for _, m := range mechs {
		plus = plus || m == "SCRAM-SHA-256-PLUS"
		plain = plain || m == "SCRAM-SHA-256"
	}
	var cbind []byte
	if plus && c.tlsOn && c.cfg.ChannelBinding != "disable" {
		if tc, ok := c.nc.(*tls.Conn); ok {
			if certs := tc.ConnectionState().PeerCertificates; len(certs) > 0 {
				if sum, ok := endPointHash(certs[0]); ok {
					cbind = sum
				}
			}
		}
	}
	mech, gs2 := "SCRAM-SHA-256", "n,,"
	switch {
	case cbind != nil:
		mech, gs2 = "SCRAM-SHA-256-PLUS", "p=tls-server-end-point,,"
	case c.cfg.ChannelBinding == "require":
		return errors.New("pgwire: channel_binding=require, but the server or connection can't bind (needs TLS and SCRAM-SHA-256-PLUS)")
	case !plain:
		return fmt.Errorf("pgwire: the server offers no SASL method this client knows (%s)", strings.Join(mechs, ", "))
	case c.tlsOn && c.cfg.ChannelBinding != "disable":
		gs2 = "y,," // we could bind, the server didn't offer it
	}
	s, err := newSCRAM(c.cfg.Password, gs2, cbind)
	if err != nil {
		return err
	}
	s.maxIter = c.scramMaxIter
	first := s.clientFirst()
	m := begin('p').str(mech).int32(int32(len(first))).bytes(first)
	if err := c.write(m.done()); err != nil {
		return err
	}
	body, err := c.expectAuth(11)
	if err != nil {
		return err
	}
	final, err := s.clientFinal(body)
	if err != nil {
		return err
	}
	if err := c.write(begin('p').bytes(final).done()); err != nil {
		return err
	}
	body, err = c.expectAuth(12)
	if err != nil {
		return err
	}
	if err := s.verifyServerFinal(body); err != nil {
		return err
	}
	c.auth = strings.ToLower(mech)
	return nil
}

// expectAuth reads an Authentication message with the given code and
// returns its data.
func (c *Conn) expectAuth(want int32) ([]byte, error) {
	for {
		typ, body, err := c.read()
		if err != nil {
			return nil, err
		}
		switch typ {
		case 'E':
			return nil, parseError(body)
		case 'N':
			continue
		case 'R':
			r := &reader{b: body}
			if code := r.int32(); r.err != nil || code != want {
				return nil, fmt.Errorf("pgwire: unexpected step in SCRAM login (got %d, want %d)", code, want)
			}
			return r.rest(), nil
		default:
			return nil, fmt.Errorf("pgwire: unexpected message %q during SCRAM login", typ)
		}
	}
}

func (c *Conn) read() (byte, []byte, error) {
	r := io.Reader(c.nc)
	if c.br != nil {
		r = c.br
	}
	typ, body, err := readMessage(r, c.hdr[:])
	if err != nil {
		return 0, nil, err
	}
	return typ, body, nil
}

func (c *Conn) write(b []byte) error {
	_, err := c.nc.Write(b)
	return err
}

// handleAsync deals with messages that can arrive at any time.
func (c *Conn) handleAsync(typ byte, body []byte) {
	if typ == 'S' {
		r := &reader{b: body}
		k, v := r.cstr(), r.cstr()
		if r.err == nil {
			c.params[k] = v
		}
	}
}

// Param returns a run-time parameter the server reported, such as
// server_version, server_encoding or integer_datetimes.
func (c *Conn) Param(name string) string { return c.params[name] }

// ServerVersion is the server_version the server reported, for example
// "16.15 (Ubuntu 16.15-0ubuntu0.24.04.1)".
func (c *Conn) ServerVersion() string { return c.params["server_version"] }

// TLSActive reports whether the connection is encrypted.
func (c *Conn) TLSActive() bool { return c.tlsOn }

// AuthMethod is how the login went: trust, password (cleartext), md5,
// scram-sha-256 or scram-sha-256-plus (bound to the TLS channel).
func (c *Conn) AuthMethod() string { return c.auth }

// ProcessID is the server process serving this connection.
func (c *Conn) ProcessID() uint32 { return c.pid }

// TxStatus is the transaction status after the last command: 'I' idle,
// 'T' in a transaction, 'E' in a failed transaction.
func (c *Conn) TxStatus() byte { return c.status }

// Config returns the settings the connection was made with (password
// included; use Config.Redacted for display).
func (c *Conn) Config() Config { return c.cfg }

// Close says goodbye and closes the socket.
func (c *Conn) Close() error {
	if c.nc == nil {
		return nil
	}
	if c.broken == nil {
		c.nc.SetWriteDeadline(time.Now().Add(time.Second))
		c.write(begin('X').done())
	}
	err := c.nc.Close()
	c.nc = nil
	c.broken = errors.New("pgwire: connection closed")
	return err
}

// cancelRequest asks the server, over a new connection, to stop the
// query this connection is running.
func (c *Conn) cancelRequest() {
	if len(c.key) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	nc, err := dial(ctx, c.cfg)
	if err != nil {
		return
	}
	defer nc.Close()
	nc.SetDeadline(time.Now().Add(3 * time.Second))
	m := begin(0).int32(cancelCode).int32(int32(c.pid)).bytes(c.key)
	nc.Write(m.done())
	// The server closes the connection when it is done; wait for that so
	// the cancel isn't lost when we close first.
	var b [1]byte
	nc.Read(b[:])
}

func firstByte(b []byte) byte {
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// be32 is used by tests building server messages.
func be32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
