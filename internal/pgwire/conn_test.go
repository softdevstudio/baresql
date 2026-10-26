package pgwire

import (
	"context"
	"crypto/md5"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestFakeTrustLoginAndResults(t *testing.T) {
	cfg := startFake(t, func(s *fakeServer) {
		p := s.startup()
		if p["user"] != "tester" || p["database"] != "db" || p["client_encoding"] != "UTF8" || p["application_name"] != "baresql" {
			t.Errorf("startup params %v", p)
		}
		s.login()
		if q := s.expect('Q'); string(q) != "SELECT a, b FROM t; SELECT 1 WHERE false\x00" {
			t.Errorf("query %q", q)
		}
		s.rowDesc("a", "b")
		s.dataRow(ptr("1"), nil)
		s.notice("watch out")
		s.dataRow(ptr("é"), ptr(""))
		s.complete("SELECT 2")
		s.rowDesc("?column?")
		s.complete("SELECT 0")
		s.ready('I')
		s.expect('X')
	}, nil)
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerVersion() != "16.15 (fake)" || c.ProcessID() != 4242 || c.AuthMethod() != "trust" || c.TLSActive() {
		t.Fatalf("version %q pid %d auth %q tls %v", c.ServerVersion(), c.ProcessID(), c.AuthMethod(), c.TLSActive())
	}
	res, err := c.Query(context.Background(), "SELECT a, b FROM t; SELECT 1 WHERE false")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("%d results", len(res))
	}
	r := res[0]
	if r.Tag != "SELECT 2" || r.RowsAffected() != 2 || len(r.Rows) != 2 || r.Columns[1].Name != "b" || r.Columns[0].TypeOID != 25 || r.Columns[0].TypeSize != -1 {
		t.Fatalf("%+v", r)
	}
	if r.Rows[0][0].String() != "1" || !r.Rows[0][1].Null || r.Rows[1][0].String() != "é" || r.Rows[1][1].Null {
		t.Fatalf("rows %+v", r.Rows)
	}
	if len(r.Notices) != 1 || r.Notices[0].Message != "watch out" || r.Notices[0].Severity != "NOTICE" {
		t.Fatalf("notices %+v", r.Notices)
	}
	if res[1].Tag != "SELECT 0" || len(res[1].Rows) != 0 {
		t.Fatalf("%+v", res[1])
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFakeErrorStopsTheRestButConnLives(t *testing.T) {
	cfg := startFake(t, func(s *fakeServer) {
		s.startup()
		s.login()
		s.expect('Q')
		s.complete("CREATE TABLE")
		s.errorResp("ERROR", "42601", `syntax error at or near "SELEC"`)
		s.ready('I')
		s.expect('Q')
		s.rowDesc("x")
		s.dataRow(ptr("1"))
		s.complete("SELECT 1")
		s.ready('I')
	}, nil)
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	res, err := c.Query(context.Background(), "CREATE TABLE t(); SELEC 1")
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != "42601" || !IsCode(err, "42601") {
		t.Fatalf("err = %v", err)
	}
	if len(res) != 1 || res[0].Tag != "CREATE TABLE" {
		t.Fatalf("results before the error: %+v", res)
	}
	if got := err.Error(); got != `ERROR: syntax error at or near "SELEC" (SQLSTATE 42601)` {
		t.Fatalf("Error() = %q", got)
	}
	res, err = c.Query(context.Background(), "SELECT 1")
	if err != nil || len(res) != 1 || res[0].Rows[0][0].String() != "1" {
		t.Fatalf("after error: %v %+v", err, res)
	}
}

func TestFakeCopyIsRefusedAndDrained(t *testing.T) {
	cfg := startFake(t, func(s *fakeServer) {
		s.startup()
		s.login()
		s.expect('Q')
		s.send('G', []byte{0, 0, 0}) // CopyInResponse: text, 0 columns
		if body := s.expect('f'); !strings.Contains(string(body), "does not send COPY data") {
			t.Errorf("CopyFail body %q", body)
		}
		s.errorResp("ERROR", "57014", "COPY from stdin failed")
		s.ready('I')
		s.expect('Q')
		s.send('H', []byte{0, 0, 0})
		s.send('d', []byte("1\tone\n"))
		s.send('d', []byte("2\ttwo\n"))
		s.send('c', nil)
		s.complete("COPY 2")
		s.ready('I')
	}, nil)
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Query(context.Background(), "COPY t FROM STDIN"); !IsCode(err, "57014") {
		t.Fatalf("COPY IN: %v", err)
	}
	res, err := c.Query(context.Background(), "COPY t TO STDOUT")
	if err != nil || len(res) != 1 || res[0].Tag != "COPY 2" {
		t.Fatalf("COPY OUT: %v %+v", err, res)
	}
}

func TestFakeMD5AndCleartext(t *testing.T) {
	salt := []byte{1, 2, 3, 4}
	cfg := startFake(t, func(s *fakeServer) {
		s.startup()
		s.send('R', append(be32(5), salt...))
		got := strings.TrimSuffix(string(s.expect('p')), "\x00")
		inner := md5.Sum([]byte("secret" + "tester"))
		outer := md5.Sum(append([]byte(hex.EncodeToString(inner[:])), salt...))
		if want := "md5" + hex.EncodeToString(outer[:]); got != want {
			t.Errorf("md5 response %q, want %q", got, want)
		}
		s.login()
	}, nil)
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMethod() != "md5" {
		t.Fatalf("auth %q", c.AuthMethod())
	}
	c.Close()

	cfg = startFake(t, func(s *fakeServer) {
		s.startup()
		s.send('R', be32(3))
		if got := string(s.expect('p')); got != "secret\x00" {
			t.Errorf("cleartext response %q", got)
		}
		s.login()
	}, nil)
	c, err = Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMethod() != "password" {
		t.Fatalf("auth %q", c.AuthMethod())
	}
	c.Close()
}

func TestFakeLoginRefused(t *testing.T) {
	cfg := startFake(t, func(s *fakeServer) {
		s.startup()
		s.send('R', be32(3))
		s.expect('p')
		s.errorResp("FATAL", "28P01", `password authentication failed for user "tester"`)
	}, nil)
	_, err := Connect(context.Background(), cfg)
	if !IsCode(err, "28P01") {
		t.Fatalf("err = %v", err)
	}
}

// saslLogin runs the server side of a SCRAM login.
func (s *fakeServer) saslLogin(mechs []string, ss *scramServer, wantMech string, tamper bool) {
	b := be32(10)
	for _, m := range mechs {
		b = append(append(b, m...), 0)
	}
	s.send('R', append(b, 0))
	r := &reader{b: s.expect('p')}
	mech := r.cstr()
	n := r.int32()
	first := string(r.next(int(n)))
	if mech != wantMech {
		s.t.Errorf("client chose %q, want %q", mech, wantMech)
	}
	sf, ok := ss.first(first)
	if !ok {
		s.t.Errorf("bad client-first %q", first)
		return
	}
	s.send('R', append(be32(11), sf...))
	final, ok := ss.final(string(s.expect('p')))
	if !ok {
		s.errorResp("FATAL", "28P01", "SCRAM failed: "+final)
		return
	}
	if tamper {
		final = "v=" + base64.StdEncoding.EncodeToString(make([]byte, 32))
	}
	s.send('R', append(be32(12), final...))
	if !tamper {
		s.login()
	}
}

func TestFakeSCRAM(t *testing.T) {
	ss := &scramServer{password: "secret", salt: []byte("0123456789abcdef"), iter: 4096, nonce: "SERVERPART",
		wantCbind: base64.StdEncoding.EncodeToString([]byte("n,,"))}
	cfg := startFake(t, func(s *fakeServer) {
		s.startup()
		s.saslLogin([]string{"SCRAM-SHA-256"}, ss, "SCRAM-SHA-256", false)
	}, nil)
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMethod() != "scram-sha-256" {
		t.Fatalf("auth %q", c.AuthMethod())
	}
	c.Close()

	// Wrong password: the server refuses.
	cfg = startFake(t, func(s *fakeServer) {
		s.startup()
		s.saslLogin([]string{"SCRAM-SHA-256"}, ss, "SCRAM-SHA-256", false)
	}, nil)
	cfg.Password = "wrong"
	if _, err := Connect(context.Background(), cfg); !IsCode(err, "28P01") {
		t.Fatalf("wrong password: %v", err)
	}

	// A server that doesn't know the password can't fake the last step.
	cfg = startFake(t, func(s *fakeServer) {
		s.startup()
		s.saslLogin([]string{"SCRAM-SHA-256"}, ss, "SCRAM-SHA-256", true)
	}, nil)
	if _, err := Connect(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "signature is wrong") {
		t.Fatalf("forged server signature: %v", err)
	}
}

func TestFakeTLSModes(t *testing.T) {
	cert, caFile := testCert(t)
	tlsLogin := func(s *fakeServer) {
		if s.tlsStartup(cert) {
			s.startup()
			s.login()
		}
	}
	for _, tc := range []struct {
		name, mode, root, host, server string
		ok                             bool
	}{
		{"require skips verification", "require", "", "127.0.0.1", "", true},
		{"verify-full by IP", "verify-full", caFile, "127.0.0.1", "", true},
		{"verify-full by name", "verify-full", caFile, "127.0.0.1", "localhost", true},
		{"verify-full wrong name", "verify-full", caFile, "127.0.0.1", "db.example.com", false},
		{"verify-ca ignores the name", "verify-ca", caFile, "127.0.0.1", "db.example.com", true},
		{"require with a CA file verifies", "require", writeOtherCA(t), "127.0.0.1", "", false},
		{"verify-full with another CA", "verify-full", writeOtherCA(t), "127.0.0.1", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := startFake(t, tlsLogin, nil)
			cfg.SSLMode, cfg.SSLRootCert, cfg.ServerName = tc.mode, tc.root, tc.server
			c, err := Connect(context.Background(), cfg)
			if tc.ok != (err == nil) {
				t.Fatalf("ok=%v, err=%v", tc.ok, err)
			}
			if err == nil {
				if !c.TLSActive() {
					t.Fatal("TLS not active")
				}
				c.Close()
			}
		})
	}
}

func writeOtherCA(t *testing.T) string {
	_, path := testCert(t)
	return path
}

func TestFakeServerWithoutTLS(t *testing.T) {
	refuse := func(s *fakeServer) {
		if code, _ := s.readUntyped(); code != sslRequestCode {
			t.Errorf("expected SSLRequest, got %d", code)
		}
		s.raw([]byte{'N'})
		if p := s.startup(); p == nil {
			return
		}
		s.login()
	}
	cfg := startFake(t, refuse, nil)
	cfg.SSLMode = "prefer"
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("prefer: %v", err)
	}
	if c.TLSActive() {
		t.Fatal("TLS active")
	}
	c.Close()

	cfg = startFake(t, func(s *fakeServer) {
		s.readUntyped()
		s.raw([]byte{'N'})
	}, nil)
	cfg.SSLMode = "require"
	if _, err := Connect(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "does not offer TLS") {
		t.Fatalf("require: %v", err)
	}
}

func TestFakeChannelBinding(t *testing.T) {
	cert, caFile := testCert(t)
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	sum, ok := endPointHash(leaf)
	if !ok {
		t.Fatal("no end-point hash for an ECDSA-SHA256 certificate")
	}
	ss := &scramServer{password: "secret", salt: []byte("salt-salt-salt-1"), iter: 4096, nonce: "SRV",
		wantCbind: base64.StdEncoding.EncodeToString(append([]byte("p=tls-server-end-point,,"), sum...))}
	cfg := startFake(t, func(s *fakeServer) {
		if s.tlsStartup(cert) {
			s.startup()
			s.saslLogin([]string{"SCRAM-SHA-256-PLUS", "SCRAM-SHA-256"}, ss, "SCRAM-SHA-256-PLUS", false)
		}
	}, nil)
	cfg.SSLMode, cfg.SSLRootCert = "verify-full", caFile
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMethod() != "scram-sha-256-plus" {
		t.Fatalf("auth %q", c.AuthMethod())
	}
	c.Close()

	// TLS, but the server offers no PLUS: the client says "y" (it could bind).
	ssY := &scramServer{password: "secret", salt: []byte("salt-salt-salt-2"), iter: 4096, nonce: "SRV",
		wantCbind: base64.StdEncoding.EncodeToString([]byte("y,,"))}
	cfg = startFake(t, func(s *fakeServer) {
		if s.tlsStartup(cert) {
			s.startup()
			s.saslLogin([]string{"SCRAM-SHA-256"}, ssY, "SCRAM-SHA-256", false)
		}
	}, nil)
	cfg.SSLMode = "require"
	c, err = Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()

	// channel_binding=require without PLUS on offer fails before any proof.
	cfg = startFake(t, func(s *fakeServer) {
		if s.tlsStartup(cert) {
			s.startup()
			s.send('R', append(be32(10), "SCRAM-SHA-256\x00\x00"...))
		}
	}, nil)
	cfg.SSLMode, cfg.ChannelBinding = "require", "require"
	if _, err := Connect(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "channel_binding=require") {
		t.Fatalf("require binding: %v", err)
	}
}

func TestFakeMalformedMessages(t *testing.T) {
	desc := func(s *fakeServer) { s.rowDesc("a") }
	for name, junk := range map[string]func(s *fakeServer){
		"length below 4":     func(s *fakeServer) { s.raw([]byte{'Z', 0, 0, 0, 1}) },
		"huge length":        func(s *fakeServer) { s.raw([]byte{'D', 0x7f, 0xff, 0xff, 0xff}) },
		"row before desc":    func(s *fakeServer) { s.send('D', []byte{0, 0}) },
		"unknown message":    func(s *fakeServer) { s.send('?', nil) },
		"truncated then EOF": func(s *fakeServer) { s.raw([]byte{'T', 0, 0, 0, 50, 0, 1}) },
		"field count wrong":  func(s *fakeServer) { desc(s); s.send('D', []byte{0, 2, 0, 0, 0, 0, 0, 0, 0, 0}) },
		"field past the end": func(s *fakeServer) { desc(s); s.send('D', []byte{0, 1, 0, 0, 0, 9, 'x'}) },
		"negative field len": func(s *fakeServer) { desc(s); s.send('D', []byte{0, 1, 0xff, 0xff, 0xff, 0xfb}) },
		"desc too short":     func(s *fakeServer) { s.send('T', []byte{0, 3, 'a', 0}) },
		"negative columns":   func(s *fakeServer) { s.send('T', []byte{0xff, 0xff}) },
		"tag without NUL":    func(s *fakeServer) { s.send('C', []byte("SELECT 1")); s.ready('I') },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := startFake(t, func(s *fakeServer) {
				s.startup()
				s.login()
				s.expect('Q')
				junk(s)
				s.nc.(*net.TCPConn).CloseWrite()
			}, nil)
			c, err := Connect(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.Query(context.Background(), "SELECT 1"); err == nil {
				if name == "tag without NUL" {
					return // a tag without its NUL reads as empty; harmless
				}
				t.Fatal("no error")
			}
			// The connection is now unusable and says so.
			if _, err := c.Query(context.Background(), "SELECT 1"); err == nil || !strings.Contains(err.Error(), "unusable") {
				t.Fatalf("second query: %v", err)
			}
		})
	}
}

func TestFakeCancelKeepsConnection(t *testing.T) {
	cancelled := make(chan struct{})
	cfg := startFake(t, func(s *fakeServer) {
		s.startup()
		s.login()
		s.expect('Q')
		select {
		case <-cancelled:
		case <-time.After(5 * time.Second):
			t.Error("no cancel request arrived")
		}
		s.errorResp("ERROR", "57014", "canceling statement due to user request")
		s.ready('I')
		s.expect('Q')
		s.rowDesc("ok")
		s.complete("SELECT 0")
		s.ready('I')
	}, func(nc net.Conn) {
		defer nc.Close()
		s := &fakeServer{t: t, nc: nc, br: newBufReader(nc)}
		code, body := s.readUntyped()
		r := &reader{b: body}
		if code != cancelCode || r.int32() != 4242 || r.int32() != 99 {
			t.Errorf("cancel request code %d", code)
		}
		close(cancelled)
	})
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = c.Query(ctx, "SELECT pg_sleep(60)")
	if !errors.Is(err, context.DeadlineExceeded) || !IsCode(err, "57014") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("took %v", d)
	}
	if _, err := c.Query(context.Background(), "SELECT 0"); err != nil {
		t.Fatalf("after cancel: %v", err)
	}
}

func TestFakeConnectTimeout(t *testing.T) {
	cfg := startFake(t, func(s *fakeServer) {
		s.startup()
		time.Sleep(1500 * time.Millisecond) // never answers in time
	}, nil)
	cfg.ConnectTimeout = 300 * time.Millisecond
	start := time.Now()
	_, err := Connect(context.Background(), cfg)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestFakeQueryArgs(t *testing.T) {
	cfg := startFake(t, func(s *fakeServer) {
		s.startup()
		s.login()
		parse := &reader{b: s.expect('P')}
		parse.cstr()
		if q := parse.cstr(); q != "SELECT $1::int, $2::text, $3" {
			t.Errorf("parse query %q", q)
		}
		bind := &reader{b: s.expect('B')}
		bind.cstr()
		bind.cstr()
		bind.int16()
		if n := bind.int16(); n != 3 {
			t.Errorf("bind params %d", n)
		}
		if l := bind.int32(); l != 2 || string(bind.next(2)) != "42" {
			t.Errorf("param 1")
		}
		if l := bind.int32(); l != 3 || string(bind.next(3)) != "a'b" {
			t.Errorf("param 2")
		}
		if l := bind.int32(); l != -1 {
			t.Errorf("param 3 not NULL: %d", l)
		}
		s.expect('D')
		s.expect('E')
		s.expect('S')
		s.send('1', nil)
		s.send('2', nil)
		s.rowDesc("a", "b", "c")
		s.dataRow(ptr("42"), ptr("a'b"), nil)
		s.complete("SELECT 1")
		s.ready('I')
	}, nil)
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := c.QueryArgs(context.Background(), "SELECT $1::int, $2::text, $3", 42, "a'b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || r.Rows[0][1].String() != "a'b" || !r.Rows[0][2].Null {
		t.Fatalf("%+v", r)
	}
	if _, err := c.QueryArgs(context.Background(), "SELECT $1", struct{}{}); err == nil {
		t.Fatal("unsupported argument type accepted")
	}
}

func TestEndPointHashAlgorithms(t *testing.T) {
	for alg, want := range map[x509.SignatureAlgorithm]int{
		x509.SHA1WithRSA: 32, x509.SHA256WithRSA: 32, x509.ECDSAWithSHA384: 48,
		x509.SHA512WithRSA: 64, x509.PureEd25519: 0,
	} {
		sum, ok := endPointHash(&x509.Certificate{SignatureAlgorithm: alg, Raw: []byte("cert")})
		if len(sum) != want || ok != (want > 0) {
			t.Errorf("%v: %d bytes, ok=%v", alg, len(sum), ok)
		}
	}
}
