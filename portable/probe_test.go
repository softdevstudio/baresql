package portable

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestProbe runs the cases of the file named by BARESQL_PROBE on every
// configured engine and prints the answers. A case starts with a line
// "-- case: name". Development aid; not part of CI.
func TestProbe(t *testing.T) {
	path := os.Getenv("BARESQL_PROBE")
	if path == "" {
		t.Skip("BARESQL_PROBE not set")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	type tc struct{ name, sql string }
	var cases []tc
	for _, part := range strings.Split(string(src), "-- case: ")[1:] {
		nl := strings.IndexByte(part, '\n')
		cases = append(cases, tc{strings.TrimSpace(part[:nl]), part[nl+1:]})
	}
	ops := availableOpeners(t)
	var b strings.Builder
	for _, c := range cases {
		engines := openFresh(t, ops)
		fmt.Fprintf(&b, "== %s\n", c.name)
		for _, e := range engines {
			a, err := runScript(context.Background(), e, c.sql)
			if err != nil {
				fmt.Fprintf(&b, "  %-9s FAILED %v\n", e.Name(), err)
				continue
			}
			r := Record(a, true)
			note := ""
			if len(a.Notes) > 0 {
				note = "   [notes: " + strings.Join(a.Notes, " | ") + "]"
			}
			fmt.Fprintf(&b, "  %-9s %s%s\n", e.Name(), r.String(), note)
		}
		for _, e := range engines {
			e.Close()
		}
	}
	fmt.Print(b.String())
}
