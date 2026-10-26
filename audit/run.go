// Package audit is BareSQL Audit: for every login of a PostgreSQL
// database, each column it can read, the route that gives it, and whether
// the column holds personal data.
//
// It only reads. Everything happens in one READ ONLY transaction that is
// rolled back at the end; it asks the server about privileges with
// has_table_privilege and friends, reads view definitions from
// pg_rewrite, and samples table values through their types' C output
// functions. It never calls a function written in SQL or a procedural
// language, never reads views or foreign tables, and never writes.
package audit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/softdevstudio/baresql"
	"github.com/softdevstudio/baresql/internal/pgwire"
)

// RunOptions control one audit.
type RunOptions struct {
	Schemas    []string // empty: every non-system schema
	Logins     []string // empty: every role that can log in
	Sample     bool     // read sample values (default on in the CLI)
	SampleRows int      // rows per table, default 200
	// StatementTimeout bounds each query; 0 means 60 seconds.
	StatementTimeout time.Duration
	// Now stamps the result; zero means time.Now (tests fix it).
	Now time.Time
}

// Run connects, audits and returns the result.
func Run(ctx context.Context, cfg pgwire.Config, opt RunOptions) (*Result, error) {
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["application_name"] = "baresql audit"
	c, err := pgwire.Connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return RunOn(ctx, c, opt)
}

// RunOn audits over an open connection, which it leaves idle afterwards.
func RunOn(ctx context.Context, c *pgwire.Conn, opt RunOptions) (*Result, error) {
	if opt.SampleRows <= 0 {
		opt.SampleRows = 200
	}
	timeout := opt.StatementTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if err := c.Exec(ctx, fmt.Sprintf(`BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
		SET LOCAL statement_timeout = %d;
		SET LOCAL lock_timeout = 5000;
		SET LOCAL idle_in_transaction_session_timeout = 0;
		SET LOCAL search_path = pg_catalog, pg_temp;
		SET LOCAL row_security = off`, timeout.Milliseconds())); err != nil {
		return nil, fmt.Errorf("audit: starting the read-only transaction: %w", err)
	}
	defer c.Exec(context.Background(), "ROLLBACK")
	ro, err := c.Query(ctx, "SHOW transaction_read_only")
	if err != nil || len(ro) != 1 || len(ro[0].Rows) != 1 || ro[0].Rows[0][0].String() != "on" {
		return nil, errors.New("audit: the transaction is not read-only; refusing to go on")
	}

	snap, err := Load(ctx, c, Options{Schemas: opt.Schemas})
	if err != nil {
		return nil, err
	}
	a := &analysis{ctx: ctx, c: c, s: snap, evals: map[uint32]map[uint32]map[string]*grantSet{},
		personal: map[colRef]*Personal{}, sampled: map[uint32]bool{}}

	logins, err := pickLogins(snap, opt.Logins)
	if err != nil {
		return nil, err
	}
	subjects := a.subjects(logins)
	if auditor := snap.RoleByName[snap.Auditor.Role]; auditor != nil && !auditor.Super {
		// Sampling reads only what the auditor's own login may read.
		subjects = appendUnique(subjects, auditor.OID)
		a.auditor = auditor.OID
	}
	truth, err := serverTruth(ctx, c, snap, subjects)
	if err != nil {
		return nil, err
	}
	for _, sub := range subjects {
		e := snap.rightsOf(sub).evaluate()
		a.mismatches = append(a.mismatches, reconcile(snap, sub, e, truth)...)
		a.evals[sub] = e
	}
	if err := a.functionTruth(subjects); err != nil {
		return nil, err
	}
	if opt.Sample {
		a.sample(opt.SampleRows)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	} else {
		snap.NotSeen = append(snap.NotSeen, NotSeen{What: "table values", Why: "sampling was turned off (--no-sample); personal data is found by column names only"})
	}
	a.classifyRest()
	for _, rel := range snap.Relations {
		if rel.LineageErr != "" {
			snap.NotSeen = append(snap.NotSeen, NotSeen{What: "column sources of view " + snap.QualifiedName(rel),
				Why: "its stored query couldn't be read: " + rel.LineageErr})
		}
	}

	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	res := &Result{
		Tool: "baresql", Version: baresql.Version, Generated: now.UTC().Format(time.RFC3339),
		Server: ServerReport{Version: snap.Server.Version, VersionNum: snap.Server.VersionNum, Database: snap.Server.Database,
			Target: c.Config().Redacted(), TLS: c.TLSActive(), Auth: c.AuthMethod(),
			Auditor: snap.Auditor.Role, Superuser: snap.Auditor.Superuser},
		Settings: Settings{Sampled: opt.Sample, SampleRows: opt.SampleRows, Logins: opt.Logins},
	}
	if !opt.Sample {
		res.Settings.SampleRows = 0
	}
	for _, sc := range snap.Schemas {
		if sc.Audited {
			res.Settings.Schemas = append(res.Settings.Schemas, sc.Name)
		}
	}
	sort.Strings(res.Settings.Schemas)
	res.Relations = a.relationReports()
	for _, l := range logins {
		res.Logins = append(res.Logins, a.loginReport(l))
	}
	res.Public = a.publicReport()
	res.DefaultPrivileges = a.defaultPrivileges()
	if !snap.Auditor.Superuser {
		snap.NotSeen = append(snap.NotSeen, NotSeen{What: "other databases",
			Why: "roles are shared by the whole server, but tables are audited one database at a time"})
	}
	sort.Strings(a.mismatches)
	res.Mismatches = a.mismatches
	if res.Mismatches == nil {
		res.Mismatches = []string{}
	}
	res.NotSeen = snap.NotSeen
	if res.NotSeen == nil {
		res.NotSeen = []NotSeen{}
	}
	res.Findings = a.findings(res)
	if res.Findings == nil {
		res.Findings = []Finding{}
	}
	return res, nil
}

func appendUnique(xs []uint32, x uint32) []uint32 {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

func pickLogins(s *Snapshot, names []string) ([]*Role, error) {
	var out []*Role
	if len(names) == 0 {
		for _, r := range s.Roles {
			if r.CanLogin && !r.Predefined {
				out = append(out, r)
			}
		}
	} else {
		for _, n := range names {
			r := s.RoleByName[n]
			if r == nil {
				return nil, fmt.Errorf("audit: role %q does not exist", n)
			}
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
