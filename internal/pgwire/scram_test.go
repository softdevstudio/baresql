package pgwire

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strings"
	"testing"
)

// The example exchange from RFC 7677, section 3.
func TestSCRAMRFC7677(t *testing.T) {
	s := &scram{user: "user", password: "pencil", nonce: "rOprNGfwEbeRWgbNEkqO", gs2: "n,,"}
	if got := string(s.clientFirst()); got != "n,,n=user,r=rOprNGfwEbeRWgbNEkqO" {
		t.Fatalf("client-first = %q", got)
	}
	serverFirst := "r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"
	final, err := s.clientFinal([]byte(serverFirst))
	if err != nil {
		t.Fatal(err)
	}
	want := "c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ="
	if string(final) != want {
		t.Fatalf("client-final = %q\nwant           %q", final, want)
	}
	if err := s.verifyServerFinal([]byte("v=6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=")); err != nil {
		t.Fatal(err)
	}
	if err := s.verifyServerFinal([]byte("v=AAAATRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=")); err == nil {
		t.Fatal("a wrong server signature was accepted")
	}
	if err := s.verifyServerFinal([]byte("e=invalid-proof")); err == nil || !strings.Contains(err.Error(), "invalid-proof") {
		t.Fatalf("server error not reported: %v", err)
	}
}

func TestSCRAMRejectsBadServerFirst(t *testing.T) {
	for name, msg := range map[string]string{
		"nonce not extended": "r=OTHERNONCE,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096",
		"same nonce":         "r=abc,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096",
		"bad salt":           "r=abcdef,s=***,i=4096",
		"zero iterations":    "r=abcdef,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=0",
		"huge iterations":    "r=abcdef,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=2000000000",
		"malformed":          "garbage",
		"extension":          "m=ext,r=abcdef,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096",
	} {
		s := &scram{password: "pw", nonce: "abc", gs2: "n,,"}
		s.clientFirst()
		if _, err := s.clientFinal([]byte(msg)); err == nil {
			t.Errorf("%s: accepted %q", name, msg)
		}
	}
}

func TestSASLName(t *testing.T) {
	if got := saslName("a=b,c"); got != "a=3Db=2Cc" {
		t.Fatalf("got %q", got)
	}
}

// scramServer is the server side of SCRAM-SHA-256 for the fake server
// tests: it checks the client proof and the channel binding, and signs.
type scramServer struct {
	password  string
	salt      []byte
	iter      int
	nonce     string
	wantCbind string // expected c= value (base64 of gs2 header + binding data)

	clientFirstBare, serverFirst string
}

func (ss *scramServer) first(clientFirst string) (string, bool) {
	// gs2 header is everything up to the second comma.
	i := strings.Index(clientFirst, ",")
	j := strings.Index(clientFirst[i+1:], ",")
	if i < 0 || j < 0 {
		return "", false
	}
	ss.clientFirstBare = clientFirst[i+1+j+1:]
	attrs, err := scramAttrs(ss.clientFirstBare)
	if err != nil {
		return "", false
	}
	ss.serverFirst = "r=" + attrs['r'] + ss.nonce + ",s=" + base64.StdEncoding.EncodeToString(ss.salt) + ",i=" + itoa(ss.iter)
	return ss.serverFirst, true
}

// final checks the client-final message and returns the server-final one.
func (ss *scramServer) final(clientFinal string) (string, bool) {
	k := strings.LastIndex(clientFinal, ",p=")
	if k < 0 {
		return "", false
	}
	withoutProof := clientFinal[:k]
	attrs, err := scramAttrs(withoutProof)
	if err != nil || attrs['c'] != ss.wantCbind {
		return "e=channel-bindings-dont-match", false
	}
	proof, err := base64.StdEncoding.DecodeString(clientFinal[k+3:])
	if err != nil {
		return "", false
	}
	salted, _ := pbkdf2.Key(sha256.New, ss.password, ss.salt, ss.iter, 32)
	auth := ss.clientFirstBare + "," + ss.serverFirst + "," + withoutProof
	clientKey := hmacSHA256(salted, "Client Key")
	stored := sha256.Sum256(clientKey)
	sig := hmacSHA256(stored[:], auth)
	if len(proof) != len(sig) {
		return "e=invalid-proof", false
	}
	recovered := make([]byte, len(sig))
	for i := range sig {
		recovered[i] = proof[i] ^ sig[i]
	}
	h := sha256.Sum256(recovered)
	if subtle.ConstantTimeCompare(h[:], stored[:]) != 1 {
		return "e=invalid-proof", false
	}
	m := hmac.New(sha256.New, hmacSHA256(salted, "Server Key"))
	m.Write([]byte(auth))
	return "v=" + base64.StdEncoding.EncodeToString(m.Sum(nil)), true
}
