package portable

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/softdevstudio/baresql/internal/sqltext"
)

// entryRun is one entry run on one engine.
type entryRun struct {
	engine, version string
	example         *Answer
	rewrite         *Answer
	items           map[string]*Answer
}

// runCatalogOn runs every entry on fresh engines of one kind.
func runCatalogOn(ctx context.Context, o opener) (map[string]*entryRun, error) {
	out := map[string]*entryRun{}
	for _, e := range Catalog() {
		run := &entryRun{engine: o.name, items: map[string]*Answer{}}
		once := func(sql string) (*Answer, error) {
			eng, err := o.open(ctx)
			if err != nil {
				return nil, err
			}
			defer eng.Close()
			run.version = cleanVersion(eng.Version())
			return RunEntry(ctx, eng, e.Setup, sql)
		}
		var err error
		if run.example, err = once(e.Example); err != nil {
			return nil, fmt.Errorf("%s on %s: %w", e.Code, o.name, err)
		}
		if e.Rewrite != "" {
			if run.rewrite, err = once(e.Rewrite); err != nil {
				return nil, fmt.Errorf("%s rewrite on %s: %w", e.Code, o.name, err)
			}
		}
		if len(e.Items) > 0 {
			eng, err := o.open(ctx)
			if err != nil {
				return nil, err
			}
			for _, it := range e.Items {
				a, err := RunEntry(ctx, eng, it.Setup, it.Probe)
				if err != nil {
					eng.Close()
					return nil, fmt.Errorf("%s item %s on %s: %w", e.Code, it.Name, o.name, err)
				}
				run.items[it.Name] = a
				// Rows share the engine: drop what their setups make.
				for _, drop := range []string{"DROP VIEW IF EXISTS v", "DROP TABLE IF EXISTS t", "DROP TABLE IF EXISTS u"} {
					if _, err := eng.Run(ctx, drop); err != nil {
						eng.Close()
						return nil, err
					}
				}
			}
			eng.Close()
		}
		out[e.Code] = run
	}
	return out, nil
}

// TestCatalog runs every catalog entry on every engine the environment
// offers and compares the answers with answers.json. BARESQL_RECORD=1, with
// all four engines, writes answers.json from the run instead.
func TestCatalog(t *testing.T) {
	ops := availableOpeners(t)
	record := os.Getenv("BARESQL_RECORD") != ""
	if record && len(ops) < len(EngineOrder) {
		t.Fatalf("recording needs all four engines, have %d", len(ops))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	runs := map[string]map[string]*entryRun{} // engine -> code -> run
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error
	features := map[string]map[string]bool{}
	for _, o := range ops {
		features[o.name] = engineFeatures(t, ctx, o)
	}
	for _, o := range ops {
		wg.Add(1)
		go func(o opener) {
			defer wg.Done()
			r, err := runCatalogOn(ctx, o)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			runs[o.name] = r
		}(o)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	if record {
		writeRecording(t, runs)
		return
	}
	for _, e := range Catalog() {
		rec := RecordedAnswers(e.Code)
		var rewrites []*Answer
		var rewriteNames []string
		var examples []*Answer
		for _, eng := range EngineOrder {
			run := runs[eng][e.Code]
			if run == nil {
				continue
			}
			if v := e.Since[eng]; v != "" && versionLess(run.version, v) {
				t.Logf("%s: %s %s is older than %s, skipped", e.Code, eng, run.version, v)
				continue
			}
			if f := e.Requires[eng]; f != "" && !features[eng][f] {
				t.Logf("%s: this %s lacks %s, skipped", e.Code, eng, f)
				continue
			}
			check := func(what string, m map[string]Recorded, a *Answer) {
				got := Record(a, e.Columns)
				want, ok := Pick(m, eng, run.version)
				if !ok {
					t.Errorf("%s %s: nothing recorded for %s (run BARESQL_RECORD=1)", e.Code, what, eng)
					return
				}
				if !want.Same(got) {
					js, _ := json.Marshal(got)
					t.Errorf("%s %s on %s %s: recorded %q, got %q; to accept it, add \"%s %s\" (this version line) or \"%s >=%s\" (from this version on): %s",
						e.Code, what, eng, run.version, want.String(), got.String(), eng, versionLine(eng, run.version), eng, versionLine(eng, run.version), js)
				}
			}
			check("example", rec.Example, run.example)
			examples = append(examples, run.example)
			if e.Rewrite != "" {
				check("rewrite", rec.Rewrite, run.rewrite)
				rewrites = append(rewrites, run.rewrite)
				rewriteNames = append(rewriteNames, eng)
			}
			for _, it := range e.Items {
				if v := it.Since[eng]; v != "" && versionLess(run.version, v) {
					continue
				}
				if f := it.Requires[eng]; f != "" && !features[eng][f] {
					continue
				}
				check("item "+it.Name, rec.Items[it.Name], run.items[it.Name])
			}
		}
		// The rewrite answers alike everywhere.
		if len(rewrites) > 1 {
			sorted := ordered(lastStatement(e.Rewrite))
			for i := 1; i < len(rewrites); i++ {
				if !answersEqual(rewrites[0], rewrites[i], sorted) {
					t.Errorf("%s: the rewrite answers %s on %s and %s on %s", e.Code,
						Record(rewrites[0], e.Columns), rewriteNames[0], Record(rewrites[i], e.Columns), rewriteNames[i])
				}
			}
		}
		// With all four engines, the example shows a difference.
		if len(examples) == len(EngineOrder) && !e.Columns {
			sorted := ordered(lastStatement(e.Example))
			same := true
			for i := 1; i < len(examples); i++ {
				if !answersEqual(examples[0], examples[i], sorted) {
					same = false
				}
			}
			if same {
				t.Errorf("%s: every engine answers the example alike", e.Code)
			}
		}
	}
}

// engineFeatures finds the build features of an engine that some answers
// depend on, by trying them.
func engineFeatures(t *testing.T, ctx context.Context, o opener) map[string]bool {
	out := map[string]bool{}
	if o.name != SQLite {
		return out
	}
	e, err := o.open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if a, err := e.Run(ctx, "SELECT power(2, 3)"); err == nil && a.Err == "" {
		out["math"] = true
	}
	if a, err := e.Run(ctx, "SELECT lower('É') = 'É'"); err == nil && a.Err == "" && len(a.Rows) == 1 && a.Rows[0][0].Text == "1" {
		out["ascii-case"] = true
	}
	opts := []string{}
	if a, err := e.Run(ctx, "PRAGMA compile_options"); err == nil {
		for _, r := range a.Rows {
			opts = append(opts, r[0].Text)
		}
	}
	t.Logf("sqlite %s: features %v; compile options %s", e.Version(), out, strings.Join(opts, " "))
	return out
}

// checkSeverity holds lint to its definitions: error when an engine
// refuses the statement, and not when every engine runs it.
func checkSeverity(t *testing.T, what, severity string, m map[string]Recorded) {
	t.Helper()
	refused := false
	for _, eng := range EngineOrder {
		if a, ok := m[eng]; ok && a.Error != "" {
			refused = true
		}
	}
	if len(m) == 0 {
		return
	}
	switch {
	case refused && severity != "error":
		t.Errorf("%s: an engine refuses it, and lint says %s", what, severity)
	case !refused && severity == "error":
		t.Errorf("%s: every engine runs it, and lint says error", what)
	}
}

func lastStatement(src string) sqltext.Statement {
	st := sqltext.Split(src)
	if len(st) == 0 {
		return sqltext.Statement{}
	}
	return st[len(st)-1]
}

// versionLine is the part of a version that names a release line: the
// major version of PostgreSQL (16), major.minor elsewhere (8.4, 11.8).
func versionLine(engine, v string) string {
	p := strings.SplitN(v, ".", 3)
	if engine == Postgres || len(p) < 2 {
		return p[0]
	}
	return p[0] + "." + p[1]
}

// writeRecording replaces the plain engine keys of answers.json with this
// run's answers; keys for particular versions ("mysql 9.7") stay.
func writeRecording(t *testing.T, runs map[string]map[string]*entryRun) {
	rec := Recording{Engines: map[string]string{}, Entries: map[string]*Answers{}}
	for code, a := range recording.Entries {
		rec.Entries[code] = a
	}
	for _, e := range Catalog() {
		a := rec.Entries[e.Code]
		if a == nil {
			a = &Answers{}
		}
		keep := func(m map[string]Recorded) map[string]Recorded {
			out := map[string]Recorded{}
			for k, v := range m {
				if strings.Contains(k, " ") {
					out[k] = v
				}
			}
			return out
		}
		a.Example = keep(a.Example)
		a.Rewrite = keep(a.Rewrite)
		items := map[string]map[string]Recorded{}
		for _, eng := range EngineOrder {
			run := runs[eng][e.Code]
			rec.Engines[eng] = run.version
			a.Example[eng] = Record(run.example, e.Columns)
			if e.Rewrite != "" {
				a.Rewrite[eng] = Record(run.rewrite, e.Columns)
			}
			for _, it := range e.Items {
				if items[it.Name] == nil {
					items[it.Name] = keep(a.Items[it.Name])
				}
				items[it.Name][eng] = Record(run.items[it.Name], false)
			}
		}
		if len(a.Rewrite) == 0 {
			a.Rewrite = nil
		}
		a.Items = nil
		if len(items) > 0 {
			a.Items = items
		}
		rec.Entries[e.Code] = a
	}
	// Entries no longer in the catalog go.
	for code := range rec.Entries {
		if Lookup(code) == nil {
			delete(rec.Entries, code)
		}
	}
	// Keys like "mysql >=9.6" are written by hand too, so ">" stays ">".
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("answers.json", b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote answers.json: %d entries", len(rec.Entries))
}

// TestCatalogRules: every lint rule has a catalog entry and every entry a
// rule, every operator row a matcher, and lint finds each example's
// problem and none in its rewrite.
func TestCatalogRules(t *testing.T) {
	codes := map[string]bool{}
	for _, c := range RuleCodes() {
		codes[c] = true
		if Lookup(c) == nil {
			t.Errorf("rule %s has no catalog entry", c)
		}
	}
	var order []string
	for _, e := range Catalog() {
		order = append(order, e.Code)
		if !codes[e.Code] {
			t.Errorf("catalog entry %s has no rule", e.Code)
		}
		if e.Title == "" || e.Summary == "" || e.Detail == "" || e.Advice == "" || e.Example == "" {
			t.Errorf("%s: missing text", e.Code)
		}
		switch e.Severity {
		case "error", "warning", "info":
		default:
			t.Errorf("%s: severity %q", e.Code, e.Severity)
		}
		for _, field := range []string{e.Title, e.Summary, e.Detail, e.Advice} {
			if strings.ContainsAny(field, "—–") {
				t.Errorf("%s: dash in %q", e.Code, field)
			}
		}
		found := false
		for _, p := range LintScript("example.sql", e.Setup+";\n"+e.Example) {
			if p.Code == e.Code {
				found = true
				checkSeverity(t, e.Code+" example", p.Severity, RecordedAnswers(e.Code).Example)
				// --engines judges a rule with rows by the row's answers.
				if len(e.Items) > 0 && e.item(p.Item) == nil {
					t.Errorf("%s: the example's problem names no row (%q)", e.Code, p.Item)
				}
			}
		}
		if !found {
			t.Errorf("%s: lint does not flag the example", e.Code)
		}
		if e.Rewrite != "" {
			for _, p := range LintScript("rewrite.sql", e.Rewrite) {
				if p.Code == e.Code {
					t.Errorf("%s: lint flags the rewrite: %s", e.Code, p.Text)
				}
			}
		}
		for _, it := range e.Items {
			for _, field := range []string{it.Advice, it.Message, it.Note} {
				if strings.ContainsAny(field, "—–") {
					t.Errorf("%s %s: dash in %q", e.Code, it.Name, field)
				}
			}
			switch it.Severity {
			case "", "error", "warning", "info":
			default:
				t.Errorf("%s %s: severity %q", e.Code, it.Name, it.Severity)
			}
			// Every row's probe is flagged, as that row.
			src := it.Probe
			if it.Setup != "" {
				src = it.Setup + ";\n" + it.Probe
			}
			found := false
			for _, p := range LintScript("probe.sql", src) {
				if p.Code == e.Code && p.Item == it.Name {
					found = true
					checkSeverity(t, e.Code+" "+it.Name, p.Severity, RecordedAnswers(e.Code).Items[it.Name])
				}
			}
			if !found {
				t.Errorf("%s %s: lint does not flag its probe as this row", e.Code, it.Name)
			}
		}
	}
	if strings.Join(order, " ") != strings.Join(RuleCodes(), " ") {
		t.Errorf("rules and catalog are in different orders")
	}
	if e := Lookup("operator-not-everywhere"); e != nil {
		for _, it := range e.Items {
			if operatorMatch[it.Name] == nil {
				t.Errorf("operator %s has no matcher", it.Name)
			}
		}
	}
	names := map[string]bool{}
	for _, e := range Catalog() {
		for _, it := range e.Items {
			if names[e.Code+it.Name] {
				t.Errorf("%s: two rows %s", e.Code, it.Name)
			}
			names[e.Code+it.Name] = true
		}
	}
	_ = sort.Strings
}

// TestFormNames checks the form names rules.go passes as literals.
func TestFormNames(t *testing.T) {
	src, err := os.ReadFile("rules.go")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range Catalog() {
		for _, it := range e.Items {
			names[it.Name] = true
		}
	}
	re := regexp.MustCompile("hitForm\\(r, [^,]+, [^,]+, (\"[^\"]*\"|`[^`]*`)")
	n := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		name := strings.Trim(m[1], "\"`")
		n++
		if !names[name] {
			t.Errorf("rules.go names a form %q the catalog lacks", name)
		}
	}
	if n < 40 {
		t.Errorf("found only %d form names in rules.go", n)
	}
}
