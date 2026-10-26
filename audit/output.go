package audit

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed report.html.tmpl
var reportTemplate string

// WriteFiles writes report.html and audit.json into dir.
func WriteFiles(dir string, res *Result) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	js, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "audit.json"), append(js, '\n'), 0o644); err != nil {
		return err
	}
	var html bytes.Buffer
	if err := WriteHTML(&html, res); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.html"), html.Bytes(), 0o644)
}

// ReadResult loads an audit.json written by WriteFiles.
func ReadResult(path string) (*Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var res Result
	if err := json.Unmarshal(b, &res); err != nil {
		return nil, fmt.Errorf("%s: not an audit.json from baresql: %w", path, err)
	}
	if res.Tool != "baresql" {
		return nil, fmt.Errorf("%s: not an audit.json from baresql", path)
	}
	return &res, nil
}

type reportView struct {
	R               *Result
	When            string
	Counts          map[string]int
	PersonalLogins  int
	PersonalColumns int
	Who             []whoRow
	Data            []dataRow
	Logins          []loginView
	Public          loginView
}

type whoRow struct {
	Login, Anchor             string
	Kinds                     []string
	Columns, Tables, Writable int
	Notes                     string
}

type dataRow struct {
	Column, Kind, Type, Confidence string
	Categories, Evidence, Readers  []string
	Derived                        bool
}

type loginView struct {
	L        LoginReport
	Anchor   string
	Personal []ColumnReach
	Other    []ColumnReach
}

func splitReach(l LoginReport) loginView {
	v := loginView{L: l, Anchor: anchor(l.Name)}
	for _, c := range l.Read {
		if c.Personal != nil {
			v.Personal = append(v.Personal, c)
		} else {
			v.Other = append(v.Other, c)
		}
	}
	return v
}

func anchor(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "-%x", r)
		}
	}
	return b.String()
}

func labels(cats []string) []string {
	var out []string
	for _, c := range cats {
		out = append(out, CategoryLabel(c))
	}
	return out
}

// WriteHTML renders the report.
func WriteHTML(w io.Writer, res *Result) error {
	v := reportView{R: res, Counts: map[string]int{"high": 0, "medium": 0, "low": 0}}
	if t, err := time.Parse(time.RFC3339, res.Generated); err == nil {
		v.When = t.UTC().Format("2 January 2006, 15:04 UTC")
	}
	for _, f := range res.Findings {
		v.Counts[f.Severity]++
	}
	readers := map[string][]string{} // relation.column -> logins
	for _, l := range res.Logins {
		lv := splitReach(l)
		v.Logins = append(v.Logins, lv)
		if l.Summary.PersonalColumns > 0 {
			v.PersonalLogins++
			var notes []string
			for _, a := range l.Attributes {
				if a == "superuser" || a == "bypassrls" {
					notes = append(notes, a)
				}
			}
			if needsSetRole(l) {
				notes = append(notes, "some only after SET ROLE")
			}
			v.Who = append(v.Who, whoRow{Login: l.Name, Anchor: lv.Anchor, Kinds: labels(l.Summary.Categories),
				Columns: l.Summary.PersonalColumns, Tables: l.Summary.PersonalTables,
				Writable: l.Summary.PersonalWritable, Notes: strings.Join(notes, ", ")})
		}
		for _, c := range l.Read {
			if c.Personal != nil {
				k := c.Relation + "." + quoteIdent(c.Column)
				readers[k] = append(readers[k], l.Name)
			}
		}
	}
	sort.SliceStable(v.Who, func(i, j int) bool { return v.Who[i].Columns > v.Who[j].Columns })
	v.Public = splitReach(res.Public)
	for _, rel := range res.Relations {
		for _, c := range rel.Columns {
			if c.Personal == nil {
				continue
			}
			if rel.Kind != "view" {
				v.PersonalColumns++
			}
			k := rel.Name + "." + quoteIdent(c.Name)
			v.Data = append(v.Data, dataRow{Column: k, Kind: rel.Kind, Type: c.Type, Confidence: c.Personal.Confidence,
				Categories: labels(c.Personal.Categories), Evidence: c.Personal.Evidence, Readers: dedupStrings(readers[k]),
				Derived: c.Personal.Derived})
		}
	}
	funcs := template.FuncMap{
		"upper":      strings.ToUpper,
		"route":      func(r Route) string { return r.String() },
		"capability": capabilityWords,
		"labels":     labels,
		"password":   passwordWords,
	}
	t, err := template.New("report").Funcs(funcs).Parse(reportTemplate)
	if err != nil {
		return err
	}
	return t.Execute(w, v)
}

func passwordWords(kind string) string {
	switch kind {
	case "none":
		return "no password set"
	case "md5":
		return "password stored as an MD5 hash"
	case "scram-sha-256":
		return "password stored as a SCRAM hash"
	}
	return "password stored in another form"
}

// needsSetRole reports whether some of the personal data a login reads is
// reachable only after SET ROLE.
func needsSetRole(l LoginReport) bool {
	for _, c := range l.Read {
		if c.Personal == nil {
			continue
		}
		direct := false
		for _, r := range c.Routes {
			if r.SetRole == "" {
				direct = true
			}
		}
		if !direct {
			return true
		}
	}
	return false
}

func dedupStrings(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// WriteSummary prints the short text the CLI shows after an audit.
func WriteSummary(w io.Writer, res *Result) {
	fmt.Fprintf(w, "BareSQL Audit of %s (PostgreSQL %s) as %s\n", res.Server.Database, res.Server.Version, res.Server.Auditor)
	personal := 0
	for _, l := range res.Logins {
		if l.Summary.PersonalColumns > 0 {
			personal++
		}
	}
	cols := 0
	for _, rel := range res.Relations {
		if rel.Kind == "view" {
			continue
		}
		for _, c := range rel.Columns {
			if c.Personal != nil {
				cols++
			}
		}
	}
	fmt.Fprintf(w, "%d logins, %d of them can read personal data; %d columns hold personal data.\n", len(res.Logins), personal, cols)
	counts := map[string]int{}
	for _, f := range res.Findings {
		counts[f.Severity]++
	}
	fmt.Fprintf(w, "Findings: %d high, %d medium, %d low\n", counts["high"], counts["medium"], counts["low"])
	for _, f := range res.Findings {
		fmt.Fprintf(w, "  %-6s %-28s %s\n", f.Severity, f.Code, f.Subject)
	}
	for _, n := range res.NotSeen {
		fmt.Fprintf(w, "Not seen: %s: %s\n", n.What, n.Why)
	}
	if len(res.Mismatches) > 0 {
		fmt.Fprintf(w, "BareSQL disagreed with the server %d times; see the report and please tell us.\n", len(res.Mismatches))
	}
}
