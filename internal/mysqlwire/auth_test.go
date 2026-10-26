package mysqlwire

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"
)

// serverScramble is 20 bytes like the ones servers send.
const serverScramble = "s:d8&Tb3)aR!@5vX0q-Z"

func TestScrambleNative(t *testing.T) {
	const password = "secret"
	stage1 := sha1.Sum([]byte(password))
	stage2 := sha1.Sum(stage1[:])
	// MariaDB's PASSWORD('secret') is '*' and SHA1(SHA1('secret')) in hex,
	// a known answer for the hash the server stores.
	if got := fmt.Sprintf("%X", stage2); got != "14E65567ABDB5135D0CFD9A70B3032C179A49EE7" {
		t.Fatalf("SHA1(SHA1(secret)) = %s", got)
	}
	h := sha1.New()
	h.Write([]byte(serverScramble))
	h.Write(stage2[:])
	mix := h.Sum(nil)
	want := make([]byte, sha1.Size)
	for i := range want {
		want[i] = stage1[i] ^ mix[i]
	}
	if got := scrambleNative([]byte(serverScramble), password); !bytes.Equal(got, want) {
		t.Errorf("scrambleNative = % x\nwant % x", got, want)
	}
	if got := scrambleNative([]byte(serverScramble), ""); len(got) != 0 {
		t.Errorf("empty password gives % x, want an empty reply", got)
	}
}

func TestScrambleCachingSHA2(t *testing.T) {
	const password = "secret"
	stage1 := sha256.Sum256([]byte(password))
	stage2 := sha256.Sum256(stage1[:])
	h := sha256.New()
	h.Write(stage2[:])
	h.Write([]byte(serverScramble))
	mix := h.Sum(nil)
	want := make([]byte, sha256.Size)
	for i := range want {
		want[i] = stage1[i] ^ mix[i]
	}
	if got := scrambleCachingSHA2([]byte(serverScramble), password); !bytes.Equal(got, want) {
		t.Errorf("scrambleCachingSHA2 = % x\nwant % x", got, want)
	}
	if got := scrambleCachingSHA2([]byte(serverScramble), ""); len(got) != 0 {
		t.Errorf("empty password gives % x, want an empty reply", got)
	}
}

// rsaPEM returns a fresh RSA key and its public half as the PEM the
// server sends.
func rsaPEM(t testing.TB) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// decryptPassword does what the server does with encryptPassword's output.
func decryptPassword(t *testing.T, key *rsa.PrivateKey, scramble, enc []byte) string {
	t.Helper()
	plain, err := rsa.DecryptOAEP(sha1.New(), nil, key, enc, nil)
	if err != nil {
		t.Fatalf("server cannot decrypt: %v", err)
	}
	for i := range plain {
		plain[i] ^= scramble[i%len(scramble)]
	}
	return string(plain)
}

func TestEncryptPassword(t *testing.T) {
	key, pemKey := rsaPEM(t)
	// Longer than the scramble, so the XOR has to wrap around.
	const password = "pässwörd that is longer than twenty bytes"
	enc, err := encryptPassword(password, []byte(serverScramble), pemKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := decryptPassword(t, key, []byte(serverScramble), enc); got != password+"\x00" {
		t.Errorf("server reads %q", got)
	}

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, _ := x509.MarshalPKIXPublicKey(&ecKey.PublicKey)
	bad := []struct {
		name     string
		scramble []byte
		key      []byte
		want     string
	}{
		{"empty scramble", nil, pemKey, "empty scramble"},
		{"no PEM", []byte(serverScramble), []byte("not a key"), "no PEM"},
		{"garbage DER", []byte(serverScramble), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte{1, 2, 3}}), "public key"},
		{"not RSA", []byte(serverScramble), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: ecDER}), "not RSA"},
	}
	for _, tc := range bad {
		if _, err := encryptPassword("pw", tc.scramble, tc.key); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestParseAuthSwitch(t *testing.T) {
	pkt := append([]byte{0xFE}, "mysql_native_password\x00"...)
	pkt = append(append(pkt, serverScramble...), 0)
	plugin, scramble, err := parseAuthSwitch(pkt)
	if err != nil || plugin != pluginNative || !bytes.Equal(scramble, []byte(serverScramble)) {
		t.Errorf("got %q, % x, %v", plugin, scramble, err)
	}
	// Without the trailing NUL the scramble is the same.
	plugin, scramble, err = parseAuthSwitch(pkt[:len(pkt)-1])
	if err != nil || plugin != pluginNative || !bytes.Equal(scramble, []byte(serverScramble)) {
		t.Errorf("no trailing NUL: got %q, % x, %v", plugin, scramble, err)
	}
	if _, _, err := parseAuthSwitch([]byte{0xFE}); err == nil || !strings.Contains(err.Error(), "mysql_old_password") {
		t.Errorf("old auth switch: %v", err)
	}
	if _, _, err := parseAuthSwitch(append([]byte{0xFE}, "no terminator"...)); err == nil {
		t.Error("plugin name without NUL accepted")
	}
}
