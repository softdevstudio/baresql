package audit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/softdevstudio/baresql/internal/pgwire"
	"github.com/softdevstudio/baresql/internal/testenv"
)

// TestScale times an audit of 1,000 tables of 10 columns and 50 logins,
// with sampling. It runs only when BARESQL_SCALE is set (it takes a while),
// and writes its timing to BARESQL_RECORD_DIR when that is set.
func TestScale(t *testing.T) {
	if os.Getenv("BARESQL_SCALE") == "" {
		t.Skip("set BARESQL_SCALE=1 to run the 1,000-table timing")
	}
	base, err := pgwire.ParseDSN(testenv.DSN(t, testenv.PG))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	admin, err := pgwire.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	const tables, logins = 1000, 50
	admin.Exec(ctx, "DROP DATABASE IF EXISTS baresql_fx_scale WITH (FORCE)")
	dropFixtureRoles(t, admin, "scale")
	var roles strings.Builder
	roles.WriteString("CREATE ROLE scale_owner NOLOGIN; CREATE ROLE scale_dbowner NOLOGIN;")
	for g := 0; g < 10; g++ {
		fmt.Fprintf(&roles, "CREATE ROLE scale_group%d NOLOGIN;", g)
	}
	for i := 0; i < logins; i++ {
		fmt.Fprintf(&roles, "CREATE ROLE scale_login%02d LOGIN IN ROLE scale_group%d;", i, i%10)
	}
	if err := admin.Exec(ctx, roles.String()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgwire.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close()
		c.Exec(context.Background(), "DROP DATABASE IF EXISTS baresql_fx_scale WITH (FORCE)")
		dropFixtureRoles(t, c, "scale")
	})
	if err := admin.Exec(ctx, "CREATE DATABASE baresql_fx_scale OWNER scale_dbowner"); err != nil {
		t.Fatal(err)
	}
	cfg := base
	cfg.Database = "baresql_fx_scale"
	db, err := pgwire.Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup := `CREATE SCHEMA app AUTHORIZATION scale_owner;
		GRANT USAGE ON SCHEMA app TO PUBLIC;
		SET ROLE scale_owner;
		DO $$
		DECLARE i int;
		BEGIN
		  FOR i IN 1..` + strconv.Itoa(tables) + ` LOOP
		    EXECUTE format('CREATE TABLE app.t%s (id int, email text, phone text, note text, a int, b int, c text, d date, e numeric, f boolean)', i);
		    EXECUTE format('INSERT INTO app.t%s SELECT g, ''u'' || g || ''@example.com'', ''+1 555 010 '' || g, ''n'', g, g, ''x'', now()::date, 1.5, true FROM generate_series(1, 20) g', i);
		    EXECUTE format('GRANT SELECT ON app.t%s TO scale_group%s', i, i % 10);
		    IF i % 7 = 0 THEN
		      EXECUTE format('GRANT SELECT (id, note) ON app.t%s TO scale_login%s', i, lpad((i % 50)::text, 2, '0'));
		    END IF;
		  END LOOP;
		END $$;
		RESET ROLE`
	start := time.Now()
	if err := db.Exec(ctx, setup); err != nil {
		t.Fatal(err)
	}
	t.Logf("setup took %v", time.Since(start).Round(time.Millisecond))
	start = time.Now()
	res, err := Run(ctx, cfg, RunOptions{Sample: true, Logins: scaleLogins(logins)})
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if len(res.Mismatches) > 0 {
		t.Fatalf("%d mismatches, first: %s", len(res.Mismatches), res.Mismatches[0])
	}
	reads := 0
	for _, l := range res.Logins {
		reads += len(l.Read)
	}
	line := fmt.Sprintf("%d tables of 10 columns, %d logins, values sampled: the audit took %.1f seconds and found %d readable login-columns (PostgreSQL %s, %d CPUs, %s/%s)",
		tables, logins, took.Seconds(), reads, res.Server.Version, runtime.NumCPU(), runtime.GOOS, runtime.GOARCH)
	t.Log(line)
	if dir := os.Getenv("BARESQL_RECORD_DIR"); dir != "" {
		os.MkdirAll(filepath.Join(dir, "audit"), 0o755)
		os.WriteFile(filepath.Join(dir, "audit", "scale.txt"), []byte(line+"\n"), 0o644)
	}
}

func scaleLogins(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("scale_login%02d", i))
	}
	return out
}
