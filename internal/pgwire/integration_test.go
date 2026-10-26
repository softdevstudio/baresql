package pgwire

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/softdevstudio/baresql/internal/testenv"
)

// Tests against a real PostgreSQL server (BARESQL_TEST_PG, a superuser).

func pgConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := ParseDSN(testenv.DSN(t, testenv.PG))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func pgConnect(t *testing.T, cfg Config) *Conn {
	t.Helper()
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect %s: %v", cfg.Redacted(), err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func mustQuery(t *testing.T, c *Conn, sql string) []*Result {
	t.Helper()
	res, err := c.Query(context.Background(), sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return res
}

func randName(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// tempRole creates a login role with a password stored as md5 or SCRAM,
// and drops it when the test ends.
func tempRole(t *testing.T, admin *Conn, encryption, password string) string {
	t.Helper()
	name := randName("baresql_t_")
	mustQuery(t, admin, "SET password_encryption = '"+encryption+"'; CREATE ROLE "+name+" LOGIN PASSWORD '"+strings.ReplaceAll(password, "'", "''")+"'; RESET password_encryption")
	t.Cleanup(func() { admin.Exec(context.Background(), "DROP ROLE IF EXISTS "+name) })
	return name
}

func TestPGTextValues(t *testing.T) {
	c := pgConnect(t, pgConfig(t))
	r := mustQuery(t, c, `SELECT 1::int AS i, 3.5000::numeric AS n, true AS b, NULL::text AS z, 'é'::text AS e,
		'{1,2}'::int[] AS a, DATE '2024-01-02' AS d, ''::text AS empty`)[0]
	want := []string{"1", "3.5000", "t", "", "é", "{1,2}", "2024-01-02", ""}
	for i, w := range want {
		if got := r.Rows[0][i].String(); got != w {
			t.Errorf("column %s = %q, want %q", r.Columns[i].Name, got, w)
		}
	}
	if !r.Rows[0][3].Null || r.Rows[0][7].Null {
		t.Error("NULL and empty text mixed up")
	}
	if r.Columns[0].TypeOID != 23 || r.Columns[1].TypeOID != 1700 || r.Columns[4].TypeOID != 25 {
		t.Errorf("type oids %d %d %d", r.Columns[0].TypeOID, r.Columns[1].TypeOID, r.Columns[4].TypeOID)
	}
	if c.ServerVersion() == "" || c.Param("client_encoding") != "UTF8" {
		t.Errorf("server_version %q client_encoding %q", c.ServerVersion(), c.Param("client_encoding"))
	}
}

func TestPGMultiStatementError(t *testing.T) {
	c := pgConnect(t, pgConfig(t))
	res, err := c.Query(context.Background(),
		"CREATE TEMP TABLE t(a int); INSERT INTO t VALUES (1),(2); SELECT a FROM t ORDER BY a; SELECT 1/0; SELECT 3")
	if !IsCode(err, "22012") {
		t.Fatalf("err = %v", err)
	}
	if len(res) != 3 || res[0].Tag != "CREATE TABLE" || res[1].Tag != "INSERT 0 2" || res[1].RowsAffected() != 2 ||
		len(res[2].Rows) != 2 || res[2].Rows[1][0].String() != "2" {
		t.Fatalf("results %+v", res)
	}
	if r := mustQuery(t, c, "SELECT 42"); r[0].Rows[0][0].String() != "42" {
		t.Fatal("connection not usable after the error")
	}
}

func TestPGTransactionStatus(t *testing.T) {
	c := pgConnect(t, pgConfig(t))
	if c.TxStatus() != 'I' {
		t.Fatalf("status %q", c.TxStatus())
	}
	mustQuery(t, c, "BEGIN")
	if c.TxStatus() != 'T' {
		t.Fatalf("status %q", c.TxStatus())
	}
	c.Exec(context.Background(), "SELECT * FROM no_such_table_here")
	if c.TxStatus() != 'E' {
		t.Fatalf("status %q", c.TxStatus())
	}
	mustQuery(t, c, "ROLLBACK")
	if c.TxStatus() != 'I' {
		t.Fatalf("status %q", c.TxStatus())
	}
}

func TestPGNotices(t *testing.T) {
	c := pgConnect(t, pgConfig(t))
	res := mustQuery(t, c, "SET client_min_messages = notice; DO $$ BEGIN RAISE NOTICE 'hello %', 42; RAISE WARNING 'careful'; END $$")
	n := res[1].Notices
	if len(n) != 2 || n[0].Message != "hello 42" || n[0].Severity != "NOTICE" || n[1].Severity != "WARNING" {
		t.Fatalf("notices %+v", n)
	}
}

func TestPGQueryArgs(t *testing.T) {
	c := pgConnect(t, pgConfig(t))
	r, err := c.QueryArgs(context.Background(), "SELECT $1::int + 1, $2::text, $3::text IS NULL, $4::oid::regclass::text, $5::bool",
		41, "it's", nil, uint32(1259), true)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{r.Rows[0][0].String(), r.Rows[0][1].String(), r.Rows[0][2].String(), r.Rows[0][3].String(), r.Rows[0][4].String()}
	if strings.Join(got, "|") != "42|it's|t|pg_class|t" {
		t.Fatalf("got %v", got)
	}
	// An error in the extended protocol leaves the connection usable.
	if _, err := c.QueryArgs(context.Background(), "SELECT $1::int", "not a number"); !IsCode(err, "22P02") {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.QueryArgs(context.Background(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
}

func TestPGCopy(t *testing.T) {
	c := pgConnect(t, pgConfig(t))
	res := mustQuery(t, c, "COPY (SELECT g FROM generate_series(1, 1000) g) TO STDOUT")
	if res[0].Tag != "COPY 1000" {
		t.Fatalf("tag %q", res[0].Tag)
	}
	mustQuery(t, c, "CREATE TEMP TABLE c(a int)")
	_, err := c.Query(context.Background(), "COPY c FROM STDIN")
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("COPY FROM STDIN: %v", err)
	}
	if r := mustQuery(t, c, "SELECT count(*) FROM c"); r[0].Rows[0][0].String() != "0" {
		t.Fatal("rows were copied")
	}
}

func TestPGCancel(t *testing.T) {
	c := pgConnect(t, pgConfig(t))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Query(ctx, "SELECT pg_sleep(30)")
	if !errors.Is(err, context.DeadlineExceeded) || !IsCode(err, "57014") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("cancel took %v", d)
	}
	if r := mustQuery(t, c, "SELECT 'still here'"); r[0].Rows[0][0].String() != "still here" {
		t.Fatal("connection lost after cancel")
	}
}

func TestPGLargeValue(t *testing.T) {
	c := pgConnect(t, pgConfig(t))
	r := mustQuery(t, c, "SELECT repeat('x', 3*1024*1024), 'after'")[0]
	if len(r.Rows[0][0].Raw) != 3*1024*1024 || r.Rows[0][1].String() != "after" {
		t.Fatalf("got %d bytes", len(r.Rows[0][0].Raw))
	}
}

func TestPGLoginMethods(t *testing.T) {
	base := pgConfig(t)
	admin := pgConnect(t, base)
	md5Role := tempRole(t, admin, "md5", "md5 pass")
	scramRole := tempRole(t, admin, "scram-sha-256", "scram:pass'")

	plain := base
	plain.SSLMode = "disable"
	for _, tc := range []struct {
		user, password, sslmode, want string
	}{
		{md5Role, "md5 pass", "disable", "md5"},
		{scramRole, "scram:pass'", "disable", "scram-sha-256"},
		{scramRole, "scram:pass'", "require", "scram-sha-256-plus"},
	} {
		cfg := base
		cfg.User, cfg.Password, cfg.SSLMode, cfg.Database = tc.user, tc.password, tc.sslmode, "postgres"
		c, err := Connect(context.Background(), cfg)
		if err != nil {
			if tc.sslmode == "require" && strings.Contains(err.Error(), "does not offer TLS") {
				t.Logf("server has no TLS; skipping %s", tc.want)
				continue
			}
			t.Fatalf("%s: %v", tc.want, err)
		}
		if c.AuthMethod() != tc.want {
			t.Errorf("%s: got %s", tc.want, c.AuthMethod())
		}
		c.Close()
	}
	for _, user := range []string{md5Role, scramRole} {
		cfg := plain
		cfg.User, cfg.Password, cfg.Database = user, "wrong", "postgres"
		if _, err := Connect(context.Background(), cfg); !IsCode(err, "28P01") {
			t.Errorf("%s with a wrong password: %v", user, err)
		}
	}
}

// TestPGCleartext needs a pg_hba line asking baresql_cleartext for a
// cleartext password (scripts/dev-engines.sh sets one up).
func TestPGCleartext(t *testing.T) {
	base := pgConfig(t)
	admin := pgConnect(t, base)
	mustQuery(t, admin, `DO $$ BEGIN CREATE ROLE baresql_cleartext LOGIN; EXCEPTION WHEN duplicate_object THEN NULL; END $$;
		ALTER ROLE baresql_cleartext PASSWORD 'clear text'`)
	cfg := base
	cfg.User, cfg.Password, cfg.Database, cfg.SSLMode = "baresql_cleartext", "clear text", "postgres", "disable"
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.AuthMethod() != "password" {
		t.Skipf("the server uses %s for baresql_cleartext (no pg_hba line for cleartext)", c.AuthMethod())
	}
}

func TestPGTLSVerification(t *testing.T) {
	base := pgConfig(t)
	cfg := base
	cfg.SSLMode = "require"
	c, err := Connect(context.Background(), cfg)
	if err != nil {
		if strings.Contains(err.Error(), "does not offer TLS") {
			t.Skip("server has no TLS")
		}
		t.Fatal(err)
	}
	if !c.TLSActive() {
		t.Fatal("TLS not active with sslmode=require")
	}
	c.Close()

	ca := os.Getenv("BARESQL_TEST_PG_CA")
	if ca == "" {
		t.Skip("BARESQL_TEST_PG_CA is not set; can't test verify-full")
	}
	for _, tc := range []struct {
		mode, server string
		ok           bool
	}{
		{"verify-full", "localhost", true},
		{"verify-full", "wrong.example.com", false},
		{"verify-ca", "wrong.example.com", true},
	} {
		cfg := base
		cfg.SSLMode, cfg.SSLRootCert, cfg.ServerName = tc.mode, ca, tc.server
		c, err := Connect(context.Background(), cfg)
		if tc.ok != (err == nil) {
			t.Errorf("%s %s: ok=%v err=%v", tc.mode, tc.server, tc.ok, err)
		}
		if err == nil {
			c.Close()
		}
	}
}

func TestPGUnixSocket(t *testing.T) {
	base := pgConfig(t)
	dir := os.Getenv("BARESQL_TEST_PG_SOCKET")
	if dir == "" {
		t.Skip("BARESQL_TEST_PG_SOCKET is not set")
	}
	cfg := base
	cfg.Host = dir
	c := pgConnect(t, cfg)
	if c.TLSActive() {
		t.Fatal("TLS over a unix socket")
	}
	if r := mustQuery(t, c, "SELECT 1"); r[0].Rows[0][0].String() != "1" {
		t.Fatal("query over the socket failed")
	}
}
