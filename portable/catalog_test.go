package portable

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
				if _, err := eng.Run(ctx, "DROP TABLE IF EXISTS t"); err != nil {
					eng.Close()
					return nil, err
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
			check := func(what string, m map[string]Recorded, a *Answer) {
				got := Record(a, e.Columns)
				want, ok := Pick(m, eng, run.version)
				if !ok {
					t.Errorf("%s %s: nothing recorded for %s (run BARESQL_RECORD=1)", e.Code, what, eng)
					return
				}
				if !want.Same(got) {
					js, _ := json.Marshal(got)
					t.Errorf("%s %s on %s %s: recorded %q, got %q; to accept it, add \"%s %s\": %s",
						e.Code, what, eng, run.version, want.String(), got.String(), eng, majorMinor(run.version), js)
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

func lastStatement(src string) sqltext.Statement {
	st := sqltext.Split(src)
	if len(st) == 0 {
		return sqltext.Statement{}
	}
	return st[len(st)-1]
}

func majorMinor(v string) string {
	p := strings.SplitN(v, ".", 3)
	if len(p) >= 2 {
		return p[0] + "." + p[1]
	}
	return v
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
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("answers.json", append(b, '\n'), 0o644); err != nil {
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
	}
	if strings.Join(order, " ") != strings.Join(RuleCodes(), " ") {
		t.Errorf("rules and catalog are in different orders")
	}
	if e := Lookup("operator-not-everywhere"); e != nil {
		for _, it := range e.Items {
			if operatorMatch[it.Name] == nil {
				t.Errorf("operator %s has no matcher", it.Name)
			}
			found := false
			for _, p := range LintScript("probe.sql", it.Probe) {
				if p.Code == e.Code {
					found = true
				}
			}
			if !found {
				t.Errorf("operator %s: lint does not flag its probe", it.Name)
			}
		}
	}
	for _, code := range []string{"function-not-everywhere", "type-not-everywhere"} {
		e := Lookup(code)
		for _, it := range e.Items {
			found := false
			for _, p := range LintScript("probe.sql", it.Setup+";\n"+it.Probe) {
				if p.Code == code {
					found = true
				}
			}
			if !found {
				t.Errorf("%s %s: lint does not flag its probe", code, it.Name)
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
