package pgwire

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// clearPGEnv keeps the tests independent of the machine's PG* variables.
func clearPGEnv(t *testing.T) {
	for _, k := range []string{"PGHOST", "PGHOSTADDR", "PGPORT", "PGUSER", "PGPASSWORD", "PGDATABASE",
		"PGSSLMODE", "PGSSLROOTCERT", "PGSSLCERT", "PGSSLKEY", "PGCHANNELBINDING",
		"PGCONNECT_TIMEOUT", "PGAPPNAME", "PGOPTIONS"} {
		t.Setenv(k, "")
	}
	t.Setenv("PGPASSFILE", filepath.Join(t.TempDir(), "none"))
}

func TestParseDSNURL(t *testing.T) {
	clearPGEnv(t)
	c, err := ParseDSN("postgres://alice:p%40ss%3Aword@db.example.com:6543/sales?sslmode=verify-full&sslrootcert=/ca.pem&connect_timeout=7&application_name=x")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "db.example.com" || c.Port != 6543 || c.User != "alice" || c.Password != "p@ss:word" ||
		c.Database != "sales" || c.SSLMode != "verify-full" || c.SSLRootCert != "/ca.pem" ||
		c.ConnectTimeout != 7*time.Second || c.Params["application_name"] != "x" {
		t.Fatalf("%+v", c)
	}
	if strings.Contains(c.Redacted(), "word") {
		t.Fatalf("Redacted shows the password: %s", c.Redacted())
	}
	if got := c.Redacted(); got != "postgres://alice@db.example.com:6543/sales" {
		t.Fatalf("Redacted = %s", got)
	}
}

func TestParseDSNDefaults(t *testing.T) {
	clearPGEnv(t)
	c, err := ParseDSN("postgresql://bob@localhost")
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 5432 || c.Database != "bob" || c.SSLMode != "prefer" || c.ChannelBinding != "prefer" ||
		c.ConnectTimeout != 10*time.Second {
		t.Fatalf("%+v", c)
	}
}

func TestParseDSNIPv6AndSocketParam(t *testing.T) {
	clearPGEnv(t)
	c, err := ParseDSN("postgres://u@[::1]:5433/d")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "::1" || c.Port != 5433 {
		t.Fatalf("%+v", c)
	}
	c, err = ParseDSN("postgres://u@/d?host=/var/run/postgresql")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "/var/run/postgresql" || !c.IsUnixSocket() {
		t.Fatalf("%+v", c)
	}
	if got := c.Redacted(); got != "postgres://u@/var/run/postgresql/d" {
		t.Fatalf("Redacted = %s", got)
	}
}

func TestParseDSNKeywords(t *testing.T) {
	clearPGEnv(t)
	c, err := ParseDSN(`host=db port = 5440 user=carol password='it''s \'quoted\' and spaced' dbname=x sslmode=require`)
	if err == nil {
		// libpq doesn't double quotes; '' ends one value and starts none.
		t.Fatalf("expected an error for '' inside a quoted value, got %+v", c)
	}
	c, err = ParseDSN(`host=db port = 5440 user=carol password='it\'s spaced' dbname=x sslmode=require`)
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "db" || c.Port != 5440 || c.User != "carol" || c.Password != "it's spaced" || c.Database != "x" || c.SSLMode != "require" {
		t.Fatalf("%+v", c)
	}
}

func TestParseDSNEnvironment(t *testing.T) {
	clearPGEnv(t)
	t.Setenv("PGHOST", "envhost")
	t.Setenv("PGPORT", "7777")
	t.Setenv("PGUSER", "envuser")
	t.Setenv("PGPASSWORD", "envpass")
	t.Setenv("PGDATABASE", "envdb")
	t.Setenv("PGSSLMODE", "disable")
	c, err := ParseDSN("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "envhost" || c.Port != 7777 || c.User != "envuser" || c.Password != "envpass" ||
		c.Database != "envdb" || c.SSLMode != "disable" {
		t.Fatalf("%+v", c)
	}
	// The connection string wins over the environment.
	c, err = ParseDSN("postgres://other@h/d")
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "other" || c.Host != "h" || c.Database != "d" || c.Port != 7777 {
		t.Fatalf("%+v", c)
	}
}

func TestParseDSNErrors(t *testing.T) {
	clearPGEnv(t)
	for _, dsn := range []string{
		"postgres://u@h/d?sslmod=require",       // typo in a parameter name
		"postgres://u@h:99999/d",                // port out of range
		"postgres://u@h/d?sslmode=allow",        // unsupported mode
		"postgres://u@h/d?sslmode=bogus",        // unknown mode
		"postgres://u@h/d?channel_binding=bad",  // unknown channel binding
		"postgres://u@h/d?connect_timeout=soon", // not a number
		"host=h user",                           // no '='
		"host='h",                               // unterminated quote
		"postgres://u@h/d?sslrootcert=system&sslmode=require",
	} {
		if _, err := ParseDSN(dsn); err == nil {
			t.Errorf("%q: no error", dsn)
		}
	}
}

func TestParseDSNErrorHidesPassword(t *testing.T) {
	clearPGEnv(t)
	_, err := ParseDSN("postgres://u:sekrit@h:bad/d")
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "sekrit") {
		t.Fatalf("error shows the password: %v", err)
	}
}

func TestParseDSNSystemRootImpliesVerifyFull(t *testing.T) {
	clearPGEnv(t)
	c, err := ParseDSN("postgres://u@h/d?sslrootcert=system")
	if err != nil {
		t.Fatal(err)
	}
	if c.SSLMode != "verify-full" {
		t.Fatalf("sslmode = %s", c.SSLMode)
	}
}

func TestParseDSNHostaddr(t *testing.T) {
	clearPGEnv(t)
	c, err := ParseDSN("host=db.example.com hostaddr=10.0.0.5 user=u")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "10.0.0.5" || c.ServerName != "db.example.com" {
		t.Fatalf("%+v", c)
	}
}

func TestPgpass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permissions differ on Windows")
	}
	clearPGEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "pgpass")
	content := "# comment\n" +
		"other:*:*:*:wrong\n" +
		`db\:x:5432:sales:alice:p\:w\\d` + "\n" +
		"*:*:*:bob:bobpass\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGPASSFILE", path)
	c, err := ParseDSN("host=db:x user=alice dbname=sales")
	if err != nil {
		t.Fatal(err)
	}
	if c.Password != `p:w\d` {
		t.Fatalf("password %q", c.Password)
	}
	c, _ = ParseDSN("host=anything user=bob")
	if c.Password != "bobpass" {
		t.Fatalf("wildcard: password %q", c.Password)
	}
	// A given password wins.
	c, _ = ParseDSN("host=anything user=bob password=given")
	if c.Password != "given" {
		t.Fatalf("password %q", c.Password)
	}
	// A file others can read is ignored, as libpq does.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	c, _ = ParseDSN("host=anything user=bob")
	if c.Password != "" {
		t.Fatalf("world-readable pgpass used: %q", c.Password)
	}
}
