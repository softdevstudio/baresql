package pgwire

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config says where and how to connect. ParseDSN fills one from a
// connection string the way psql would, environment variables included.
type Config struct {
	// Host is a host name, an IP address, or (starting with "/") the
	// directory holding the server's unix socket.
	Host     string
	Port     int
	User     string
	Password string
	Database string

	// SSLMode is disable, prefer, require, verify-ca or verify-full, as in
	// libpq. With prefer, TLS is used when the server offers it.
	SSLMode string
	// SSLRootCert is a PEM file of trusted CAs for verify-ca and
	// verify-full, or "system" for the system roots (implies verify-full).
	SSLRootCert string
	// SSLCert and SSLKey are an optional client certificate and key (PEM).
	SSLCert, SSLKey string
	// ServerName overrides the name checked by verify-full.
	ServerName string
	// ChannelBinding is disable, prefer (default) or require: whether SCRAM
	// binds the login to the TLS connection, which stops a man in the middle
	// even when the certificate isn't verified.
	ChannelBinding string

	// ConnectTimeout bounds dialing, TLS and login together. Default 10s.
	ConnectTimeout time.Duration

	// Params are sent in the startup message (application_name, options,
	// DateStyle, TimeZone ...). client_encoding is always UTF8.
	Params map[string]string
}

var knownParams = map[string]bool{
	"host": true, "hostaddr": true, "port": true, "user": true, "password": true, "dbname": true,
	"sslmode": true, "sslrootcert": true, "sslcert": true, "sslkey": true, "channel_binding": true,
	"connect_timeout": true, "application_name": true, "options": true,
}

// ParseDSN reads a PostgreSQL connection string: a URL
// (postgres://user:pass@host:5432/db?sslmode=require) or keyword=value pairs
// (host=db user=me sslmode=verify-full). Anything it leaves out comes from
// the PG* environment variables and then libpq's defaults; a missing
// password is looked up in ~/.pgpass (or PGPASSFILE). An empty string
// connects like psql with no arguments.
func ParseDSN(dsn string) (Config, error) {
	kv, err := splitDSN(strings.TrimSpace(dsn))
	if err != nil {
		return Config{}, err
	}
	env := map[string]string{
		"host": "PGHOST", "hostaddr": "PGHOSTADDR", "port": "PGPORT", "user": "PGUSER",
		"password": "PGPASSWORD", "dbname": "PGDATABASE", "sslmode": "PGSSLMODE",
		"sslrootcert": "PGSSLROOTCERT", "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY",
		"channel_binding": "PGCHANNELBINDING", "connect_timeout": "PGCONNECT_TIMEOUT",
		"application_name": "PGAPPNAME", "options": "PGOPTIONS",
	}
	for k, name := range env {
		if _, ok := kv[k]; !ok {
			if v := os.Getenv(name); v != "" {
				kv[k] = v
			}
		}
	}

	c := Config{
		Host:           kv["host"],
		User:           kv["user"],
		Password:       kv["password"],
		Database:       kv["dbname"],
		SSLMode:        strings.ToLower(kv["sslmode"]),
		SSLRootCert:    kv["sslrootcert"],
		SSLCert:        kv["sslcert"],
		SSLKey:         kv["sslkey"],
		ChannelBinding: strings.ToLower(kv["channel_binding"]),
		Params:         map[string]string{},
	}
	if a := kv["hostaddr"]; a != "" {
		// libpq dials hostaddr but still checks the certificate against host.
		if c.Host != "" && !strings.HasPrefix(c.Host, "/") {
			c.ServerName = c.Host
		}
		c.Host = a
	}
	if p := kv["port"]; p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return Config{}, fmt.Errorf("pgwire: invalid port %q", p)
		}
		c.Port = n
	}
	if t := kv["connect_timeout"]; t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 0 {
			return Config{}, fmt.Errorf("pgwire: invalid connect_timeout %q (seconds)", t)
		}
		c.ConnectTimeout = time.Duration(n) * time.Second
	}
	if v := kv["application_name"]; v != "" {
		c.Params["application_name"] = v
	}
	if v := kv["options"]; v != "" {
		c.Params["options"] = v
	}
	if err := c.fillDefaults(); err != nil {
		return Config{}, err
	}
	if c.Password == "" {
		c.Password = pgpassLookup(c)
	}
	return c, nil
}

func (c *Config) fillDefaults() error {
	if c.Port == 0 {
		c.Port = 5432
	}
	if c.Host == "" {
		c.Host = defaultHost(c.Port)
	}
	if c.User == "" {
		if u, err := user.Current(); err == nil {
			name := u.Username
			if i := strings.LastIndex(name, `\`); i >= 0 { // Windows DOMAIN\user
				name = name[i+1:]
			}
			c.User = name
		}
		if c.User == "" {
			return errors.New("pgwire: no user name given and the current user is unknown")
		}
	}
	if c.Database == "" {
		c.Database = c.User
	}
	switch c.SSLMode {
	case "":
		c.SSLMode = "prefer"
		if c.SSLRootCert == "system" {
			c.SSLMode = "verify-full" // libpq's default with the system roots
		}
	case "disable", "prefer", "require", "verify-ca", "verify-full":
	case "allow":
		return errors.New(`pgwire: sslmode "allow" is not supported; use "prefer"`)
	default:
		return fmt.Errorf("pgwire: invalid sslmode %q", c.SSLMode)
	}
	if c.SSLRootCert == "system" && c.SSLMode != "verify-full" {
		// libpq refuses weaker modes with the system roots.
		return fmt.Errorf(`pgwire: sslrootcert=system needs sslmode=verify-full, not %q`, c.SSLMode)
	}
	switch c.ChannelBinding {
	case "":
		c.ChannelBinding = "prefer"
	case "disable", "prefer", "require":
	default:
		return fmt.Errorf("pgwire: invalid channel_binding %q", c.ChannelBinding)
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 10 * time.Second
	}
	return nil
}

// defaultHost is the unix socket directory a local server would use, as
// libpq does, or localhost when there is none.
func defaultHost(port int) string {
	if runtime.GOOS != "windows" {
		for _, dir := range []string{"/var/run/postgresql", "/tmp"} {
			if _, err := os.Stat(filepath.Join(dir, ".s.PGSQL."+strconv.Itoa(port))); err == nil {
				return dir
			}
		}
	}
	return "localhost"
}

// IsUnixSocket reports whether Host names a socket directory.
func (c Config) IsUnixSocket() bool {
	return strings.HasPrefix(c.Host, "/") || strings.HasPrefix(c.Host, "@")
}

// Redacted returns the connection target without the password, for logs
// and reports.
func (c Config) Redacted() string {
	host := c.Host
	if !c.IsUnixSocket() {
		host = fmt.Sprintf("%s:%d", c.Host, c.Port)
	}
	return fmt.Sprintf("postgres://%s@%s/%s", c.User, host, c.Database)
}

func splitDSN(dsn string) (map[string]string, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return splitURL(dsn)
	}
	return splitKeywords(dsn)
}

func splitURL(dsn string) (map[string]string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("pgwire: invalid connection URL: %w", redactURLError(err))
	}
	kv := map[string]string{}
	if u.User != nil {
		if n := u.User.Username(); n != "" {
			kv["user"] = n
		}
		if p, ok := u.User.Password(); ok {
			kv["password"] = p
		}
	}
	if h := u.Hostname(); h != "" {
		kv["host"] = h
	}
	if p := u.Port(); p != "" {
		kv["port"] = p
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" {
		kv["dbname"] = db
	}
	for k, vs := range u.Query() {
		if !knownParams[k] {
			return nil, fmt.Errorf("pgwire: unknown connection parameter %q", k)
		}
		kv[k] = vs[len(vs)-1]
	}
	return kv, nil
}

// redactURLError drops the URL (and so any password) from a url.Error.
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// splitKeywords parses libpq's keyword=value form. Values may be quoted with
// single quotes; a backslash escapes the next character.
func splitKeywords(s string) (map[string]string, error) {
	kv := map[string]string{}
	i := 0
	for {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			return kv, nil
		}
		start := i
		for i < len(s) && s[i] != '=' && !isSpace(s[i]) {
			i++
		}
		key := s[start:i]
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) || s[i] != '=' {
			return nil, fmt.Errorf("pgwire: missing \"=\" after %q in connection string", key)
		}
		i++
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		var val strings.Builder
		if i < len(s) && s[i] == '\'' {
			i++
			closed := false
			for i < len(s) {
				ch := s[i]
				if ch == '\\' && i+1 < len(s) {
					val.WriteByte(s[i+1])
					i += 2
					continue
				}
				if ch == '\'' {
					i++
					closed = true
					break
				}
				val.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("pgwire: unterminated quoted value for %q", key)
			}
		} else {
			for i < len(s) && !isSpace(s[i]) {
				if s[i] == '\\' && i+1 < len(s) {
					val.WriteByte(s[i+1])
					i += 2
					continue
				}
				val.WriteByte(s[i])
				i++
			}
		}
		if !knownParams[key] {
			return nil, fmt.Errorf("pgwire: unknown connection parameter %q", key)
		}
		kv[key] = val.String()
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == '\v'
}

// pgpassLookup finds a password in the password file, as libpq does. A
// file others can read is ignored on Unix, like libpq ignores it.
func pgpassLookup(c Config) string {
	path := os.Getenv("PGPASSFILE")
	if path == "" {
		if runtime.GOOS == "windows" {
			path = filepath.Join(os.Getenv("APPDATA"), "postgresql", "pgpass.conf")
		} else if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, ".pgpass")
		}
	}
	if path == "" {
		return ""
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return ""
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	host := c.Host
	if c.IsUnixSocket() {
		host = "localhost" // libpq matches socket connections as localhost
	}
	want := []string{host, strconv.Itoa(c.Port), c.Database, c.User}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		fields := splitPgpass(line)
		if len(fields) != 5 {
			continue
		}
		ok := true
		for i, w := range want {
			if fields[i] != "*" && fields[i] != w {
				ok = false
				break
			}
		}
		if ok {
			return fields[4]
		}
	}
	return ""
}

func splitPgpass(line string) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch ch := line[i]; {
		case ch == '\\' && i+1 < len(line):
			cur.WriteByte(line[i+1])
			i++
		case ch == ':' && len(out) < 4:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	return append(out, cur.String())
}
