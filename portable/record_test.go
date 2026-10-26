package portable

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestRecordResults writes the numbers the site quotes about the catalog
// to $BARESQL_RECORD_DIR/portable/counts.txt (scripts/record-results.sh
// sets it).
func TestRecordResults(t *testing.T) {
	dir := os.Getenv("BARESQL_RECORD_DIR")
	if dir == "" {
		t.Skip("set BARESQL_RECORD_DIR to record results")
	}
	sev := map[string]int{}
	rows := map[string]int{}
	answers, versionKeys := 0, 0
	for _, e := range Catalog() {
		sev[e.Severity]++
		switch e.Code {
		case "function-not-everywhere", "type-not-everywhere", "operator-not-everywhere":
			rows[e.Code] = len(e.Items)
		default:
			rows["forms of other rules"] += len(e.Items)
		}
		a := RecordedAnswers(e.Code)
		for _, m := range append([]map[string]Recorded{a.Example, a.Rewrite}, itemMaps(a)...) {
			for k := range m {
				if strings.Contains(k, " ") {
					versionKeys++
				} else {
					answers++
				}
			}
		}
	}
	var on []string
	for _, eng := range EngineOrder {
		on = append(on, EngineTitle(eng)+" "+RecordedOn()[eng])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "rules: %d (%d errors, %d warnings, %d notes)\n", len(Catalog()), sev["error"], sev["warning"], sev["info"])
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	total := 0
	for _, k := range keys {
		fmt.Fprintf(&b, "rows of %s: %d\n", k, rows[k])
		total += rows[k]
	}
	fmt.Fprintf(&b, "rows in all: %d\n", total)
	fmt.Fprintf(&b, "recorded answers: %d, on %s\n", answers, strings.Join(on, ", "))
	fmt.Fprintf(&b, "answers kept for other versions and builds: %d\n", versionKeys)
	if err := os.MkdirAll(filepath.Join(dir, "portable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "portable", "counts.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itemMaps(a *Answers) []map[string]Recorded {
	var out []map[string]Recorded
	for _, m := range a.Items {
		out = append(out, m)
	}
	return out
}
