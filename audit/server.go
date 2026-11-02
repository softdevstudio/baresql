package audit

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/softdevstudio/baresql/internal/pgwire"
)

// runServer audits several databases of one server, each in its own
// read-only transaction over its own connection, and merges the results.
// Roles belong to the whole server, so each login appears once, with what
// it reaches in every database.
func runServer(ctx context.Context, cfg pgwire.Config, opt RunOptions) (*Result, error) {
	c, err := pgwire.Connect(ctx, cfg)
	if err != nil {
		return nil, err
	}
	res, err := c.Query(ctx, `SELECT datname FROM pg_catalog.pg_database
		WHERE datallowconn AND NOT datistemplate ORDER BY datname`)
	c.Close()
	if err != nil {
		return nil, fmt.Errorf("audit: listing the databases: %w", err)
	}
	var all []string
	for _, r := range res[0].Rows {
		all = append(all, r[0].String())
	}
	dbs := all
	if !opt.AllDatabases {
		dbs = nil
		for _, d := range opt.Databases {
			if !slices.Contains(all, d) {
				return nil, fmt.Errorf("audit: database %s does not exist or does not accept connections", quoteIdent(d))
			}
			if !slices.Contains(dbs, d) {
				dbs = append(dbs, d)
			}
		}
		sort.Strings(dbs)
	}
	if len(dbs) == 0 {
		return nil, fmt.Errorf("audit: the server has no database to audit")
	}
	var parts []*Result
	var skipped []NotSeen
	one := opt
	one.AllDatabases, one.Databases, one.prefix = false, nil, true
	for _, db := range dbs {
		dc := cfg
		dc.Database = db
		conn, err := pgwire.Connect(ctx, dc)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			skipped = append(skipped, NotSeen{What: "database " + quoteIdent(db), Why: "the auditor couldn't connect: " + CleanText(err.Error())})
			continue
		}
		r, err := RunOn(ctx, conn, one)
		conn.Close()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			skipped = append(skipped, NotSeen{What: "database " + quoteIdent(db), Why: "the audit failed: " + CleanText(err.Error())})
			continue
		}
		parts = append(parts, r)
	}
	if len(parts) == 0 {
		if len(skipped) > 0 {
			return nil, fmt.Errorf("audit: no database could be audited; %s: %s", skipped[0].What, skipped[0].Why)
		}
		return nil, fmt.Errorf("audit: no database could be audited")
	}
	out := Merge(parts)
	out.NotSeen = append(out.NotSeen, skipped...)
	out.Settings.Databases = dbs
	if opt.AllDatabases {
		out.Settings.AllDatabases = true
	}
	return out, nil
}

// Merge joins the audits of several databases of one server, each run
// with its objects named after its database, into one result.
func Merge(parts []*Result) *Result {
	out := &Result{Tool: parts[0].Tool, Version: parts[0].Version, Generated: parts[0].Generated,
		Server: parts[0].Server, Settings: parts[0].Settings}
	out.Server.Databases = nil
	out.Settings.Schemas = nil
	logins := map[string]*LoginReport{}
	var order []string
	public := &LoginReport{Name: "PUBLIC"}
	seenFinding := map[string]bool{}
	seenNotSeen := map[string]bool{}
	for _, p := range parts {
		db := p.Server.Database
		out.Server.Databases = append(out.Server.Databases, db)
		for _, s := range p.Settings.Schemas {
			out.Settings.Schemas = append(out.Settings.Schemas, quoteIdent(db)+"."+s)
		}
		out.Relations = append(out.Relations, p.Relations...)
		for _, l := range p.Logins {
			m := logins[l.Name]
			if m == nil {
				m = &LoginReport{Name: l.Name, Attributes: l.Attributes, MemberOf: l.MemberOf, CanSetRole: l.CanSetRole,
					Password: l.Password, oid: l.oid}
				logins[l.Name] = m
				order = append(order, l.Name)
			}
			mergeLogin(m, l, db)
		}
		mergeLogin(public, p.Public, db)
		if public.Attributes == nil {
			public.Attributes, public.MemberOf = p.Public.Attributes, p.Public.MemberOf
		}
		for _, d := range p.DefaultPrivileges {
			d.Database = db
			out.DefaultPrivileges = append(out.DefaultPrivileges, d)
		}
		for _, f := range p.Findings {
			k := f.Code + "\x00" + f.Subject + "\x00" + f.Message + "\x00" + strings.Join(f.Details, "\x00")
			if !seenFinding[k] {
				seenFinding[k] = true
				out.Findings = append(out.Findings, f)
			}
		}
		for _, n := range p.NotSeen {
			if !seenNotSeen[n.What+"\x00"+n.Why] {
				seenNotSeen[n.What+"\x00"+n.Why] = true
				out.NotSeen = append(out.NotSeen, n)
			}
		}
		for _, m := range p.Mismatches {
			out.Mismatches = append(out.Mismatches, quoteIdent(db)+": "+m)
		}
	}
	out.Server.Database = strings.Join(out.Server.Databases, ", ")
	sort.Strings(order)
	for _, name := range order {
		l := logins[name]
		finishLogin(l, len(parts))
		out.Logins = append(out.Logins, *l)
	}
	finishLogin(public, len(parts))
	out.Public = *public
	sort.Slice(out.Relations, func(i, j int) bool { return out.Relations[i].Name < out.Relations[j].Name })
	sort.SliceStable(out.Findings, func(i, j int) bool {
		a, b := out.Findings[i], out.Findings[j]
		if severityOrder[a.Severity] != severityOrder[b.Severity] {
			return severityOrder[a.Severity] < severityOrder[b.Severity]
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Subject < b.Subject
	})
	if out.Findings == nil {
		out.Findings = []Finding{}
	}
	if out.NotSeen == nil {
		out.NotSeen = []NotSeen{}
	}
	if out.Mismatches == nil {
		out.Mismatches = []string{}
	}
	if out.DefaultPrivileges == nil {
		out.DefaultPrivileges = []DefaultPriv{}
	}
	return out
}

// mergeLogin adds what a login reaches in database db to m.
func mergeLogin(m *LoginReport, l LoginReport, db string) {
	if l.CannotConnect {
		m.CannotConnectTo = append(m.CannotConnectTo, db)
	}
	m.Read = append(m.Read, l.Read...)
	m.Write = append(m.Write, l.Write...)
	m.Functions = append(m.Functions, l.Functions...)
	m.Mappings = append(m.Mappings, l.Mappings...)
	// Server capabilities belong to the whole server: the same in every
	// database, except for routes through functions, which are per
	// database.
	for _, s := range l.Server {
		i := slices.IndexFunc(m.Server, func(x ServerAccess) bool { return x.Capability == s.Capability })
		if i < 0 {
			m.Server = append(m.Server, ServerAccess{Capability: s.Capability, Routes: append([]Route{}, s.Routes...)})
			continue
		}
		for _, r := range s.Routes {
			if !slices.ContainsFunc(m.Server[i].Routes, func(x Route) bool { return x.String() == r.String() }) {
				m.Server[i].Routes = append(m.Server[i].Routes, r)
			}
		}
	}
	s := &m.Summary
	s.Relations += l.Summary.Relations
	s.Columns += l.Summary.Columns
	s.PersonalColumns += l.Summary.PersonalColumns
	s.PersonalTables += l.Summary.PersonalTables
	s.WritableTables += l.Summary.WritableTables
	s.PersonalWritable += l.Summary.PersonalWritable
	for _, c := range l.Summary.Categories {
		if !slices.Contains(s.Categories, c) {
			s.Categories = append(s.Categories, c)
		}
	}
}

// finishLogin sorts what was merged and keeps empty lists as [] in JSON.
func finishLogin(l *LoginReport, parts int) {
	l.CannotConnect = len(l.CannotConnectTo) == parts
	if l.CannotConnect {
		l.CannotConnectTo = nil
	}
	sort.Strings(l.Summary.Categories)
	if l.Summary.Categories == nil {
		l.Summary.Categories = []string{}
	}
	if l.Attributes == nil {
		l.Attributes = []string{}
	}
	if l.MemberOf == nil {
		l.MemberOf = []string{}
	}
	for _, p := range []*[]ColumnReach{&l.Read} {
		if *p == nil {
			*p = []ColumnReach{}
		}
	}
	if l.Write == nil {
		l.Write = []TableWrite{}
	}
	if l.Functions == nil {
		l.Functions = []FunctionAccess{}
	}
	if l.Server == nil {
		l.Server = []ServerAccess{}
	}
	if l.Mappings == nil {
		l.Mappings = []MappingAccess{}
	}
}
