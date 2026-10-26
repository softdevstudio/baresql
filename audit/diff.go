package audit

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Change is one difference between two audits.
type Change struct {
	Kind   string `json:"kind"` // read, personal, write, function, server, foreign-server, finding
	Login  string `json:"login,omitempty"`
	Object string `json:"object"`
	Detail string `json:"detail,omitempty"`
}

func (c Change) String() string {
	s := c.Kind
	if c.Login != "" {
		s += " " + c.Login
	}
	s += " " + c.Object
	if c.Detail != "" {
		s += " (" + c.Detail + ")"
	}
	return s
}

// Comparison is what changed between an older and a newer audit.
type Comparison struct {
	Old, New string   `json:"-"`
	Grew     []Change `json:"grew"`   // reach the newer audit has and the older didn't
	Shrank   []Change `json:"shrank"` // reach that is gone
	Other    []Change `json:"other"`  // new routes to old reach, findings that came or went
}

// GrewAny reports whether any login can do something it couldn't before.
func (c *Comparison) GrewAny() bool { return len(c.Grew) > 0 }

type fact struct {
	kind, login, object string
	cats                []string
	routes              []string
}

func facts(r *Result) map[string]fact {
	out := map[string]fact{}
	for _, l := range append(append([]LoginReport{}, r.Logins...), r.Public) {
		for _, c := range l.Read {
			// Routes that give no rows today aren't reach: when one starts
			// to give rows, reach grows.
			var routes []Route
			for _, r := range c.Routes {
				if r.GivesRows() {
					routes = append(routes, r)
				}
			}
			if len(routes) == 0 {
				continue
			}
			f := fact{kind: "read", login: l.Name, object: c.Relation + "." + quoteIdent(c.Column), routes: routeStrings(routes)}
			if c.Personal != nil {
				f.cats = c.Personal.Categories
			}
			out[f.kind+"\x00"+f.login+"\x00"+f.object] = f
		}
		for _, w := range l.Write {
			obj := w.Relation + " " + w.Privilege
			if len(w.Columns) > 0 {
				obj += "(" + strings.Join(w.Columns, ",") + ")"
			}
			f := fact{kind: "write", login: l.Name, object: obj, routes: routeStrings(w.Routes)}
			out[f.kind+"\x00"+f.login+"\x00"+f.object] = f
		}
		for _, fa := range l.Functions {
			f := fact{kind: "function", login: l.Name, object: fa.Function, routes: routeStrings(fa.Routes)}
			out[f.kind+"\x00"+f.login+"\x00"+f.object] = f
		}
		for _, s := range l.Server {
			f := fact{kind: "server", login: l.Name, object: s.Capability, routes: routeStrings(s.Routes)}
			out[f.kind+"\x00"+f.login+"\x00"+f.object] = f
		}
		for _, m := range l.Mappings {
			f := fact{kind: "foreign-server", login: l.Name, object: m.Server, routes: routeStrings(m.Routes)}
			out[f.kind+"\x00"+f.login+"\x00"+f.object] = f
		}
	}
	return out
}

// Compare lists what changed from old to new.
func Compare(old, new *Result) *Comparison {
	c := &Comparison{Old: old.Generated, New: new.Generated}
	of, nf := facts(old), facts(new)
	detail := func(f fact) string {
		d := strings.Join(f.routes, " | ")
		if len(f.cats) > 0 {
			d = strings.Join(labels(f.cats), ", ") + "; " + d
		}
		return d
	}
	for k, f := range nf {
		o, ok := of[k]
		if !ok {
			c.Grew = append(c.Grew, Change{Kind: f.kind, Login: f.login, Object: f.object, Detail: detail(f)})
			continue
		}
		if gained := minus(f.cats, o.cats); len(gained) > 0 {
			c.Grew = append(c.Grew, Change{Kind: "personal", Login: f.login, Object: f.object,
				Detail: "now holds " + strings.Join(labels(gained), ", ")})
		}
		if lost := minus(o.cats, f.cats); len(lost) > 0 {
			c.Other = append(c.Other, Change{Kind: "personal", Login: f.login, Object: f.object,
				Detail: "no longer holds " + strings.Join(labels(lost), ", ")})
		}
		if added := minus(f.routes, o.routes); len(added) > 0 {
			c.Other = append(c.Other, Change{Kind: "route", Login: f.login, Object: f.object,
				Detail: "new route: " + strings.Join(added, " | ")})
		}
	}
	for k, f := range of {
		if _, ok := nf[k]; !ok {
			c.Shrank = append(c.Shrank, Change{Kind: f.kind, Login: f.login, Object: f.object, Detail: detail(f)})
		}
	}
	oldF, newF := map[string]Finding{}, map[string]Finding{}
	for _, f := range old.Findings {
		oldF[f.Code+"\x00"+f.Subject] = f
	}
	for _, f := range new.Findings {
		newF[f.Code+"\x00"+f.Subject] = f
	}
	for k, f := range newF {
		if _, ok := oldF[k]; !ok {
			c.Other = append(c.Other, Change{Kind: "finding", Object: f.Code + " " + f.Subject, Detail: "new, " + f.Severity + ": " + f.Message})
		}
	}
	for k, f := range oldF {
		if _, ok := newF[k]; !ok {
			c.Other = append(c.Other, Change{Kind: "finding", Object: f.Code + " " + f.Subject, Detail: "gone"})
		}
	}
	for _, l := range []*[]Change{&c.Grew, &c.Shrank, &c.Other} {
		sort.Slice(*l, func(i, j int) bool { return (*l)[i].String() < (*l)[j].String() })
		if *l == nil {
			*l = []Change{}
		}
	}
	return c
}

func minus(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}

// Write prints the comparison as text.
func (c *Comparison) Write(w io.Writer) {
	fmt.Fprintf(w, "Compared the audit of %s with the audit of %s.\n", c.Old, c.New)
	section := func(title, mark string, cs []Change) {
		fmt.Fprintf(w, "%s: %d\n", title, len(cs))
		for _, ch := range cs {
			fmt.Fprintf(w, "  %s %s\n", mark, ch.String())
		}
	}
	section("Reach that grew", "+", c.Grew)
	section("Reach that is gone", "-", c.Shrank)
	section("Other changes", "~", c.Other)
}
