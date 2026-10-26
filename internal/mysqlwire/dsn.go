package mysqlwire

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ParseDSN accepts mysql://user:pass@host:port/db?tls=require&socket=/path&timeout=5s
// and mariadb://... (same form, Flavor "mariadb"). User and password are URL-decoded.
// When the password is empty and env MYSQL_PWD is set, use MYSQL_PWD.
//
// The query parameters are tls (disable, preferred, require or verify),
// tlsca (PEM file of CAs for verify), servername (the name verify checks
// instead of the host), socket (a unix socket path; host and port are
// then ignored) and timeout (a Go duration such as 5s). What the DSN
// leaves out stays zero, and Connect applies the defaults.
func ParseDSN(dsn string) (Config, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		// url.Parse quotes its input in errors, password and all, so say
		// only what usually went wrong.
		return Config{}, errors.New("mysqlwire: DSN is not a valid URL (percent-encode @ : / # % in the user name and password)")
	}
	if u.Scheme != "mysql" && u.Scheme != "mariadb" {
		return Config{}, fmt.Errorf("mysqlwire: DSN scheme %q, want mysql or mariadb", u.Scheme)
	}
	if u.Opaque != "" || u.Fragment != "" {
		return Config{}, errors.New("mysqlwire: DSN must look like mysql://user:pass@host:port/db (percent-encode '#' as %23)")
	}
	cfg := Config{Flavor: u.Scheme, Host: u.Hostname(), Database: strings.TrimPrefix(u.Path, "/")}
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}
	if cfg.Password == "" {
		cfg.Password = os.Getenv("MYSQL_PWD")
	}
	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("mysqlwire: DSN port %q out of range", p)
		}
		cfg.Port = port
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return Config{}, errors.New("mysqlwire: DSN has a malformed query string")
	}
	for key, values := range params {
		if len(values) > 1 {
			return Config{}, fmt.Errorf("mysqlwire: DSN parameter %q given more than once", key)
		}
		v := values[0]
		switch key {
		case "tls":
			if !validTLSMode(v) {
				return Config{}, fmt.Errorf("mysqlwire: DSN tls=%q, want disable, preferred, require or verify", v)
			}
			cfg.TLS = v
		case "tlsca":
			cfg.TLSCA = v
		case "servername":
			cfg.ServerName = v
		case "socket":
			cfg.Socket = v
		case "timeout":
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return Config{}, fmt.Errorf("mysqlwire: DSN timeout=%q, want a positive duration such as 5s", v)
			}
			cfg.Timeout = d
		default:
			return Config{}, fmt.Errorf("mysqlwire: unknown DSN parameter %q", key)
		}
	}
	return cfg, nil
}
