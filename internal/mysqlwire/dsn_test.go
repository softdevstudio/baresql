package mysqlwire

import (
	"strings"
	"testing"
	"time"
)

func TestParseDSN(t *testing.T) {
	t.Setenv("MYSQL_PWD", "")
	cases := []struct {
		dsn  string
		want Config
	}{
		{"mysql://root:root@127.0.0.1:3306/", Config{Flavor: "mysql", User: "root", Password: "root", Host: "127.0.0.1", Port: 3306}},
		{"mariadb://app:s3cret@db.example.com/shop", Config{Flavor: "mariadb", User: "app", Password: "s3cret", Host: "db.example.com", Database: "shop"}},
		// Percent-encoded user and password: @ : / # % space and UTF-8.
		{"mysql://r%40dmin:p%40ss%3Aw%2Frd%23%25%20%C3%BC@h:3307/db", Config{Flavor: "mysql", User: "r@dmin", Password: "p@ss:w/rd#% ü", Host: "h", Port: 3307, Database: "db"}},
		{"mysql://u:p@[::1]:3306/db", Config{Flavor: "mysql", User: "u", Password: "p", Host: "::1", Port: 3306, Database: "db"}},
		{"mysql://u@/db?socket=/var/run/mysqld/mysqld.sock", Config{Flavor: "mysql", User: "u", Database: "db", Socket: "/var/run/mysqld/mysqld.sock"}},
		{"mysql://u:p@h/?tls=require&timeout=5s", Config{Flavor: "mysql", User: "u", Password: "p", Host: "h", TLS: "require", Timeout: 5 * time.Second}},
		{"mariadb://u:p@h/?tls=verify&tlsca=/etc/ca.pem&servername=db.internal", Config{Flavor: "mariadb", User: "u", Password: "p", Host: "h", TLS: "verify", TLSCA: "/etc/ca.pem", ServerName: "db.internal"}},
		{"MySQL://u@h", Config{Flavor: "mysql", User: "u", Host: "h"}},
		{"mysql://h", Config{Flavor: "mysql", Host: "h"}},
	}
	for _, tc := range cases {
		got, err := ParseDSN(tc.dsn)
		if err != nil {
			t.Errorf("ParseDSN(%q): %v", tc.dsn, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseDSN(%q)\n got %+v\nwant %+v", tc.dsn, got, tc.want)
		}
	}
}

func TestParseDSNPasswordFromEnv(t *testing.T) {
	t.Setenv("MYSQL_PWD", "from-env")
	for dsn, want := range map[string]string{
		"mysql://u@h/":       "from-env",
		"mysql://u:@h/":      "from-env", // an empty password counts as none
		"mysql://u:given@h/": "given",
	} {
		cfg, err := ParseDSN(dsn)
		if err != nil || cfg.Password != want {
			t.Errorf("ParseDSN(%q): password %q, %v; want %q", dsn, cfg.Password, err, want)
		}
	}
}

func TestParseDSNErrors(t *testing.T) {
	cases := []struct{ dsn, want string }{
		{"postgres://u:p@h/db", "scheme"},
		{"mysql:u@h", "must look like"},
		{"mysql://u:pa#ss@h/db", "percent-encode"},
		{"mysql://u:p@h/db#x", "%23"},
		{"mysql://u:p@h:70000/", "out of range"},
		{"mysql://u:p@h/?tls=yes", "tls="},
		{"mysql://u:p@h/?timeout=soon", "timeout="},
		{"mysql://u:p@h/?timeout=-1s", "timeout="},
		{"mysql://u:p@h/?sslmode=require", "unknown DSN parameter"},
		{"mysql://u:p@h/?tls=require&tls=disable", "more than once"},
		{"mysql://u:p@h/?tls=%zz", "malformed query"},
	}
	for _, tc := range cases {
		if _, err := ParseDSN(tc.dsn); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseDSN(%q): %v, want an error containing %q", tc.dsn, err, tc.want)
		}
	}
}

// TestParseDSNKeepsSecrets checks that errors never repeat the password,
// which a CLI would print.
func TestParseDSNKeepsSecrets(t *testing.T) {
	for _, dsn := range []string{
		"mysql://u:hunter2@h:port/",     // url.Parse rejects the port and quotes the URL
		"mysql://u:hunter2@h/?tls=%zz",  // malformed query
		"mysql://u:hun#ter2@h/",         // '#' cuts the password short
		"mysql://u:hunter2@[::1/",       // broken IPv6 literal
		"mysql://u:hunter2\x7f@h/",      // control character
		"mysql://u:hunter2@h/?tls=nope", // bad parameter value
	} {
		_, err := ParseDSN(dsn)
		if err == nil {
			t.Errorf("ParseDSN(%q) accepted", dsn)
			continue
		}
		if msg := err.Error(); strings.Contains(msg, "hunter2") || strings.Contains(msg, "hun") && strings.Contains(msg, "ter2") {
			t.Errorf("ParseDSN(%q) error shows the password: %s", dsn, msg)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	got, err := Config{User: "u"}.withDefaults()
	want := Config{User: "u", Host: "127.0.0.1", Port: 3306, TLS: "preferred", Timeout: 10 * time.Second}
	if err != nil || got != want {
		t.Errorf("withDefaults = %+v, %v", got, err)
	}
	for _, cfg := range []Config{
		{Port: 65536},
		{TLS: "on"},
		{Timeout: -time.Second},
		{User: "a\x00b"},
		{Database: "a\x00b"},
	} {
		if _, err := cfg.withDefaults(); err == nil {
			t.Errorf("withDefaults accepted %+v", cfg)
		}
	}
}
