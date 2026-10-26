package mysqlwire

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config says where and how to connect. Zero fields take the defaults
// noted on them.
type Config struct {
	Host       string // default "127.0.0.1"
	Port       int    // default 3306
	Socket     string // unix socket path; when set, Host/Port are ignored
	User       string
	Password   string
	Database   string        // optional
	TLS        string        // "disable", "preferred" (default), "require" (encrypt, no verification), "verify" (verify chain and host name)
	TLSCA      string        // optional PEM file used by "verify"
	ServerName string        // optional host name override for "verify"
	Timeout    time.Duration // dial + handshake timeout, default 10s
	Flavor     string        // "mysql" or "mariadb", taken from the URL scheme; informational
}

// withDefaults fills in zero fields and rejects settings Connect cannot
// use.
func (cfg Config) withDefaults() (Config, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 3306
	}
	if cfg.TLS == "" {
		cfg.TLS = "preferred"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	switch {
	case cfg.Port < 1 || cfg.Port > 65535:
		return cfg, fmt.Errorf("mysqlwire: port %d out of range", cfg.Port)
	case !validTLSMode(cfg.TLS):
		return cfg, fmt.Errorf("mysqlwire: TLS mode %q, want disable, preferred, require or verify", cfg.TLS)
	case cfg.Timeout < 0:
		return cfg, errors.New("mysqlwire: negative timeout")
	case strings.ContainsRune(cfg.User, 0) || strings.ContainsRune(cfg.Database, 0):
		// Both travel NUL-terminated in the handshake response.
		return cfg, errors.New("mysqlwire: user and database names cannot contain NUL")
	}
	return cfg, nil
}

func validTLSMode(mode string) bool {
	switch mode {
	case "disable", "preferred", "require", "verify":
		return true
	}
	return false
}

// tlsConfig builds the TLS settings for cfg.TLS. "preferred" and
// "require" encrypt without checking who answers, like the mysql client's
// --ssl-mode=PREFERRED and REQUIRED; "verify" checks the chain and the
// host name, like VERIFY_IDENTITY. On a unix socket the name checked is
// still Host unless ServerName is set.
func (cfg Config) tlsConfig() (*tls.Config, error) {
	name := cfg.ServerName
	if name == "" {
		name = cfg.Host
	}
	// ServerName also goes out as SNI (unless it is an IP address), which
	// proxies that route by host name need even when nothing is verified.
	conf := &tls.Config{ServerName: name, MinVersion: tls.VersionTLS12}
	if cfg.TLS != "verify" {
		conf.InsecureSkipVerify = true
		return conf, nil
	}
	if cfg.TLSCA != "" {
		pemData, err := os.ReadFile(cfg.TLSCA)
		if err != nil {
			return nil, fmt.Errorf("mysqlwire: TLS CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("mysqlwire: TLS CA: no certificates in %s", cfg.TLSCA)
		}
		conf.RootCAs = pool
	}
	return conf, nil
}

// Conn is a session with a MySQL or MariaDB server.
//
// A Conn is not safe for concurrent use: make one call at a time.
type Conn struct {
	cfg        Config
	raw        net.Conn // the socket; deadlines and closing act on it
	pio        *packetIO
	caps       uint32 // capabilities in effect: asked for and offered
	serverCaps uint32
	version    string
	mariaDB    bool
	connID     uint32
	unixSocket bool
	tlsActive  bool
	authPlugin string
	fullAuth   bool  // caching_sha2_password asked for the password itself
	broken     error // why the Conn is unusable; nil while it works
}

// Connect dials the server, upgrades to TLS as cfg.TLS says, logs in and
// sets the session to SET NAMES utf8mb4 and time_zone '+00:00', so that
// text and TIMESTAMP values come back the same whatever the server's
// defaults. cfg.Timeout bounds all of it, and so does ctx.
func Connect(ctx context.Context, cfg Config) (*Conn, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	var tlsConf *tls.Config
	if cfg.TLS != "disable" {
		if tlsConf, err = cfg.tlsConfig(); err != nil {
			return nil, err
		}
	}
	network, addr := "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	if cfg.Socket != "" {
		network, addr = "unix", cfg.Socket
	}
	deadline := time.Now().Add(cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	raw, err := (&net.Dialer{Deadline: deadline}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, withContext(ctx, fmt.Errorf("mysqlwire: %w", err))
	}
	c := &Conn{cfg: cfg, raw: raw, pio: newPacketIO(raw, raw), unixSocket: cfg.Socket != ""}
	if err := c.guard(ctx, deadline, func() error { return c.login(tlsConf) }); err != nil {
		raw.Close()
		return nil, err
	}
	return c, nil
}

// login runs the connection phase: handshake, TLS upgrade, authentication
// and the statements that set up the session.
func (c *Conn) login(tlsConf *tls.Config) error {
	pkt, err := c.readPacket()
	if err != nil {
		return err
	}
	hs, err := parseHandshake(pkt)
	if err != nil {
		return err
	}
	c.serverCaps = hs.caps
	c.version, c.mariaDB = serverVersion(hs.version)
	c.connID = hs.connID

	caps := uint32(clientCaps)
	if c.cfg.Database != "" {
		caps |= clientConnectWithDB
	}
	useTLS, err := c.wantTLS()
	if err != nil {
		return err
	}
	if useTLS {
		caps |= clientSSL
	}
	// Claim nothing the server lacks: what goes out is then also exactly
	// the set of capabilities in effect.
	c.caps = caps & hs.caps
	if useTLS {
		if err := c.startTLS(tlsConf); err != nil {
			return err
		}
	}

	plugin := hs.plugin
	reply, awaitingKey, err := c.authReply(plugin, hs.scramble)
	if err != nil {
		// The handshake names the server's default plugin, not the
		// account's. Answering as mysql_native_password is safe: when the
		// account needs another plugin the server asks for it with an
		// auth switch, and a plugin we cannot use fails there.
		plugin, reply, awaitingKey = pluginNative, scrambleNative(hs.scramble, c.cfg.Password), false
	}
	resp, err := handshakeResponse(c.caps, c.cfg.User, c.cfg.Database, plugin, reply)
	if err != nil {
		return err
	}
	if err := c.writePacket(resp); err != nil {
		return err
	}
	if err := c.authenticate(plugin, hs.scramble, awaitingKey); err != nil {
		return err
	}
	for _, stmt := range [...]string{"SET NAMES utf8mb4", "SET time_zone = '+00:00'"} {
		if _, err := c.query(stmt); err != nil {
			return err
		}
	}
	return nil
}

// wantTLS decides from cfg.TLS and the server's capabilities whether to
// upgrade to TLS.
func (c *Conn) wantTLS() (bool, error) {
	offered := c.serverCaps&clientSSL != 0
	switch c.cfg.TLS {
	case "disable":
		return false, nil
	case "preferred":
		// Like the mysql client, leave a unix socket alone: it never
		// leaves the host and already counts as a secure transport.
		return offered && !c.unixSocket, nil
	}
	if !offered {
		return false, fmt.Errorf("mysqlwire: tls=%s, but the server does not offer TLS", c.cfg.TLS)
	}
	return true, nil
}

// startTLS sends the SSLRequest and runs the TLS handshake. The login
// then continues inside TLS with the next sequence id.
func (c *Conn) startTLS(conf *tls.Config) error {
	if c.pio.r.Buffered() > 0 {
		// Bytes read ahead now would be lost in the switch to TLS.
		return errors.New("mysqlwire: server sent data before the TLS handshake")
	}
	if err := c.writePacket(appendClientHeader(nil, c.caps)); err != nil {
		return err
	}
	tconn := tls.Client(c.raw, conf)
	if err := tconn.Handshake(); err != nil {
		return c.fail(fmt.Errorf("mysqlwire: TLS handshake: %w", err))
	}
	c.pio.r.Reset(tconn)
	c.pio.w.Reset(tconn)
	c.tlsActive = true
	return nil
}

// Query runs sql, one statement, with COM_QUERY and returns every result
// it produced: normally one. CALL returns one per result set of the
// procedure and then a Result without columns for the CALL itself. A
// statement without rows gives a Result with no Columns and its
// AffectedRows, LastInsertID and Warnings.
//
// A server error comes back as *Error and leaves the Conn usable, as do a
// refused LOAD DATA LOCAL INFILE request and a ctx that is done before the
// call. Any other error, ctx ending mid-query included, closes the Conn,
// and later calls fail.
func (c *Conn) Query(ctx context.Context, sql string) ([]*Result, error) {
	if c.broken != nil {
		return nil, c.broken
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("mysqlwire: %w", err)
	}
	deadline, _ := ctx.Deadline()
	var results []*Result
	err := c.guard(ctx, deadline, func() (err error) {
		results, err = c.query(sql)
		return err
	})
	return results, err
}

// Close sends COM_QUIT and closes the socket, waiting at most a second
// for the server. Closing a closed or broken Conn does nothing.
func (c *Conn) Close() error {
	if c.broken != nil {
		return nil
	}
	c.broken = errors.New("mysqlwire: connection is closed")
	c.raw.SetDeadline(time.Now().Add(time.Second))
	c.pio.seq = 0
	// Best effort: the server may be gone already, and the socket closes
	// either way.
	c.pio.writePacket([]byte{comQuit})
	return c.raw.Close()
}

// ServerVersion returns the version from the handshake, without the
// "5.5.5-" prefix MariaDB adds.
func (c *Conn) ServerVersion() string { return c.version }

// IsMariaDB reports whether the server is MariaDB.
func (c *Conn) IsMariaDB() bool { return c.mariaDB }

// ConnectionID returns the server's id for this session, as
// CONNECTION_ID() reports it.
func (c *Conn) ConnectionID() uint32 { return c.connID }

// TLSActive reports whether the session runs over TLS.
func (c *Conn) TLSActive() bool { return c.tlsActive }

// AuthPlugin returns the auth plugin the server accepted the login with.
func (c *Conn) AuthPlugin() string { return c.authPlugin }

// guard runs fn with deadline on the socket and with ctx watched: when
// either expires, blocked reads and writes fail at once.
func (c *Conn) guard(ctx context.Context, deadline time.Time, fn func() error) error {
	if err := c.raw.SetDeadline(deadline); err != nil {
		return c.fail(fmt.Errorf("mysqlwire: %w", err))
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		c.raw.SetDeadline(time.Unix(1, 0))
		close(interrupted)
	})
	err := fn()
	if !stop() {
		// The callback has started. Wait for it, or its deadline could
		// land after the next call has set its own.
		<-interrupted
	}
	var serverErr *Error
	if err == nil || errors.As(err, &serverErr) {
		return err
	}
	return withContext(ctx, err)
}

// withContext puts ctx's error in front of err when ctx has ended: the
// I/O error is then only the symptom.
func withContext(ctx context.Context, err error) error {
	cause := ctx.Err()
	if cause == nil && errors.Is(err, os.ErrDeadlineExceeded) {
		// The socket deadline is ctx's deadline and can fire a moment
		// before ctx itself reports it.
		if d, ok := ctx.Deadline(); ok && !time.Now().Before(d) {
			cause = context.DeadlineExceeded
		}
	}
	if cause == nil {
		return err
	}
	return fmt.Errorf("%w: %w", cause, err)
}

// readPacket reads the next payload. Every packet from the server has at
// least a header byte, so an empty one is a protocol error too.
func (c *Conn) readPacket() ([]byte, error) {
	pkt, err := c.pio.readPacket()
	if err != nil {
		return nil, c.fail(err)
	}
	if len(pkt) == 0 {
		return nil, c.fail(errMalformed("empty"))
	}
	return pkt, nil
}

func (c *Conn) writePacket(payload []byte) error {
	if err := c.pio.writePacket(payload); err != nil {
		return c.fail(err)
	}
	return nil
}

// fail closes the connection after an I/O or protocol error, which leaves
// the position in the stream unknown, and returns err.
func (c *Conn) fail(err error) error {
	if c.broken == nil {
		c.broken = fmt.Errorf("mysqlwire: connection unusable after an earlier error (%v)", err)
		c.raw.Close()
	}
	return err
}
