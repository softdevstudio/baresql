package pgwire

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"
)

// maxSCRAMIterations bounds the PBKDF2 work a server can ask for. The
// PostgreSQL default is 4096; a server asking for billions would hang the
// login, since the key derivation can't be interrupted.
const maxSCRAMIterations = 10_000_000

// scram is the client side of SCRAM-SHA-256 (RFC 5802, RFC 7677), with the
// tls-server-end-point channel binding of SCRAM-SHA-256-PLUS (RFC 5929).
//
// The password is used as given. PostgreSQL first runs it through SASLprep,
// which leaves ASCII passwords alone and changes only a few non-ASCII ones
// (compatibility characters, unusual spaces); those few can't log in here.
type scram struct {
	user     string // sent as n=; PostgreSQL ignores it and libpq sends ""
	password string
	nonce    string
	gs2      string // "n,,", "y,," or "p=tls-server-end-point,,"
	cbind    []byte // channel binding data, with gs2 "p=..."

	clientFirstBare string
	authMessage     string
	salted          []byte
	maxIter         int // 0 means maxSCRAMIterations; fuzz tests lower it
}

func newSCRAM(password, gs2 string, cbind []byte) (*scram, error) {
	n := make([]byte, 18)
	if _, err := rand.Read(n); err != nil {
		return nil, err
	}
	return &scram{password: password, nonce: base64.StdEncoding.EncodeToString(n), gs2: gs2, cbind: cbind}, nil
}

func (s *scram) clientFirst() []byte {
	s.clientFirstBare = "n=" + saslName(s.user) + ",r=" + s.nonce
	return []byte(s.gs2 + s.clientFirstBare)
}

// clientFinal answers the server-first message.
func (s *scram) clientFinal(serverFirst []byte) ([]byte, error) {
	attrs, err := scramAttrs(string(serverFirst))
	if err != nil {
		return nil, err
	}
	if _, ok := attrs['m']; ok {
		return nil, errors.New("pgwire: SCRAM server asks for an extension the client doesn't know")
	}
	nonce, salt64, iter := attrs['r'], attrs['s'], attrs['i']
	if !strings.HasPrefix(nonce, s.nonce) || len(nonce) == len(s.nonce) {
		return nil, errors.New("pgwire: SCRAM server nonce doesn't extend the client nonce")
	}
	salt, err := base64.StdEncoding.DecodeString(salt64)
	if err != nil || len(salt) == 0 {
		return nil, errors.New("pgwire: SCRAM server sent a bad salt")
	}
	n, err := strconv.Atoi(iter)
	if err != nil || n < 1 {
		return nil, fmt.Errorf("pgwire: SCRAM server sent a bad iteration count %q", iter)
	}
	limit := s.maxIter
	if limit == 0 {
		limit = maxSCRAMIterations
	}
	if n > limit {
		return nil, fmt.Errorf("pgwire: SCRAM server asks for %d iterations; refusing more than %d", n, limit)
	}
	s.salted, err = pbkdf2.Key(sha256.New, s.password, salt, n, sha256.Size)
	if err != nil {
		return nil, err
	}
	cb := base64.StdEncoding.EncodeToString(append([]byte(s.gs2), s.cbind...))
	withoutProof := "c=" + cb + ",r=" + nonce
	s.authMessage = s.clientFirstBare + "," + string(serverFirst) + "," + withoutProof

	clientKey := hmacSHA256(s.salted, "Client Key")
	stored := sha256.Sum256(clientKey)
	sig := hmacSHA256(stored[:], s.authMessage)
	proof := make([]byte, len(clientKey))
	for i := range clientKey {
		proof[i] = clientKey[i] ^ sig[i]
	}
	return []byte(withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof)), nil
}

// verifyServerFinal checks the server's signature, so a server that doesn't
// know the password can't pretend the login worked.
func (s *scram) verifyServerFinal(serverFinal []byte) error {
	attrs, err := scramAttrs(string(serverFinal))
	if err != nil {
		return err
	}
	if e, ok := attrs['e']; ok {
		return fmt.Errorf("pgwire: SCRAM login refused: %s", e)
	}
	got, err := base64.StdEncoding.DecodeString(attrs['v'])
	if err != nil {
		return errors.New("pgwire: SCRAM server sent a bad signature")
	}
	want := hmacSHA256(hmacSHA256(s.salted, "Server Key"), s.authMessage)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return errors.New("pgwire: SCRAM server signature is wrong: the server doesn't know this password")
	}
	return nil
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

// scramAttrs splits "a=x,b=y" into attributes.
func scramAttrs(s string) (map[byte]string, error) {
	out := map[byte]string{}
	for _, part := range strings.Split(s, ",") {
		if len(part) < 2 || part[1] != '=' {
			return nil, fmt.Errorf("pgwire: malformed SCRAM message %q", truncate(s, 80))
		}
		if _, dup := out[part[0]]; !dup {
			out[part[0]] = part[2:]
		}
	}
	return out, nil
}

// saslName escapes "=" and "," as RFC 5802 asks.
func saslName(s string) string {
	return strings.NewReplacer("=", "=3D", ",", "=2C").Replace(s)
}

// endPointHash is the tls-server-end-point channel binding data: the hash
// of the server certificate, with the certificate's own signature hash
// (SHA-256 when that is MD5 or SHA-1). ok is false for certificates without
// such a hash (Ed25519, RSA-PSS), where PostgreSQL can't bind either.
func endPointHash(cert *x509.Certificate) (sum []byte, ok bool) {
	var h hash.Hash
	switch cert.SignatureAlgorithm {
	case x509.MD5WithRSA, x509.SHA1WithRSA, x509.ECDSAWithSHA1, x509.DSAWithSHA1,
		x509.SHA256WithRSA, x509.ECDSAWithSHA256, x509.DSAWithSHA256:
		h = sha256.New()
	case x509.SHA384WithRSA, x509.ECDSAWithSHA384:
		h = sha512.New384()
	case x509.SHA512WithRSA, x509.ECDSAWithSHA512:
		h = sha512.New()
	default:
		return nil, false
	}
	h.Write(cert.Raw)
	return h.Sum(nil), true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
