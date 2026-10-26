package portable

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/softdevstudio/baresql/internal/mysqlwire"
	"github.com/softdevstudio/baresql/internal/pgwire"
	"github.com/softdevstudio/baresql/internal/testenv"
)

// opener starts one fresh engine.
type opener struct {
	name string
	open func(ctx context.Context) (Engine, error)
}

var testenvName = map[string]string{SQLite: testenv.SQLite, Postgres: testenv.PG, MySQL: testenv.MySQL, MariaDB: testenv.MariaDB}

// availableOpeners returns an opener for each engine the environment
// offers. A required engine that is missing fails the test; a missing
// optional one is left out. With none at all the test is skipped.
func availableOpeners(t *testing.T) []opener {
	t.Helper()
	var out []opener
	for _, name := range EngineOrder {
		if o, ok := engineOpener(t, name); ok {
			out = append(out, o)
		}
	}
	if len(out) == 0 {
		t.Skip("no engine is configured (see internal/testenv)")
	}
	return out
}

// engineOpener returns the opener of one engine, if it is configured.
func engineOpener(t *testing.T, name string) (opener, bool) {
	t.Helper()
	required := testenv.Required(testenvName[name])
	switch name {
	case SQLite:
		prog := os.Getenv("BARESQL_TEST_SQLITE")
		if prog == "" {
			p, err := FindSQLite("")
			if err != nil {
				if required {
					t.Fatalf("no sqlite3 program, and BARESQL_REQUIRE_ENGINES requires sqlite")
				}
				return opener{}, false
			}
			prog = p
		}
		return opener{SQLite, func(ctx context.Context) (Engine, error) { return OpenSQLite(ctx, prog, "") }}, true
	case Postgres:
		dsn := os.Getenv("BARESQL_TEST_PG")
		if dsn == "" {
			if required {
				t.Fatalf("BARESQL_TEST_PG is not set, and BARESQL_REQUIRE_ENGINES requires pg")
			}
			return opener{}, false
		}
		cfg, err := pgwire.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		return opener{Postgres, func(ctx context.Context) (Engine, error) { return OpenPostgres(ctx, cfg) }}, true
	case MySQL, MariaDB:
		v := "BARESQL_TEST_MYSQL"
		if name == MariaDB {
			v = "BARESQL_TEST_MARIADB"
		}
		dsn := os.Getenv(v)
		if dsn == "" {
			if required {
				t.Fatalf("%s is not set, and BARESQL_REQUIRE_ENGINES requires %s", v, testenvName[name])
			}
			return opener{}, false
		}
		cfg, err := mysqlwire.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		return opener{name, func(ctx context.Context) (Engine, error) { return OpenMySQL(ctx, cfg, name) }}, true
	}
	t.Fatalf("unknown engine %s", name)
	return opener{}, false
}

// openFresh opens one engine of each opener, closed when the test ends.
func openFresh(t *testing.T, ops []opener) []Engine {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out []Engine
	for _, o := range ops {
		e, err := o.open(ctx)
		if err != nil {
			t.Fatalf("opening %s: %v", o.name, err)
		}
		t.Cleanup(func() { e.Close() })
		out = append(out, e)
	}
	return out
}
