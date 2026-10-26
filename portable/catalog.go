package portable

import (
	"context"
	_ "embed"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/softdevstudio/baresql/internal/sqltext"
)

// The catalog is the list of differences BareSQL Portable knows. Each entry
// has a minimal example and, where one exists, a form that answers the same
// on every engine. catalog.json is written by hand; answers.json holds what
// the engines actually answered, recorded by running the catalog
// (BARESQL_RECORD=1 go test ./portable -run TestCatalog), and CI runs the
// catalog again on every engine version it tests.

//go:embed catalog.json
var catalogJSON []byte

//go:embed answers.json
var answersJSON []byte

// Entry is one difference between the engines.
type Entry struct {
	Code     string `json:"code"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
	// Summary is one sentence on what differs, Detail a paragraph on why,
	// Advice what to write instead.
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
	Advice  string `json:"advice"`
	// Setup runs before Example and before Rewrite. Each may hold several
	// statements; the answer is that of the last one, or the first refusal.
	Setup   string `json:"setup,omitempty"`
	Example string `json:"example"`
	// Rewrite answers the same everywhere. Empty when no form does.
	Rewrite string `json:"rewrite,omitempty"`
	// Columns: the column names are part of the answer.
	Columns bool `json:"columns,omitempty"`
	// Since names the oldest engine versions the example and rewrite
	// answers hold for, when that is newer than what BareSQL supports.
	Since map[string]string `json:"since,omitempty"`
	// Requires names a build feature the answers assume: "math" (SQLite's
	// math functions, which some builds leave out) or "ascii-case" (SQLite
	// without ICU, which folds case for A to Z only).
	Requires map[string]string `json:"requires,omitempty"`
	// Items are the functions, operators or types of a table entry, each
	// with a statement that uses it.
	Items []Item `json:"items,omitempty"`
}

// Item is one row of a table entry.
type Item struct {
	Name     string            `json:"name"`
	Setup    string            `json:"setup,omitempty"`
	Probe    string            `json:"probe"`
	Advice   string            `json:"advice"`
	Since    map[string]string `json:"since,omitempty"`
	Requires map[string]string `json:"requires,omitempty"`
}

// Recorded is an engine's answer as kept in answers.json: the rows as
// SQL-style values ('text', 12, NULL), or the refusal's message. Refusals
// count as refusals whatever the message says; messages change between
// versions.
type Recorded struct {
	Columns []string `json:"columns,omitempty"`
	Rows    []string `json:"rows,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// Answers are the recorded answers of one entry, per engine. A key is an
// engine name ("mysql"), an engine and a version prefix ("postgres 13")
// for one version line that answers differently, or an engine and a
// lowest version ("mysql >=9.7") for versions from then on.
type Answers struct {
	Example map[string]Recorded            `json:"example,omitempty"`
	Rewrite map[string]Recorded            `json:"rewrite,omitempty"`
	Items   map[string]map[string]Recorded `json:"items,omitempty"`
}

// Recording is answers.json.
type Recording struct {
	// Engines are the versions the answers were recorded on.
	Engines map[string]string   `json:"engines"`
	Entries map[string]*Answers `json:"entries"`
}

var (
	catalog   []*Entry
	byCode    = map[string]*Entry{}
	recording Recording
)

func init() {
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic("portable: catalog.json: " + err.Error())
	}
	for _, e := range catalog {
		if byCode[e.Code] != nil {
			panic("portable: catalog.json: two entries " + e.Code)
		}
		byCode[e.Code] = e
	}
	if err := json.Unmarshal(answersJSON, &recording); err != nil {
		panic("portable: answers.json: " + err.Error())
	}
	if recording.Entries == nil {
		recording.Entries = map[string]*Answers{}
	}
}

// Catalog returns every entry, in catalog order.
func Catalog() []*Entry { return catalog }

// Lookup returns the entry with the given code, or nil.
func Lookup(code string) *Entry { return byCode[code] }

func entry(code string) *Entry { return byCode[code] }

// RecordedOn returns the engine versions the answers were recorded on.
func RecordedOn() map[string]string { return recording.Engines }

// RecordedAnswers returns the recorded answers of an entry.
func RecordedAnswers(code string) *Answers {
	if a := recording.Entries[code]; a != nil {
		return a
	}
	return &Answers{}
}

// Pick returns the answer recorded for an engine at a version: the most
// specific version prefix that matches ("mariadb 11.8", then "mariadb
// 11"), then the newest "engine >=x.y" the version reaches, then the plain
// engine name.
func Pick(m map[string]Recorded, engine, version string) (Recorded, bool) {
	v := cleanVersion(version)
	for p := v; p != ""; {
		if r, ok := m[engine+" "+p]; ok {
			return r, true
		}
		i := strings.LastIndexByte(p, '.')
		if i < 0 {
			break
		}
		p = p[:i]
	}
	best := ""
	for k := range m {
		if from, ok := strings.CutPrefix(k, engine+" >="); ok && v != "" && !versionLess(v, from) {
			if best == "" || versionLess(best, from) {
				best = from
			}
		}
	}
	if best != "" {
		return m[engine+" >="+best], true
	}
	r, ok := m[engine]
	return r, ok
}

// VersionNotes says, per engine, how an item's answer changes with the
// engine's version or build: "from 3.44" (since), "before 9.6" (an
// "engine >=9.6" answer that refuses where the plain one works, or the
// other way round), "except 8.0" (an "engine 8.0" answer that does so),
// "with math functions" (a build feature the answer needs).
func VersionNotes(m map[string]Recorded, since, requires map[string]string) map[string][]string {
	out := map[string][]string{}
	for _, eng := range EngineOrder {
		base, found := m[eng]
		if !found {
			continue
		}
		works := base.Error == ""
		if v := since[eng]; v != "" && works {
			out[eng] = append(out[eng], "from "+strings.TrimSuffix(v, ".0"))
		}
		if f := requires[eng]; f != "" && works {
			out[eng] = append(out[eng], "with "+featureNames[f])
		}
		var keys []string
		for k := range m {
			if strings.HasPrefix(k, eng+" ") {
				keys = append(keys, strings.TrimPrefix(k, eng+" "))
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			return versionLess(strings.TrimPrefix(keys[i], ">="), strings.TrimPrefix(keys[j], ">="))
		})
		for _, k := range keys {
			if (m[eng+" "+k].Error == "") == works {
				continue
			}
			if v, ok := strings.CutPrefix(k, ">="); ok {
				out[eng] = append(out[eng], "before "+v)
			} else {
				out[eng] = append(out[eng], "except "+k)
			}
		}
	}
	return out
}

// featureNames are the build features an answer can need, as Requires
// names them.
var featureNames = map[string]string{
	"math":       "math functions",
	"ascii-case": "ASCII-only case folding",
}

// cleanVersion is the version number an engine reports, without what
// follows it: "16.15" of "16.15 (Ubuntu 16.15-1)", "11.8.3" of
// "11.8.3-MariaDB-ubu2404".
func cleanVersion(v string) string {
	i := 0
	for i < len(v) && (v[i] >= '0' && v[i] <= '9' || v[i] == '.') {
		i++
	}
	return strings.TrimSuffix(v[:i], ".")
}

// scratchRe matches the random names of compare's scratch databases and
// schemas, which engines put in some messages.
var scratchRe = regexp.MustCompile(`baresql_compare_[0-9a-f]{10}`)

// Record turns an answer into its recorded form.
func Record(a *Answer, columns bool) Recorded {
	if a.Err != "" {
		return Recorded{Error: scratchRe.ReplaceAllString(a.Err, "baresql_compare")}
	}
	r := Recorded{}
	if columns {
		r.Columns = append([]string(nil), a.Columns...)
	}
	for _, row := range a.Rows {
		var vals []string
		for _, c := range row {
			vals = append(vals, RenderCell(c))
		}
		r.Rows = append(r.Rows, strings.Join(vals, ", "))
	}
	return r
}

// Same reports whether two recorded answers are the same: equal rows (and
// columns), or both refusals.
func (r Recorded) Same(o Recorded) bool {
	if (r.Error != "") != (o.Error != "") {
		return false
	}
	if r.Error != "" {
		return true
	}
	return strings.Join(r.Rows, "\n") == strings.Join(o.Rows, "\n") && strings.Join(r.Columns, "\x00") == strings.Join(o.Columns, "\x00")
}

// String is one line: "3", "'ab'", "NULL; 1; 2" for three rows, or
// "error: division by zero".
func (r Recorded) String() string {
	if r.Error != "" {
		return "error: " + r.Error
	}
	s := strings.Join(r.Rows, "; ")
	if len(r.Rows) == 0 {
		s = "no rows"
	}
	if len(r.Columns) > 0 {
		s = "columns " + strings.Join(r.Columns, ", ") + ": " + s
	}
	return s
}

// RenderCell writes a value the way SQL would: text in quotes, numbers and
// dates as they are, NULL, true and false, X'..' for bytes.
func RenderCell(c Cell) string {
	if c.Null {
		return "NULL"
	}
	switch c.Kind {
	case KindText, KindJSON:
		return "'" + strings.ReplaceAll(c.Text, "'", "''") + "'"
	case KindBytes:
		return "X'" + strings.ToUpper(c.Text) + "'"
	case KindBool:
		switch strings.ToLower(c.Text) {
		case "t", "true", "1":
			return "true"
		case "f", "false", "0":
			return "false"
		}
	}
	return c.Text
}

// RunEntry runs an entry's setup and example (or rewrite) on one engine,
// which should be fresh, and returns the answer of the last statement or
// the first refusal.
func RunEntry(ctx context.Context, e Engine, setup, sql string) (*Answer, error) {
	return runScript(ctx, e, setup+"\n;\n"+sql)
}

func runScript(ctx context.Context, e Engine, src string) (*Answer, error) {
	var last *Answer
	for _, st := range sqltext.Split(src) {
		a, err := e.Run(ctx, st.Text)
		if err != nil {
			return nil, err
		}
		if a.Err != "" {
			return a, nil
		}
		last = a
	}
	if last == nil {
		return &Answer{}, nil
	}
	return last, nil
}

// engineList is "SQLite and PostgreSQL", in engine order.
func engineList(names []string) string {
	sorted := append([]string(nil), names...)
	rank := map[string]int{}
	for i, n := range EngineOrder {
		rank[n] = i
	}
	sort.SliceStable(sorted, func(i, j int) bool { return rank[sorted[i]] < rank[sorted[j]] })
	return titles(sorted)
}
