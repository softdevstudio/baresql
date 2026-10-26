package audit

import (
	"sort"
	"strings"
)

// Lines is the result as one fact per line, sorted: what `baresql audit
// --format text` prints and what the fixture tests compare exactly.
//
//	read <login> <relation>.<column> [<categories>] <route>
//	write <login> <relation> <PRIVILEGE>[(<columns>)] <route>
//	function <login> <function> <route>
//	server <login> <capability> <route>
//	foreign-server <login> <server> <route>
//	default <creator> <schema or *> <objects> <PRIVILEGE> to <grantee>
//	finding <severity> <code> <subject>
func (r *Result) Lines() []string {
	var out []string
	logins := append(append([]LoginReport{}, r.Logins...), r.Public)
	for _, l := range logins {
		for _, c := range l.Read {
			cats := "-"
			if c.Personal != nil {
				cats = strings.Join(c.Personal.Categories, ",")
				if c.Personal.Derived {
					cats += "~"
				}
			}
			for _, rt := range c.Routes {
				out = append(out, "read "+l.Name+" "+c.Relation+"."+quoteIdent(c.Column)+" ["+cats+"] "+rt.String())
			}
		}
		for _, w := range l.Write {
			priv := w.Privilege
			if len(w.Columns) > 0 {
				var cs []string
				for _, c := range w.Columns {
					cs = append(cs, quoteIdent(c))
				}
				priv += "(" + strings.Join(cs, ",") + ")"
			}
			for _, rt := range w.Routes {
				out = append(out, "write "+l.Name+" "+w.Relation+" "+priv+" "+rt.String())
			}
		}
		for _, f := range l.Functions {
			for _, rt := range f.Routes {
				out = append(out, "function "+l.Name+" "+f.Function+" "+rt.String())
			}
		}
		for _, s := range l.Server {
			for _, rt := range s.Routes {
				out = append(out, "server "+l.Name+" "+s.Capability+" "+rt.String())
			}
		}
		for _, m := range l.Mappings {
			for _, rt := range m.Routes {
				out = append(out, "foreign-server "+l.Name+" "+m.Server+" "+rt.String())
			}
		}
	}
	for _, d := range r.DefaultPrivileges {
		schema := d.Schema
		if schema == "" {
			schema = "*"
		}
		out = append(out, "default "+d.Creator+" "+schema+" "+d.Objects+" "+d.Privilege+" to "+d.Grantee)
	}
	for _, f := range r.Findings {
		out = append(out, "finding "+f.Severity+" "+f.Code+" "+f.Subject)
	}
	sort.Strings(out)
	return out
}
