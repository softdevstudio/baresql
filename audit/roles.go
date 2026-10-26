package audit

import "sort"

// Role graph walks. A path runs from the subject to the role at its end,
// both included: [alice, support_team, readers].

type rolePath []uint32

// inherited returns every role whose privileges subject has without SET
// ROLE (itself included), with the shortest membership path to each.
// PostgreSQL 16 and later follow memberships granted WITH INHERIT TRUE;
// before 16, the member's own INHERIT attribute decides each step.
func (s *Snapshot) inherited(subject uint32) map[uint32]rolePath {
	return s.walk(subject, func(m Membership) bool { return m.Inherit })
}

// settable returns the roles subject may SET ROLE to, other than itself.
// From 16 each step needs the SET option; before 16 any membership will do.
func (s *Snapshot) settable(subject uint32) map[uint32]rolePath {
	out := s.walk(subject, func(m Membership) bool { return m.Set && !m.Implicit })
	delete(out, subject)
	return out
}

func (s *Snapshot) walk(subject uint32, follow func(Membership) bool) map[uint32]rolePath {
	byMember := map[uint32][]Membership{}
	for _, m := range s.Memberships {
		byMember[m.Member] = append(byMember[m.Member], m)
	}
	out := map[uint32]rolePath{subject: {subject}}
	queue := []uint32{subject}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		edges := byMember[cur]
		sort.Slice(edges, func(i, j int) bool { return s.RoleName(edges[i].Role) < s.RoleName(edges[j].Role) })
		for _, m := range edges {
			if !follow(m) {
				continue
			}
			if _, seen := out[m.Role]; seen {
				continue
			}
			p := append(append(rolePath{}, out[cur]...), m.Role)
			out[m.Role] = p
			queue = append(queue, m.Role)
		}
	}
	return out
}

// between returns the names of the roles strictly inside a path: the
// memberships passed through on the way from the subject to the target.
func (s *Snapshot) between(p rolePath) []string {
	if len(p) <= 2 {
		return nil
	}
	return s.names(p[1 : len(p)-1])
}

// names turns a path into role names.
func (s *Snapshot) names(p rolePath) []string {
	out := make([]string, len(p))
	for i, id := range p {
		out[i] = s.RoleName(id)
	}
	return out
}

// isSuper reports whether a role is a superuser.
func (s *Snapshot) isSuper(oid uint32) bool {
	r := s.Roles[oid]
	return r != nil && r.Super
}

func (s *Snapshot) predefined(name string) uint32 {
	if r := s.RoleByName[name]; r != nil && r.Predefined {
		return r.OID
	}
	return 0
}
