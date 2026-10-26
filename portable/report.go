package portable

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/softdevstudio/baresql/internal/sqltext"
)

// WriteProblems prints lint problems: a file:line:col line editors can
// jump to, the SQL it is about, and what to write instead.
func WriteProblems(w io.Writer, ps []Problem) {
	for _, p := range ps {
		fmt.Fprintf(w, "%s:%d:%d: %s: %s [%s]\n", p.File, p.Line, p.Col, p.Severity, p.Message, p.Code)
		if p.Text != "" {
			fmt.Fprintf(w, "    %s\n", p.Text)
		}
		if p.Advice != "" {
			fmt.Fprintf(w, "    %s\n", p.Advice)
		}
	}
}

// LintSummary is one line: "3 problems: 1 error, 2 warnings".
func LintSummary(ps []Problem, hidden int) string {
	n := map[string]int{}
	for _, p := range ps {
		n[p.Severity]++
	}
	var parts []string
	for _, s := range []string{"error", "warning", "info"} {
		if n[s] > 0 {
			label := s
			if s == "info" {
				label = "note"
			}
			if n[s] > 1 {
				label += "s"
			}
			parts = append(parts, fmt.Sprintf("%d %s", n[s], label))
		}
	}
	out := "No problems found."
	if len(ps) > 0 {
		out = fmt.Sprintf("%d %s: %s.", len(ps), map[bool]string{true: "problem", false: "problems"}[len(ps) == 1], strings.Join(parts, ", "))
	}
	if hidden > 0 {
		out += fmt.Sprintf(" %d %s not shown (differences that depend on the data); --all shows them.", hidden, map[bool]string{true: "note", false: "notes"}[hidden == 1])
	}
	return out
}

// FilterEngines keeps the problems that matter when the SQL has to run on
// the given engines only: those whose recorded example (or table row)
// answers differently among them. Problems without recorded answers stay.
func FilterEngines(ps []Problem, engines []string) []Problem {
	if len(engines) == 0 {
		return ps
	}
	var out []Problem
	for _, p := range ps {
		rec := RecordedAnswers(p.Code)
		m := rec.Example
		if p.Item != "" && rec.Items[p.Item] != nil {
			m = rec.Items[p.Item]
		}
		var first *Recorded
		differs, known := false, true
		for _, eng := range engines {
			r, ok := m[eng]
			if !ok {
				known = false
				break
			}
			if first == nil {
				first = &r
			} else if !first.SameMeaning(r) {
				differs = true
			}
		}
		if differs || !known {
			out = append(out, p)
		}
	}
	return out
}

// WriteComparison prints the statements whose answers differ (all of
// them with all set), then a summary.
func WriteComparison(w io.Writer, cmp *Comparison, all bool) {
	var labels []string
	for _, e := range cmp.Engines {
		labels = append(labels, EngineTitle(e.Name)+" "+cleanVersion(e.Version))
	}
	fmt.Fprintf(w, "Compared on %s.\n", strings.Join(labels, ", "))
	rules := false
	for _, sr := range cmp.Statements {
		failed := !sr.Differs() && sr.Skipped == "" && allRefused(sr)
		if !all && !sr.Differs() && !failed {
			continue
		}
		fmt.Fprintf(w, "\n%s:%d  %s\n", sr.File, sr.Line, oneLineSQL(sr.SQL, 100))
		switch {
		case sr.Skipped != "":
			fmt.Fprintf(w, "  skipped: %s\n", sr.Skipped)
			continue
		case failed:
			fmt.Fprintf(w, "  refused by every engine\n")
		case !sr.Differs():
			fmt.Fprintf(w, "  the same everywhere\n")
			continue
		}
		for _, d := range sr.Differences {
			if d != "different values" {
				fmt.Fprintf(w, "  %s\n", d)
			}
		}
		groups := sr.Groups
		if failed {
			groups = [][]string{nil}
			for _, e := range cmp.Engines {
				groups[0] = append(groups[0], e.Name)
			}
		}
		for _, g := range groups {
			// Engines that refused alike may still say different things.
			if a := sr.Answers[g[0]]; a.Err != "" && len(g) > 1 {
				for _, n := range g {
					writeAnswer(w, EngineTitle(n), sr.Answers[n], sr.Query)
				}
				continue
			}
			writeAnswer(w, titles(g), sr.Answers[g[0]], sr.Query)
		}
		for _, e := range cmp.Engines {
			if a := sr.Answers[e.Name]; a != nil && len(a.Notes) > 0 && sr.Differs() {
				notes := dedup(a.Notes)
				more := ""
				if len(notes) > 3 {
					more = fmt.Sprintf(" (and %d more)", len(notes)-3)
					notes = notes[:3]
				}
				for i := range notes {
					notes[i] = oneLine(notes[i], 100)
				}
				fmt.Fprintf(w, "  %s warned: %s%s\n", EngineTitle(e.Name), strings.Join(notes, "; "), more)
			}
		}
		if len(sr.Rules) > 0 {
			fmt.Fprintf(w, "  lint: %s\n", strings.Join(sr.Rules, ", "))
			rules = true
		}
	}
	s := cmp.Summary
	fmt.Fprintf(w, "\n%s: %d answered alike, %d differ", count(s.Statements, "statement"), s.Same, s.Differ)
	if s.Failed > 0 {
		fmt.Fprintf(w, ", %d refused by every engine", s.Failed)
	}
	if s.Skipped > 0 {
		fmt.Fprintf(w, ", %d skipped", s.Skipped)
	}
	fmt.Fprintln(w, ".")
	if rules {
		fmt.Fprintln(w, "baresql rules <code> explains each lint code above.")
	}
}

func allRefused(sr StatementResult) bool {
	if len(sr.Answers) == 0 {
		return false
	}
	for _, a := range sr.Answers {
		if a.Err == "" {
			return false
		}
	}
	return true
}

// writeAnswer prints one group's answer: inline when it is one value,
// one row per line otherwise, at most ten rows.
func writeAnswer(w io.Writer, label string, a *Answer, query bool) {
	if a.Err != "" {
		fmt.Fprintf(w, "  %s: error: %s\n", label, oneLine(a.Err, 160))
		return
	}
	rows := Record(a, false).Rows
	switch {
	case a.Columns == nil && len(rows) == 0 && !query:
		fmt.Fprintf(w, "  %s: done\n", label)
	case len(rows) == 0:
		fmt.Fprintf(w, "  %s: no rows\n", label)
	case len(rows) == 1:
		fmt.Fprintf(w, "  %s: %s\n", label, oneLine(rows[0], 160))
	default:
		fmt.Fprintf(w, "  %s: %d rows\n", label, len(rows))
		for i, r := range rows {
			if i == 10 {
				fmt.Fprintf(w, "      ... %d more\n", len(rows)-10)
				break
			}
			fmt.Fprintf(w, "      %s\n", oneLine(r, 160))
		}
	}
}

// oneLine puts text on one line, cut to n characters.
// oneLineSQL is oneLine for a statement: its -- and # comments become
// /* ... */ first, so that joining the lines can't seem to comment out the
// rest of the statement.
func oneLineSQL(s string, n int) string {
	var b strings.Builder
	for _, t := range sqltext.Tokenize(s) {
		if t.Kind == sqltext.Comment && !strings.HasPrefix(t.Text, "/*") {
			text := strings.TrimSpace(strings.TrimLeft(t.Text, "-#"))
			b.WriteString("/* " + strings.ReplaceAll(text, "*/", "* /") + " */")
			continue
		}
		b.WriteString(t.Text)
	}
	return oneLine(b.String(), n)
}

func count(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n-3]) + "..."
	}
	return s
}

// WriteCatalog lists the rules.
func WriteCatalog(w io.Writer) {
	fmt.Fprintf(w, "BareSQL Portable knows %d differences between SQLite, PostgreSQL, MySQL and MariaDB.\n", len(catalog))
	if on := recordedLabels(); on != "" {
		fmt.Fprintf(w, "Answers recorded on %s.\n", on)
	}
	fmt.Fprintln(w)
	width := 0
	for _, e := range catalog {
		if len(e.Code) > width {
			width = len(e.Code)
		}
	}
	for _, e := range catalog {
		fmt.Fprintf(w, "  %-*s  %-7s  %s\n", width, e.Code, e.Severity, e.Title)
	}
	fmt.Fprintln(w, "\nbaresql rules <code> shows the example, each engine's answer and what to write instead.")
}

func recordedLabels() string {
	var out []string
	for _, eng := range EngineOrder {
		if v := recording.Engines[eng]; v != "" {
			out = append(out, EngineTitle(eng)+" "+v)
		}
	}
	return titlesOf(out)
}

func titlesOf(t []string) string {
	switch len(t) {
	case 0:
		return ""
	case 1:
		return t[0]
	}
	return strings.Join(t[:len(t)-1], ", ") + " and " + t[len(t)-1]
}

// WriteEntry prints one rule in full.
func WriteEntry(w io.Writer, e *Entry) {
	fmt.Fprintf(w, "%s (%s): %s\n\n", e.Code, e.Severity, e.Title)
	fmt.Fprintln(w, wrap(e.Summary, 78, ""))
	fmt.Fprintln(w)
	fmt.Fprintln(w, wrap(e.Detail, 78, ""))
	rec := RecordedAnswers(e.Code)
	if e.Setup != "" {
		fmt.Fprintf(w, "\nGiven:\n%s\n", indent(e.Setup, "    "))
	}
	fmt.Fprintf(w, "\nExample:\n%s\n\n", indent(e.Example, "    "))
	writeRecorded(w, rec.Example)
	if e.Rewrite != "" {
		fmt.Fprintf(w, "\nThe same on every engine:\n%s\n\n", indent(e.Rewrite, "    "))
		writeRecorded(w, rec.Rewrite)
	}
	fmt.Fprintf(w, "\n%s\n", wrap(e.Advice, 78, ""))
	if len(e.Items) > 0 {
		writeItems(w, e, rec)
	}
	if since := sinceNote(e.Since); since != "" {
		fmt.Fprintf(w, "\n%s\n", since)
	}
	for _, eng := range EngineOrder {
		if f := e.Requires[eng]; f != "" {
			fmt.Fprintf(w, "\nThe %s answers assume a build with %s.\n", EngineTitle(eng), featureNames[f])
		}
	}
}

// writeItems prints an entry's rows: "no" where an engine refuses, "yes"
// where it runs, or each engine's answer when those that run it answer
// differently.
func writeItems(w io.Writer, e *Entry, rec *Answers) {
	rows := [][]string{{""}}
	for _, eng := range EngineOrder {
		rows[0] = append(rows[0], EngineTitle(eng))
	}
	for _, it := range e.Items {
		m := rec.Items[it.Name]
		var first *Recorded
		values := false
		for _, eng := range EngineOrder {
			if r, ok := m[eng]; ok && r.Error == "" {
				if first == nil {
					first = &r
				} else if !first.SameMeaning(r) {
					values = true
				}
			}
		}
		row := []string{it.Name}
		for _, eng := range EngineOrder {
			r, ok := m[eng]
			switch {
			case !ok:
				row = append(row, "?")
			case r.Error != "":
				row = append(row, "no")
			case values:
				row = append(row, oneLine(r.String(), 18))
			default:
				row = append(row, "yes")
			}
		}
		advice := it.Advice
		notes := VersionNotes(m, it.Since, it.Requires)
		for _, eng := range EngineOrder {
			if n := notes[eng]; len(n) > 0 {
				advice += " The " + EngineTitle(eng) + " answer holds " + strings.Join(n, ", ") + "."
			}
		}
		rows = append(rows, append(row, strings.TrimSpace(advice)))
	}
	widths := make([]int, 5)
	for _, row := range rows {
		for i := 0; i < 5; i++ {
			widths[i] = max(widths[i], len([]rune(row[i])))
		}
	}
	fmt.Fprintln(w)
	for _, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			if i == len(row)-1 && i >= 5 {
				b.WriteString(cell)
				break
			}
			b.WriteString(cell + strings.Repeat(" ", widths[i]-len([]rune(cell))+1))
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}
}

func sinceNote(since map[string]string) string {
	var parts []string
	for _, eng := range EngineOrder {
		if v := since[eng]; v != "" {
			parts = append(parts, EngineTitle(eng)+" "+strings.TrimSuffix(v, ".0"))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "These answers need " + titlesOf(parts) + " or later."
}

func writeRecorded(w io.Writer, m map[string]Recorded) {
	for _, eng := range EngineOrder {
		r, ok := m[eng]
		if !ok {
			continue
		}
		label := EngineTitle(eng) + " " + recording.Engines[eng]
		fmt.Fprintf(w, "    %-20s %s\n", label, oneLine(r.String(), 120))
	}
	var extra []string
	for k := range m {
		if strings.Contains(k, " ") {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		eng, v, _ := strings.Cut(k, " ")
		label := EngineTitle(eng) + " " + v
		if from, ok := strings.CutPrefix(v, ">="); ok {
			label = EngineTitle(eng) + " " + from + " and later"
		}
		fmt.Fprintf(w, "    %-20s %s\n", label, oneLine(m[k].String(), 120))
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// wrap breaks text into lines of at most width characters.
func wrap(s string, width int, prefix string) string {
	var lines []string
	line := prefix
	for _, word := range strings.Fields(s) {
		if len([]rune(line))+len([]rune(word))+1 > width && strings.TrimSpace(line) != "" {
			lines = append(lines, line)
			line = prefix
		}
		if strings.TrimSpace(line) != "" {
			line += " "
		}
		line += word
	}
	if strings.TrimSpace(line) != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// CatalogJSON is the catalog with its recorded answers, for --format json.
type CatalogJSON struct {
	RecordedOn map[string]string `json:"recorded_on"`
	Rules      []EntryJSON       `json:"rules"`
}

// EntryJSON is an entry with its answers.
type EntryJSON struct {
	*Entry
	Answers *Answers `json:"answers"`
}

// CatalogWithAnswers returns the catalog for JSON output.
func CatalogWithAnswers() CatalogJSON {
	out := CatalogJSON{RecordedOn: recording.Engines}
	for _, e := range catalog {
		out.Rules = append(out.Rules, EntryJSON{Entry: e, Answers: RecordedAnswers(e.Code)})
	}
	return out
}
