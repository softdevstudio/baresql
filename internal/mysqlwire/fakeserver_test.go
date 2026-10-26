package mysqlwire

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSession is the server side of one connection, driven by a test
// script. Scripts report mistakes with t.Errorf and stop at the first I/O
// error, which is often the client giving up on purpose.
type fakeSession struct {
	t    *testing.T
	conn net.Conn
	pio  *packetIO
	cert *tls.Certificate // offered when the client sends an SSLRequest
}

// startFake accepts one connection on network ("tcp" or "unix") and runs
// script on it. The Config it returns points there, with user "u",
// password "pw" and tls=disable.
func startFake(t *testing.T, network string, cert *tls.Certificate, script func(s *fakeSession)) Config {
	t.Helper()
	addr := "127.0.0.1:0"
	if network == "unix" {
		// A short directory: socket paths are limited to about 100 bytes.
		dir, err := os.MkdirTemp("", "mw")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		addr = filepath.Join(dir, "s")
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		script(&fakeSession{t: t, conn: conn, pio: newPacketIO(conn, conn), cert: cert})
	}()
	t.Cleanup(func() {
		ln.Close()
		<-done
	})
	cfg := Config{User: "u", Password: "pw", TLS: "disable", Timeout: 5 * time.Second}
	if network == "unix" {
		cfg.Socket = addr
	} else {
		cfg.Host, cfg.Port = "127.0.0.1", ln.Addr().(*net.TCPAddr).Port
	}
	return cfg
}

// clientHello is a parsed HandshakeResponse41.
type clientHello struct {
	caps   uint32
	user   string
	auth   []byte
	db     string
	plugin string
	tls    bool // the response came after a TLS upgrade
}

func parseClientHello(b []byte) (clientHello, error) {
	c := cursor{b: b}
	h := clientHello{caps: c.u32()}
	c.u32() // max packet size
	if cs := c.u8(); cs != charsetUTF8MB4 && !c.bad {
		return h, errors.New("handshake response charset is not utf8mb4_general_ci")
	}
	c.take(23)
	h.user = string(c.nulBytes())
	if h.caps&clientPluginAuthLenenc != 0 {
		h.auth = c.lenencBytes()
	} else {
		h.auth = c.take(int(c.u8()))
	}
	if h.caps&clientConnectWithDB != 0 {
		h.db = string(c.nulBytes())
	}
	h.plugin = string(c.nulBytes())
	if c.bad || len(c.b) != 0 {
		return h, errMalformed("handshake response")
	}
	return h, nil
}

// accept sends the handshake and reads the client's response, doing the
// TLS upgrade in between when the client asks for it.
func (s *fakeSession) accept(h hsSpec) (clientHello, error) {
	if err := s.pio.writePacket(h.build()); err != nil {
		return clientHello{}, err
	}
	b, err := s.pio.readPacket()
	if err != nil {
		return clientHello{}, err
	}
	upgraded := false
	if len(b) == 32 { // SSLRequest: the response's first 32 bytes alone
		if binary.LittleEndian.Uint32(b)&clientSSL == 0 || s.cert == nil {
			s.t.Errorf("unexpected SSLRequest % x", b)
			return clientHello{}, errors.New("unexpected SSLRequest")
		}
		// The client sends its TLS hello right behind the SSLRequest, so
		// part of it may sit in the reader's buffer already.
		tconn := tls.Server(bufferedConn{s.conn, s.pio.r}, &tls.Config{Certificates: []tls.Certificate{*s.cert}})
		if err := tconn.Handshake(); err != nil {
			return clientHello{}, err
		}
		s.pio = &packetIO{r: bufio.NewReader(tconn), w: bufio.NewWriter(tconn), seq: s.pio.seq}
		upgraded = true
		if b, err = s.pio.readPacket(); err != nil {
			return clientHello{}, err
		}
	}
	hello, err := parseClientHello(b)
	hello.tls = upgraded
	if err != nil {
		s.t.Error(err)
	}
	return hello, err
}

// bufferedConn reads through r, which holds what was read ahead of the
// TLS handshake.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// finish accepts the login and answers the two session statements.
func (s *fakeSession) finish() error {
	if err := s.pio.writePacket(okPacketBytes(0x00, 0, 0, 2, 0)); err != nil {
		return err
	}
	for _, want := range []string{"SET NAMES utf8mb4", "SET time_zone = '+00:00'"} {
		s.pio.seq = 0
		cmd, err := s.pio.readPacket()
		if err != nil {
			return err
		}
		if string(cmd) != "\x03"+want {
			s.t.Errorf("session setup sent %q, want %q", cmd, want)
		}
		if err := s.pio.writePacket(okPacketBytes(0x00, 0, 0, 2, 0)); err != nil {
			return err
		}
	}
	return nil
}

// expectQuit checks that the client says goodbye with COM_QUIT.
func (s *fakeSession) expectQuit() {
	s.pio.seq = 0
	if cmd, err := s.pio.readPacket(); err != nil || !bytes.Equal(cmd, []byte{comQuit}) {
		s.t.Errorf("want COM_QUIT, got %q, %v", cmd, err)
	}
}

// expectGone checks that the client hung up without sending more.
func (s *fakeSession) expectGone() {
	if b, err := s.pio.readPacket(); err == nil {
		s.t.Errorf("client sent % x, want it to hang up", b)
	}
}

// expect reads the client's next auth packet and compares it with want.
func (s *fakeSession) expect(what string, want []byte) bool {
	got, err := s.pio.readPacket()
	if err != nil {
		s.t.Errorf("%s: %v", what, err)
		return false
	}
	if !bytes.Equal(got, want) {
		s.t.Errorf("%s: client sent % x, want % x", what, got, want)
		return false
	}
	return true
}

func authSwitch(plugin string, scramble []byte) []byte {
	b := append(append([]byte{0xFE}, plugin...), 0)
	return append(append(b, scramble...), 0)
}

// testCerts returns a server certificate for 127.0.0.1 and fake.test,
// signed by a fresh CA, and that CA as a PEM file.
func testCerts(t *testing.T) (*tls.Certificate, string) {
	t.Helper()
	newKey := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	caKey, leafKey := newKey(), newKey()
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mysqlwire test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	if ca, err = x509.ParseCertificate(caDER); err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "fake.test"},
		DNSNames: []string{"fake.test"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}, caFile
}

func TestConnectHandshakeResponse(t *testing.T) {
	cases := []struct {
		name string
		hs   hsSpec
	}{{"mysql", mysqlHandshake()}, {"mariadb", mariadbHandshake()}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.hs.caps &^= clientSSL
			cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
				hello, err := s.accept(tc.hs)
				if err != nil {
					return
				}
				if hello.user != "u" || hello.db != "shop" || hello.plugin != tc.hs.plugin {
					t.Errorf("hello %+v", hello)
				}
				want := scrambleNative(tc.hs.scramble, "pw")
				if tc.hs.plugin == pluginCachingSHA2 {
					want = scrambleCachingSHA2(tc.hs.scramble, "pw")
				}
				if !bytes.Equal(hello.auth, want) {
					t.Errorf("auth reply % x, want % x", hello.auth, want)
				}
				// Only what the server offers, never multi-statements or
				// local files, and the database only when there is one.
				if extra := hello.caps &^ tc.hs.caps; extra != 0 {
					t.Errorf("client claims capabilities %#x the server lacks", extra)
				}
				for _, flag := range []uint32{1 << 16, 1 << 7} {
					if hello.caps&flag != 0 {
						t.Errorf("client asked for capability %#x", flag)
					}
				}
				for _, flag := range []uint32{clientProtocol41, clientSecureConnection, clientPluginAuth, clientMultiResults, clientDeprecateEOF, clientConnectWithDB} {
					if hello.caps&flag == 0 {
						t.Errorf("client did not ask for capability %#x", flag)
					}
				}
				if s.finish() == nil {
					s.expectQuit()
				}
			})
			cfg.Database = "shop"
			c, err := Connect(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			wantVersion, wantMaria := serverVersion(tc.hs.version)
			if c.ServerVersion() != wantVersion || c.IsMariaDB() != wantMaria || c.ConnectionID() != tc.hs.connID ||
				c.TLSActive() || c.AuthPlugin() != tc.hs.plugin {
				t.Errorf("version %q, MariaDB %v, id %d, TLS %v, plugin %q", c.ServerVersion(), c.IsMariaDB(), c.ConnectionID(), c.TLSActive(), c.AuthPlugin())
			}
			if err := c.Close(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestConnectAuthSwitch(t *testing.T) {
	newScramble := []byte("abcdefghij0123456789")
	cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
		hs := mysqlHandshake()
		if _, err := s.accept(hs); err != nil {
			return
		}
		if s.pio.writePacket(authSwitch(pluginNative, newScramble)) != nil {
			return
		}
		if s.expect("reply to the switch", scrambleNative(newScramble, "pw")) && s.finish() == nil {
			s.expectQuit()
		}
	})
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.AuthPlugin() != pluginNative {
		t.Errorf("AuthPlugin %q", c.AuthPlugin())
	}
}

// TestConnectUnknownDefaultPlugin: the handshake names a default plugin
// the client lacks, so it answers as mysql_native_password, which the
// account turns out to use.
func TestConnectUnknownDefaultPlugin(t *testing.T) {
	hs := mariadbHandshake()
	hs.plugin = "auth_gssapi_client"
	cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
		hello, err := s.accept(hs)
		if err != nil {
			return
		}
		if hello.plugin != pluginNative || !bytes.Equal(hello.auth, scrambleNative(hs.scramble, "pw")) {
			t.Errorf("hello %+v", hello)
		}
		if hello.caps&clientConnectWithDB != 0 {
			t.Error("CLIENT_CONNECT_WITH_DB set without a database")
		}
		if s.finish() == nil {
			s.expectQuit()
		}
	})
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.AuthPlugin() != pluginNative {
		t.Errorf("AuthPlugin %q", c.AuthPlugin())
	}
}

func TestConnectUnsupportedPlugin(t *testing.T) {
	for _, plugin := range []string{"client_ed25519", "auth_gssapi_client", "parsec"} {
		cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
			if _, err := s.accept(mariadbHandshake()); err != nil {
				return
			}
			if s.pio.writePacket(authSwitch(plugin, []byte("12345678901234567890"))) == nil {
				s.expectGone()
			}
		})
		_, err := Connect(context.Background(), cfg)
		if err == nil || !strings.Contains(err.Error(), `"`+plugin+`"`) {
			t.Errorf("switch to %s: %v", plugin, err)
		}
	}
}

// TestConnectFullAuthRSA covers the full-authentication path without TLS
// or a socket, as MySQL 8 frames it (0x01 in front of plugin data) and as
// MySQL 5.7 and MariaDB do (plugin data bare).
func TestConnectFullAuthRSA(t *testing.T) {
	key, pemKey := rsaPEM(t)
	for _, prefixed := range []bool{true, false} {
		data := func(b []byte) []byte {
			if prefixed {
				return append([]byte{0x01}, b...)
			}
			return b
		}
		t.Run("caching_sha2_password", func(t *testing.T) {
			hs := mysqlHandshake()
			cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
				if _, err := s.accept(hs); err != nil {
					return
				}
				if s.pio.writePacket(data([]byte{cachingFullAuth})) != nil || !s.expect("key request", []byte{cachingRequestKey}) {
					return
				}
				if s.pio.writePacket(data(pemKey)) != nil {
					return
				}
				enc, err := s.pio.readPacket()
				if err != nil {
					t.Error(err)
					return
				}
				if got := decryptPassword(t, key, hs.scramble, enc); got != "pw\x00" {
					t.Errorf("server decrypted %q", got)
				}
				if s.finish() == nil {
					s.expectQuit()
				}
			})
			c, err := Connect(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if !c.fullAuth || c.AuthPlugin() != pluginCachingSHA2 {
				t.Errorf("full auth %v, plugin %q", c.fullAuth, c.AuthPlugin())
			}
		})
		t.Run("sha256_password", func(t *testing.T) {
			hs := mysqlHandshake()
			hs.plugin = pluginSHA256
			cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
				hello, err := s.accept(hs)
				if err != nil {
					return
				}
				if !bytes.Equal(hello.auth, []byte{sha256RequestKey}) {
					t.Errorf("first reply % x, want a key request", hello.auth)
				}
				if s.pio.writePacket(data(pemKey)) != nil {
					return
				}
				enc, err := s.pio.readPacket()
				if err != nil {
					t.Error(err)
					return
				}
				if got := decryptPassword(t, key, hs.scramble, enc); got != "pw\x00" {
					t.Errorf("server decrypted %q", got)
				}
				if s.finish() == nil {
					s.expectQuit()
				}
			})
			c, err := Connect(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			c.Close()
		})
	}
}

// TestConnectClearTextPaths checks where the password may go in clear:
// mysql_clear_password and caching_sha2_password's full authentication
// over TLS and a unix socket, never over plain TCP.
func TestConnectClearTextPaths(t *testing.T) {
	cert, _ := testCerts(t)
	cases := []struct {
		name, network, tlsMode string
		plugin                 string
	}{
		{"clear over TLS", "tcp", "require", pluginClear},
		{"clear over a unix socket", "unix", "disable", pluginClear},
		{"caching_sha2 full auth over TLS", "tcp", "require", pluginCachingSHA2},
		{"caching_sha2 full auth over a unix socket", "unix", "disable", pluginCachingSHA2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := startFake(t, tc.network, cert, func(s *fakeSession) {
				hello, err := s.accept(mysqlHandshake())
				if err != nil {
					return
				}
				if hello.tls != (tc.tlsMode == "require") {
					t.Errorf("TLS upgrade %v", hello.tls)
				}
				if tc.plugin == pluginClear {
					err = s.pio.writePacket(authSwitch(pluginClear, nil))
				} else {
					err = s.pio.writePacket([]byte{0x01, cachingFullAuth})
				}
				if err == nil && s.expect("password", []byte("pw\x00")) && s.finish() == nil {
					s.expectQuit()
				}
			})
			cfg.TLS = tc.tlsMode
			c, err := Connect(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			c.Close()
		})
	}
	t.Run("clear refused over plain TCP", func(t *testing.T) {
		cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
			if _, err := s.accept(mysqlHandshake()); err == nil && s.pio.writePacket(authSwitch(pluginClear, nil)) == nil {
				s.expectGone()
			}
		})
		if _, err := Connect(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "mysql_clear_password") {
			t.Errorf("got %v", err)
		}
	})
}

func TestConnectTLSModes(t *testing.T) {
	cert, caFile := testCerts(t)
	_, otherCA := testCerts(t)
	cases := []struct {
		name       string
		offerTLS   bool
		cfg        Config
		wantTLS    bool
		wantErr    string // empty: the login succeeds
		serverDone bool   // the server gets as far as the handshake response
	}{
		{"disable", true, Config{TLS: "disable"}, false, "", true},
		{"preferred with TLS", true, Config{TLS: "preferred"}, true, "", true},
		{"preferred without TLS", false, Config{TLS: "preferred"}, false, "", true},
		{"require", true, Config{TLS: "require"}, true, "", true},
		{"require without TLS", false, Config{TLS: "require"}, false, "does not offer TLS", false},
		{"verify", true, Config{TLS: "verify", TLSCA: caFile}, true, "", true},
		{"verify by name", true, Config{TLS: "verify", TLSCA: caFile, ServerName: "fake.test"}, true, "", true},
		{"verify wrong name", true, Config{TLS: "verify", TLSCA: caFile, ServerName: "other.test"}, false, "certificate is valid for", false},
		{"verify other CA", true, Config{TLS: "verify", TLSCA: otherCA}, false, "unknown authority", false},
		{"verify without TLS", false, Config{TLS: "verify", TLSCA: caFile}, false, "does not offer TLS", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := mysqlHandshake()
			if !tc.offerTLS {
				hs.caps &^= clientSSL
			}
			cfg := startFake(t, "tcp", cert, func(s *fakeSession) {
				hello, err := s.accept(hs)
				if err != nil {
					if tc.serverDone {
						t.Errorf("server: %v", err)
					}
					return
				}
				if hello.tls != tc.wantTLS || (hello.caps&clientSSL != 0) != tc.wantTLS {
					t.Errorf("upgrade %v, CLIENT_SSL %v; want %v", hello.tls, hello.caps&clientSSL != 0, tc.wantTLS)
				}
				if s.finish() == nil {
					s.expectQuit()
				}
			})
			cfg.TLS, cfg.TLSCA, cfg.ServerName = tc.cfg.TLS, tc.cfg.TLSCA, tc.cfg.ServerName
			c, err := Connect(context.Background(), cfg)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got %v, want an error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.TLSActive() != tc.wantTLS {
				t.Errorf("TLSActive %v", c.TLSActive())
			}
			c.Close()
		})
	}
	t.Run("bad CA file", func(t *testing.T) {
		_, err := Connect(context.Background(), Config{TLS: "verify", TLSCA: filepath.Join(t.TempDir(), "missing.pem")})
		if err == nil || !strings.Contains(err.Error(), "TLS CA") {
			t.Errorf("got %v", err)
		}
	})
}

// TestConnectSocketSkipsTLS: on a unix socket "preferred" stays plain, as
// with the mysql client, while "require" still upgrades.
func TestConnectSocketSkipsTLS(t *testing.T) {
	cert, _ := testCerts(t)
	for _, mode := range []string{"preferred", "require"} {
		cfg := startFake(t, "unix", cert, func(s *fakeSession) {
			hello, err := s.accept(mysqlHandshake())
			if err != nil {
				return
			}
			if hello.tls != (mode == "require") {
				t.Errorf("tls=%s over a socket: upgrade %v", mode, hello.tls)
			}
			if s.finish() == nil {
				s.expectQuit()
			}
		})
		cfg.TLS = mode
		c, err := Connect(context.Background(), cfg)
		if err != nil {
			t.Fatalf("tls=%s: %v", mode, err)
		}
		c.Close()
	}
}

func TestConnectRefused(t *testing.T) {
	t.Run("before the handshake", func(t *testing.T) {
		cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
			s.pio.writePacket(append([]byte{0xFF, 0x10, 0x04}, "Too many connections"...))
		})
		_, err := Connect(context.Background(), cfg)
		var e *Error
		if !errors.As(err, &e) || *e != (Error{1040, "HY000", "Too many connections"}) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("bad password", func(t *testing.T) {
		cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
			if _, err := s.accept(mariadbHandshake()); err == nil {
				s.pio.writePacket(errPacket(1045, "28000", "Access denied for user 'u'@'localhost' (using password: YES)"))
			}
		})
		_, err := Connect(context.Background(), cfg)
		var e *Error
		if !errors.As(err, &e) || e.Code != 1045 || e.SQLState != "28000" {
			t.Errorf("got %v", err)
		}
	})
	t.Run("sequence out of step", func(t *testing.T) {
		cfg := startFake(t, "tcp", nil, func(s *fakeSession) {
			s.pio.seq = 1
			s.pio.writePacket(mysqlHandshake().build())
		})
		if _, err := Connect(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "sequence 1, want 0") {
			t.Errorf("got %v", err)
		}
	})
}

func TestConnectDeadlines(t *testing.T) {
	// silent accepts the connection and never says a word.
	silent := func(s *fakeSession) { s.pio.readPacket() }
	t.Run("timeout", func(t *testing.T) {
		cfg := startFake(t, "tcp", nil, silent)
		cfg.Timeout = 200 * time.Millisecond
		start := time.Now()
		_, err := Connect(context.Background(), cfg)
		if !errors.Is(err, os.ErrDeadlineExceeded) || time.Since(start) > 2*time.Second {
			t.Errorf("after %v: %v", time.Since(start), err)
		}
	})
	t.Run("context deadline", func(t *testing.T) {
		cfg := startFake(t, "tcp", nil, silent)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if _, err := Connect(ctx, cfg); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		cfg := startFake(t, "tcp", nil, silent)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(100*time.Millisecond, cancel)
		start := time.Now()
		_, err := Connect(ctx, cfg)
		if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
			t.Errorf("after %v: %v", time.Since(start), err)
		}
	})
	t.Run("cancelled before dialing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Connect(ctx, Config{Port: 9}); !errors.Is(err, context.Canceled) {
			t.Errorf("got %v", err)
		}
	})
}
