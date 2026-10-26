package mysqlwire

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/softdevstudio/baresql/internal/testenv"
)

// The integration tests run against the servers named by
// BARESQL_TEST_MYSQL and BARESQL_TEST_MARIADB. An unset variable skips its
// engine, unless BARESQL_REQUIRE_ENGINES lists the engine (or "all"),
// which makes it a failure. They need only root: accounts and databases
// are created with random names and dropped afterwards.

// engine is one server under test.
type engine struct {
	name   string // "mysql" or "mariadb"
	cfg    Config // root, from the DSN
	tls    bool   // the handshake offers TLS
	socket string // the server's unix socket, when this host can reach it
}

// forEachEngine runs fn as a subtest for each engine.
func forEachEngine(t *testing.T, fn func(t *testing.T, e *engine)) {
	for _, name := range []string{testenv.MySQL, testenv.MariaDB} {
		t.Run(name, func(t *testing.T) {
			cfg, err := ParseDSN(testenv.DSN(t, name))
			if err != nil {
				t.Fatalf("%s DSN: %v", name, err)
			}
			e := &engine{name: name, cfg: cfg}
			e.probe(t)
			fn(t, e)
		})
	}
}

// probe asks the server whether it offers TLS (decided by the server, not
// the flavor: MariaDB 11.4 and later have it on by default) and where its
// unix socket is.
func (e *engine) probe(t *testing.T) {
	const identity = "SELECT CONCAT_WS(' ', @@hostname, @@port, @@datadir, VERSION())"
	c := e.connect(t, func(cfg *Config) { cfg.Socket, cfg.TLS = "", "disable" })
	e.tls = c.serverCaps&clientSSL != 0
	path, want := value(t, c, "SELECT @@socket"), value(t, c, identity)
	c.Close()
	// A server in a container reports a socket path inside it. A file at
	// that path on this host may belong to another server, so use it only
	// if it leads back to this one.
	if fi, err := os.Stat(path); err != nil || fi.Mode()&os.ModeSocket == 0 {
		return
	}
	sc, err := e.dial(func(cfg *Config) { cfg.Socket, cfg.TLS = path, "disable" })
	if err != nil {
		return
	}
	defer sc.Close()
	if res, err := sc.Query(context.Background(), identity); err == nil && res[0].Rows[0][0].String() == want {
		e.socket = path
	}
}

func (e *engine) dial(edit func(*Config)) (*Conn, error) {
	cfg := e.cfg
	if edit != nil {
		edit(&cfg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return Connect(ctx, cfg)
}

// connect logs in with the root settings as edit changes them.
func (e *engine) connect(t *testing.T, edit func(*Config)) *Conn {
	t.Helper()
	c, err := e.dial(edit)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// cleanup runs stmt as root on a fresh connection when the test ends; the
// test's own connections may be broken or closed by then.
func (e *engine) cleanup(t *testing.T, stmt string) {
	t.Cleanup(func() {
		c, err := e.dial(nil)
		if err != nil {
			t.Errorf("cleanup %s: %v", stmt, err)
			return
		}
		defer c.Close()
		if _, err := c.Query(context.Background(), stmt); err != nil {
			t.Errorf("cleanup %s: %v", stmt, err)
		}
	})
}

// transport is one way to reach the server.
type transport struct {
	name string
	set  func(*Config)
}

// transports lists TLS over TCP when the server offers it, plain TCP, and
// the unix socket when this host can reach it.
func (e *engine) transports() []transport {
	var out []transport
	if e.tls {
		out = append(out, transport{"tls", func(c *Config) { c.Socket, c.TLS = "", "require" }})
	}
	out = append(out, transport{"tcp", func(c *Config) { c.Socket, c.TLS = "", "disable" }})
	if e.socket != "" {
		out = append(out, transport{"socket", func(c *Config) { c.Socket, c.TLS = e.socket, "disable" }})
	}
	return out
}

func randomName(prefix string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// createUser makes a throwaway account, dropped when the test ends. An
// empty plugin means the server's default. ok is false when the server
// does not have the plugin: MySQL 8.4 ships without mysql_native_password
// loaded, MySQL 9 without it at all, MariaDB without the SHA-2 plugins.
func (e *engine) createUser(t *testing.T, plugin, password string) (user string, ok bool) {
	t.Helper()
	root := e.connect(t, nil)
	user = randomName("bsq_")
	stmt := fmt.Sprintf("CREATE USER '%s'@'%%'", user)
	switch {
	case plugin == "" && password != "":
		stmt += fmt.Sprintf(" IDENTIFIED BY '%s'", password)
	case plugin == "":
	case root.IsMariaDB():
		stmt += fmt.Sprintf(" IDENTIFIED VIA %s USING PASSWORD('%s')", plugin, password)
	default:
		stmt += fmt.Sprintf(" IDENTIFIED WITH %s BY '%s'", plugin, password)
	}
	_, err := root.Query(context.Background(), stmt)
	var se *Error
	if errors.As(err, &se) && (se.Code == 1524 || strings.Contains(se.Message, "not loaded")) {
		return "", false
	}
	if err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
	e.cleanup(t, fmt.Sprintf("DROP USER '%s'@'%%'", user))
	return user, true
}

// createDatabase makes a throwaway utf8mb4 database (the server default
// may be latin1), dropped when the test ends.
func (e *engine) createDatabase(t *testing.T, root *Conn) string {
	t.Helper()
	name := randomName("bsq_")
	exec(t, root, "CREATE DATABASE "+name+" CHARACTER SET utf8mb4")
	e.cleanup(t, "DROP DATABASE IF EXISTS "+name)
	return name
}

// exec runs statements and returns the results of the last one.
func exec(t *testing.T, c *Conn, stmts ...string) []*Result {
	t.Helper()
	var res []*Result
	for _, s := range stmts {
		var err error
		if res, err = c.Query(context.Background(), s); err != nil {
			t.Fatalf("%.80s: %v", s, err)
		}
	}
	return res
}

// value runs a query that returns a single value.
func value(t *testing.T, c *Conn, sql string) string {
	t.Helper()
	res := exec(t, c, sql)
	if len(res) != 1 || len(res[0].Rows) != 1 || len(res[0].Rows[0]) != 1 {
		t.Fatalf("%s: want one value, got %d results", sql, len(res))
	}
	return res[0].Rows[0][0].String()
}

func TestIntegrationSession(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		c := e.connect(t, func(cfg *Config) { cfg.Socket, cfg.TLS = "", "preferred" })
		if c.IsMariaDB() != (e.name == "mariadb") {
			t.Errorf("IsMariaDB() = %v on %s %s", c.IsMariaDB(), e.name, c.ServerVersion())
		}
		if c.TLSActive() != e.tls {
			t.Errorf("tls=preferred: TLSActive %v, server offers TLS: %v", c.TLSActive(), e.tls)
		}
		row := exec(t, c, "SELECT VERSION(), CONNECTION_ID(), @@character_set_connection, @@character_set_results, @@collation_connection, @@time_zone")[0].Rows[0]
		if row[0].String() != c.ServerVersion() || strings.HasPrefix(c.ServerVersion(), "5.5.5-") {
			t.Errorf("ServerVersion %q, VERSION() %q", c.ServerVersion(), row[0])
		}
		if row[1].String() != strconv.FormatUint(uint64(c.ConnectionID()), 10) {
			t.Errorf("ConnectionID %d, CONNECTION_ID() %s", c.ConnectionID(), row[1])
		}
		if row[2].String() != "utf8mb4" || row[3].String() != "utf8mb4" || !strings.HasPrefix(row[4].String(), "utf8mb4_") {
			t.Errorf("connection charset %s, results %s, collation %s", row[2], row[3], row[4])
		}
		if row[5].String() != "+00:00" {
			t.Errorf("time_zone %s", row[5])
		}
		if !c.IsMariaDB() {
			// SET NAMES utf8mb4 gives the server's default utf8mb4 collation.
			if v := value(t, c, "SELECT @@collation_connection = @@default_collation_for_utf8mb4"); v != "1" {
				t.Errorf("collation %s is not the server's utf8mb4 default", row[4])
			}
		}
		cipher := "" // a server built without TLS may not have the variable at all
		if rows := exec(t, c, "SHOW SESSION STATUS LIKE 'Ssl_cipher'")[0].Rows; len(rows) == 1 {
			cipher = rows[0][1].String()
		}
		if (cipher != "") != c.TLSActive() {
			t.Errorf("TLSActive %v, but the server reports cipher %q", c.TLSActive(), cipher)
		}
	})
}

func TestIntegrationAuthPlugins(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		for _, plugin := range []string{pluginNative, pluginCachingSHA2, pluginSHA256} {
			t.Run(plugin, func(t *testing.T) {
				password := randomName("Pw-ü-")
				user, ok := e.createUser(t, plugin, password)
				if !ok {
					t.Skipf("%s is not loaded on %s", plugin, e.name)
				}
				for _, tr := range e.transports() {
					t.Run(tr.name, func(t *testing.T) {
						c := e.connect(t, func(cfg *Config) {
							cfg.User, cfg.Password = user, password
							tr.set(cfg)
						})
						if c.AuthPlugin() != plugin {
							t.Errorf("AuthPlugin %q", c.AuthPlugin())
						}
						if got := value(t, c, "SELECT CURRENT_USER()"); got != user+"@%" {
							t.Errorf("CURRENT_USER() = %s", got)
						}
					})
				}
			})
		}
		t.Run("empty password", func(t *testing.T) {
			user, _ := e.createUser(t, "", "")
			for _, tr := range e.transports() {
				c := e.connect(t, func(cfg *Config) {
					cfg.User, cfg.Password = user, ""
					tr.set(cfg)
				})
				if got := value(t, c, "SELECT 1"); got != "1" {
					t.Errorf("%s: SELECT 1 = %s", tr.name, got)
				}
			}
		})
	})
}

// TestIntegrationCachingSHA2Cache: after FLUSH PRIVILEGES empties the
// server's cache, the first login is a full authentication (password in
// clear over TLS or the socket, RSA-encrypted over plain TCP) and the
// next one takes the fast path.
func TestIntegrationCachingSHA2Cache(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		password := randomName("Pw-")
		user, ok := e.createUser(t, pluginCachingSHA2, password)
		if !ok {
			t.Skipf("caching_sha2_password is not loaded on %s", e.name)
		}
		root := e.connect(t, nil)
		for _, tr := range e.transports() {
			t.Run(tr.name, func(t *testing.T) {
				exec(t, root, "FLUSH PRIVILEGES")
				for i, wantFull := range []bool{true, false} {
					c := e.connect(t, func(cfg *Config) {
						cfg.User, cfg.Password = user, password
						tr.set(cfg)
					})
					if c.fullAuth != wantFull || c.AuthPlugin() != pluginCachingSHA2 {
						t.Errorf("login %d: full authentication %v, want %v (plugin %s)", i+1, c.fullAuth, wantFull, c.AuthPlugin())
					}
					c.Close()
				}
			})
		}
	})
}

func TestIntegrationTLSModes(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		tcp := func(mode, ca string) func(*Config) {
			return func(cfg *Config) { cfg.Socket, cfg.TLS, cfg.TLSCA = "", mode, ca }
		}
		for _, tc := range []struct {
			mode string
			want bool
		}{{"disable", false}, {"preferred", e.tls}} {
			if c := e.connect(t, tcp(tc.mode, "")); c.TLSActive() != tc.want {
				t.Errorf("tls=%s: TLSActive %v", tc.mode, c.TLSActive())
			}
		}
		c, err := e.dial(tcp("require", ""))
		switch {
		case e.tls && err != nil:
			t.Errorf("tls=require: %v", err)
		case e.tls:
			if !c.TLSActive() || value(t, c, "SELECT 1") != "1" {
				t.Errorf("tls=require: TLSActive %v", c.TLSActive())
			}
			c.Close()
		case err == nil || !strings.Contains(err.Error(), "does not offer TLS"):
			t.Errorf("tls=require without server TLS: %v", err)
		}
		// A CA that signed nothing on the server: verification must fail.
		_, otherCA := testCerts(t)
		if _, err := e.dial(tcp("verify", otherCA)); err == nil {
			t.Error("tls=verify accepted a certificate from an unknown CA")
		} else if e.tls && !strings.Contains(err.Error(), "certificate") {
			t.Errorf("tls=verify: %v", err)
		}
		if e.socket == "" {
			return
		}
		// On the socket "preferred" stays plain and "require" upgrades.
		password := randomName("Pw-")
		user, _ := e.createUser(t, "", password)
		socket := func(mode string) func(*Config) {
			return func(cfg *Config) { cfg.Socket, cfg.TLS, cfg.User, cfg.Password = e.socket, mode, user, password }
		}
		if c := e.connect(t, socket("preferred")); c.TLSActive() {
			t.Error("tls=preferred upgraded a unix socket")
		}
		c, err = e.dial(socket("require"))
		switch {
		case !e.tls && err == nil:
			t.Error("tls=require over the socket succeeded without server TLS")
		case e.tls && err != nil:
			t.Errorf("tls=require over the socket: %v", err)
		case e.tls && !c.TLSActive():
			t.Error("tls=require over the socket stayed plain")
		}
		if err == nil {
			c.Close()
		}
	})
}

func TestIntegrationErrors(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		user, _ := e.createUser(t, "", "right-"+randomName(""))
		_, err := e.dial(func(cfg *Config) { cfg.User, cfg.Password = user, "wrong" })
		var se *Error
		if !errors.As(err, &se) || se.Code != 1045 || se.SQLState != "28000" {
			t.Errorf("bad password: %v", err)
		}
		c := e.connect(t, nil)
		if _, err := c.Query(context.Background(), "SELEC 1"); !errors.As(err, &se) || se.Code != 1064 || se.SQLState != "42000" {
			t.Errorf("syntax error: %v", err)
		}
		if _, err := c.Query(context.Background(), "SELECT * FROM mysql.bsq_no_such_table"); !errors.As(err, &se) || se.Code != 1146 {
			t.Errorf("missing table: %v", err)
		}
		if got := value(t, c, "SELECT 1"); got != "1" {
			t.Errorf("after errors: SELECT 1 = %s", got)
		}
	})
}

func TestIntegrationTypes(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		c := e.connect(t, nil)
		db := e.createDatabase(t, c)
		allBytes := make([]byte, 256)
		for i := range allBytes {
			allBytes[i] = byte(i)
		}
		exec(t, c,
			"CREATE TABLE "+db+".t (i INT, u BIGINT UNSIGNED, d DECIMAL(10,4), f DOUBLE, v VARCHAR(40), b BLOB, "+
				"dt DATE, dtm DATETIME(6), ts TIMESTAMP NULL, tm TIME, j JSON, bt BIT(8), e ENUM('red','green'))",
			"INSERT INTO "+db+".t VALUES (-42, 18446744073709551615, 123.4567, 0.1, 'Grüße, 世界 🙂', X'"+hex.EncodeToString(allBytes)+"', "+
				`'2024-02-29', '2024-02-29 12:34:56.123456', '2024-02-29 12:34:56', '-838:59:59', '{"b": 1, "a": [1, 2.5, "x"]}', b'10100101', 'green')`,
			"INSERT INTO "+db+".t VALUES (NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL)",
		)
		res := exec(t, c, "SELECT * FROM "+db+".t")
		if len(res) != 1 || len(res[0].Rows) != 2 {
			t.Fatalf("got %d results", len(res))
		}
		r := res[0]
		// What the servers send, as seen from MySQL 8.0 and MariaDB 10.11:
		// MySQL keeps JSON in a normalized binary form and prints it with
		// sorted keys; for MariaDB JSON is LONGTEXT and comes back as stored.
		jsonType, jsonText := "JSON", `{"a": [1, 2.5, "x"], "b": 1}`
		if c.IsMariaDB() {
			jsonType, jsonText = "BLOB", `{"b": 1, "a": [1, 2.5, "x"]}`
		}
		want := []struct {
			name, typ string
			raw       []byte
		}{
			{"i", "LONG", []byte("-42")},
			{"u", "LONGLONG", []byte("18446744073709551615")},
			{"d", "NEWDECIMAL", []byte("123.4567")},
			{"f", "DOUBLE", []byte("0.1")},
			{"v", "VAR_STRING", []byte("Grüße, 世界 🙂")},
			{"b", "BLOB", allBytes},
			{"dt", "DATE", []byte("2024-02-29")},
			{"dtm", "DATETIME", []byte("2024-02-29 12:34:56.123456")},
			{"ts", "TIMESTAMP", []byte("2024-02-29 12:34:56")},
			{"tm", "TIME", []byte("-838:59:59")},
			{"j", jsonType, []byte(jsonText)},
			{"bt", "BIT", []byte{0xA5}},
			{"e", "STRING", []byte("green")},
		}
		if len(r.Columns) != len(want) {
			t.Fatalf("%d columns", len(r.Columns))
		}
		for i, w := range want {
			col, v, null := r.Columns[i], r.Rows[0][i], r.Rows[1][i]
			if col.Name != w.name || col.TypeName() != w.typ {
				t.Errorf("column %d: %s %s, want %s %s", i, col.Name, col.TypeName(), w.name, w.typ)
			}
			if v.Null || !bytes.Equal(v.Raw, w.raw) {
				t.Errorf("%s = %q, want %q", w.name, v.Raw, w.raw)
			}
			if !null.Null || null.Raw != nil {
				t.Errorf("%s in the NULL row: %+v", w.name, null)
			}
		}
		id := r.Columns[0]
		if id.Schema != db || id.Table != "t" || id.OrgTable != "t" || id.OrgName != "i" {
			t.Errorf("column metadata %+v", id)
		}
		const unsignedFlag, enumFlag = 0x0020, 0x0100
		if r.Columns[1].Flags&unsignedFlag == 0 || r.Columns[12].Flags&enumFlag == 0 {
			t.Errorf("flags: u %#x, e %#x", r.Columns[1].Flags, r.Columns[12].Flags)
		}
		if r.Columns[2].Decimals != 4 || r.Columns[7].Decimals != 6 {
			t.Errorf("decimals: d %d, dtm %d", r.Columns[2].Decimals, r.Columns[7].Decimals)
		}
		if !r.Columns[5].IsBinary() || r.Columns[4].IsBinary() || r.Columns[10].IsBinary() {
			t.Errorf("IsBinary: b %v, v %v, j %v", r.Columns[5].IsBinary(), r.Columns[4].IsBinary(), r.Columns[10].IsBinary())
		}
	})
}

// TestIntegrationLargeValues sends a 20 MiB value and reads it back, so
// payloads split across packets in both directions; the row starts with
// 0xFE because the value is longer than 16 MiB. A statement of exactly
// 0xFFFFFF bytes needs the empty packet after it.
func TestIntegrationLargeValues(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		root := e.connect(t, nil)
		const need = 64 << 20
		orig := value(t, root, "SELECT @@GLOBAL.max_allowed_packet")
		if n, err := strconv.Atoi(orig); err != nil || n < need {
			exec(t, root, "SET GLOBAL max_allowed_packet = "+strconv.Itoa(need))
			e.cleanup(t, "SET GLOBAL max_allowed_packet = "+orig)
		}
		c := e.connect(t, nil) // a new session picks up the global value
		db := e.createDatabase(t, c)

		pad := 1<<24 - 1 - len("\x03SELECT LENGTH('')")
		if got := value(t, c, "SELECT LENGTH('"+strings.Repeat("y", pad)+"')"); got != strconv.Itoa(pad) {
			t.Errorf("statement of exactly 0xFFFFFF bytes: LENGTH = %s, want %d", got, pad)
		}

		payload := make([]byte, 20<<20)
		for i := range payload {
			payload[i] = 'a' + byte(i%26)
		}
		exec(t, c, "CREATE TABLE "+db+".big (v LONGBLOB)", "INSERT INTO "+db+".big VALUES ('"+string(payload)+"')")
		res := exec(t, c, "SELECT v, LENGTH(v) FROM "+db+".big")
		if len(res) != 1 || len(res[0].Rows) != 1 {
			t.Fatalf("got %d results", len(res))
		}
		row := res[0].Rows[0]
		if !bytes.Equal(row[0].Raw, payload) || row[1].String() != strconv.Itoa(len(payload)) {
			t.Errorf("value came back as %d bytes (server length %s), intact: %v", len(row[0].Raw), row[1], bytes.Equal(row[0].Raw, payload))
		}
		if got := value(t, c, "SELECT 1"); got != "1" {
			t.Errorf("after the large values: %s", got)
		}
	})
}

func TestIntegrationCall(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		c := e.connect(t, nil)
		db := e.createDatabase(t, c)
		exec(t, c, "CREATE PROCEDURE "+db+".two() BEGIN SELECT 1 AS a; SELECT 'x' AS b, 2 AS c; END")
		res := exec(t, c, "CALL "+db+".two()")
		if len(res) != 3 {
			t.Fatalf("CALL gave %d results, want two result sets and the CALL's own", len(res))
		}
		if cols, rows := res[0].Columns, res[0].Rows; len(cols) != 1 || cols[0].Name != "a" || len(rows) != 1 || rows[0][0].String() != "1" {
			t.Errorf("first result %+v", res[0])
		}
		if cols, rows := res[1].Columns, res[1].Rows; len(cols) != 2 || cols[1].Name != "c" || len(rows) != 1 ||
			rows[0][0].String() != "x" || rows[0][1].String() != "2" {
			t.Errorf("second result %+v", res[1])
		}
		if res[0].Status&serverMoreResultsExists == 0 || res[2].Columns != nil || res[2].Status&serverMoreResultsExists != 0 {
			t.Errorf("statuses %#x %#x %#x, last columns %v", res[0].Status, res[1].Status, res[2].Status, res[2].Columns)
		}
		if got := value(t, c, "SELECT 1"); got != "1" {
			t.Errorf("after CALL: %s", got)
		}
	})
}

// TestIntegrationManyRows reads a result of more than 256 packets, so the
// sequence id wraps around several times.
func TestIntegrationManyRows(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		c := e.connect(t, nil)
		res := exec(t, c, "WITH RECURSIVE r (n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM r WHERE n < 1000) SELECT n FROM r")
		if len(res) != 1 || len(res[0].Rows) != 1000 {
			t.Fatalf("got %d results", len(res))
		}
		for i, row := range res[0].Rows {
			if row[0].String() != strconv.Itoa(i+1) {
				t.Fatalf("row %d = %s", i, row[0])
			}
		}
	})
}

func TestIntegrationAffectedRows(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		c := e.connect(t, nil)
		db := e.createDatabase(t, c)
		exec(t, c, "CREATE TABLE "+db+".ai (id INT AUTO_INCREMENT PRIMARY KEY, x INT)")
		cases := []struct {
			sql              string
			affected, lastID uint64
		}{
			// A multi-row INSERT reports the first id it generated.
			{"INSERT INTO " + db + ".ai (x) VALUES (1), (2), (3)", 3, 1},
			{"INSERT INTO " + db + ".ai (x) VALUES (4)", 1, 4},
			{"UPDATE " + db + ".ai SET x = x + 10 WHERE x >= 2", 3, 0},
			// Without CLIENT_FOUND_ROWS only changed rows count.
			{"UPDATE " + db + ".ai SET x = x WHERE id = 1", 0, 0},
			{"DELETE FROM " + db + ".ai WHERE id > 2", 2, 0},
		}
		for _, tc := range cases {
			res := exec(t, c, tc.sql)
			if len(res) != 1 || res[0].Columns != nil || res[0].AffectedRows != tc.affected || res[0].LastInsertID != tc.lastID {
				t.Errorf("%s: %+v, want %d affected, id %d", tc.sql, res[0], tc.affected, tc.lastID)
			}
		}
	})
}

func TestIntegrationWarnings(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		c := e.connect(t, nil)
		res := exec(t, c, "SELECT 'abc' + 1")
		if res[0].Warnings != 1 || res[0].Rows[0][0].String() != "1" {
			t.Errorf("SELECT 'abc' + 1: %s with %d warnings", res[0].Rows[0][0], res[0].Warnings)
		}
		res = exec(t, c, "SHOW WARNINGS")
		if len(res[0].Rows) != 1 {
			t.Fatalf("%d warnings", len(res[0].Rows))
		}
		w := res[0].Rows[0]
		if w[0].String() != "Warning" || w[1].String() != "1292" || w[2].String() != "Truncated incorrect DOUBLE value: 'abc'" {
			t.Errorf("warning %s %s %s", w[0], w[1], w[2])
		}
	})
}

func TestIntegrationContextDeadline(t *testing.T) {
	forEachEngine(t, func(t *testing.T, e *engine) {
		c := e.connect(t, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := c.Query(ctx, "SELECT SLEEP(5)")
		if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
			t.Errorf("Query returned after %v", elapsed)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v, want an error wrapping context.DeadlineExceeded", err)
		}
		if _, err := c.Query(context.Background(), "SELECT 1"); err == nil {
			t.Error("the Conn still works after an interrupted query")
		}
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
}
