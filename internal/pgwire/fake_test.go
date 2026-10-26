package pgwire

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fakeServer plays the server side of the protocol, so the client can be
// tested against exact, scripted messages (including broken ones).
type fakeServer struct {
	t  *testing.T
	ln net.Listener
	nc net.Conn
	br *bufio.Reader
}

// startFake listens on loopback and runs script on the first connection.
// It returns a Config pointing at it. Further connections (cancel
// requests) are passed to onExtra when it is set.
func startFake(t *testing.T, script func(s *fakeServer), onExtra func(nc net.Conn)) Config {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	done := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("fake server script did not finish")
		}
	})
	go func() {
		defer close(done)
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer nc.Close()
		if onExtra != nil {
			go func() {
				for {
					extra, err := ln.Accept()
					if err != nil {
						return
					}
					onExtra(extra)
				}
			}()
		}
		nc.SetDeadline(time.Now().Add(10 * time.Second))
		s := &fakeServer{t: t, ln: ln, nc: nc, br: bufio.NewReader(nc)}
		script(s)
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	return Config{Host: "127.0.0.1", Port: port, User: "tester", Password: "secret", Database: "db",
		SSLMode: "disable", ConnectTimeout: 5 * time.Second}
}

// readUntyped reads a startup-phase message: length, then body.
func (s *fakeServer) readUntyped() (code int32, body []byte) {
	var hdr [4]byte
	if _, err := io.ReadFull(s.br, hdr[:]); err != nil {
		s.t.Errorf("fake: reading startup: %v", err)
		return 0, nil
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n < 8 || n > 10000 {
		s.t.Errorf("fake: startup length %d", n)
		return 0, nil
	}
	body = make([]byte, n-4)
	if _, err := io.ReadFull(s.br, body); err != nil {
		s.t.Errorf("fake: reading startup body: %v", err)
		return 0, nil
	}
	return int32(binary.BigEndian.Uint32(body[:4])), body[4:]
}

// startup reads the startup message and returns its parameters.
func (s *fakeServer) startup() map[string]string {
	code, body := s.readUntyped()
	if code != protocol30 {
		s.t.Errorf("fake: startup code %d", code)
		return nil
	}
	params := map[string]string{}
	r := &reader{b: body}
	for len(r.b) > 1 && r.err == nil {
		k := r.cstr()
		params[k] = r.cstr()
	}
	return params
}

func (s *fakeServer) read() (byte, []byte) {
	typ, body, err := readMessage(s.br, make([]byte, 5))
	if err != nil {
		s.t.Errorf("fake: reading message: %v", err)
	}
	return typ, body
}

func (s *fakeServer) expect(want byte) []byte {
	typ, body := s.read()
	if typ != want {
		s.t.Errorf("fake: got message %q, want %q", typ, want)
	}
	return body
}

func (s *fakeServer) send(typ byte, body []byte) {
	m := begin(typ).bytes(body).done()
	if _, err := s.nc.Write(m); err != nil {
		s.t.Errorf("fake: write: %v", err)
	}
}

func (s *fakeServer) raw(b []byte) { s.nc.Write(b) }

func (s *fakeServer) authOK()             { s.send('R', be32(0)) }
func (s *fakeServer) ready(status byte)   { s.send('Z', []byte{status}) }
func (s *fakeServer) complete(tag string) { s.send('C', append([]byte(tag), 0)) }
func (s *fakeServer) param(k, v string)   { s.send('S', []byte(k+"\x00"+v+"\x00")) }
func (s *fakeServer) keyData(pid, key int32) {
	s.send('K', append(be32(uint32(pid)), be32(uint32(key))...))
}

// login finishes a trust login with the usual messages.
func (s *fakeServer) login() {
	s.authOK()
	s.param("server_version", "16.15 (fake)")
	s.keyData(4242, 99)
	s.ready('I')
}

func (s *fakeServer) rowDesc(names ...string) {
	b := binary.BigEndian.AppendUint16(nil, uint16(len(names)))
	for _, n := range names {
		b = append(b, n...)
		b = append(b, 0)
		b = binary.BigEndian.AppendUint32(b, 0)      // table oid
		b = binary.BigEndian.AppendUint16(b, 0)      // attnum
		b = binary.BigEndian.AppendUint32(b, 25)     // text
		b = binary.BigEndian.AppendUint16(b, 0xffff) // size -1
		b = binary.BigEndian.AppendUint32(b, 0xffffffff)
		b = binary.BigEndian.AppendUint16(b, 0)
	}
	s.send('T', b)
}

// dataRow sends a row; a nil entry is NULL.
func (s *fakeServer) dataRow(vals ...*string) {
	b := binary.BigEndian.AppendUint16(nil, uint16(len(vals)))
	for _, v := range vals {
		if v == nil {
			b = binary.BigEndian.AppendUint32(b, 0xffffffff)
			continue
		}
		b = binary.BigEndian.AppendUint32(b, uint32(len(*v)))
		b = append(b, *v...)
	}
	s.send('D', b)
}

func (s *fakeServer) errorResp(severity, code, msg string) {
	s.send('E', []byte("S"+severity+"\x00V"+severity+"\x00C"+code+"\x00M"+msg+"\x00\x00"))
}

func (s *fakeServer) notice(msg string) {
	s.send('N', []byte("SNOTICE\x00VNOTICE\x00C00000\x00M"+msg+"\x00\x00"))
}

func ptr(s string) *string { return &s }

// testCert makes a CA and a server certificate for localhost and 127.0.0.1,
// and writes the CA to a PEM file.
func testCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, path
}

// tlsStartup answers SSLRequest with 'S' and switches to TLS.
func (s *fakeServer) tlsStartup(cert tls.Certificate) bool {
	code, _ := s.readUntyped()
	if code != sslRequestCode {
		s.t.Errorf("fake: expected SSLRequest, got code %d", code)
		return false
	}
	s.raw([]byte{'S'})
	tc := tls.Server(s.nc, &tls.Config{Certificates: []tls.Certificate{cert}})
	if err := tc.Handshake(); err != nil {
		// The client may refuse the certificate; that's a test outcome.
		return false
	}
	s.nc = tc
	s.br = bufio.NewReader(tc)
	return true
}

func itoa(n int) string { return strconv.Itoa(n) }

func newBufReader(nc net.Conn) *bufio.Reader { return bufio.NewReader(nc) }
