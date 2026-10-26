package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Personal is the verdict on one column.
type Personal struct {
	Categories []string `json:"categories"`
	// Confidence: "high" when sampled values show it, "medium" when only
	// the column name says so.
	Confidence string   `json:"confidence"`
	Evidence   []string `json:"evidence"`
	// Derived: a view column computed from personal data (an expression,
	// not a plain copy). It may expose less, or nothing.
	Derived bool `json:"derived,omitempty"`
}

// typeClass sorts PostgreSQL types into the groups the rules care about.
func typeClass(t string) string {
	t = strings.ToLower(t)
	base := t
	if i := strings.IndexByte(base, '('); i >= 0 {
		base = base[:i]
	}
	switch {
	case strings.HasSuffix(t, "[]"):
		return "array"
	case base == "boolean":
		return "bool"
	case base == "json" || base == "jsonb":
		return "json"
	case base == "inet" || base == "cidr":
		return "inet"
	case base == "date" || strings.HasPrefix(base, "timestamp") || strings.HasPrefix(base, "time"):
		return "date"
	case base == "smallint" || base == "integer" || base == "bigint" || base == "numeric" || base == "real" ||
		base == "double precision" || base == "money" || base == "oid":
		return "number"
	case base == "uuid":
		return "uuid"
	case base == "bytea":
		return "binary"
	case base == "point" || base == "geometry" || base == "geography":
		return "geo"
	}
	return "text" // text, varchar, char, citext, enums, domains ...
}

// nameFits says whether a name hint makes sense for a column of this type:
// email_sent_at (a timestamp) and has_phone (a boolean) hold no contact data.
func nameFits(category, class string) bool {
	switch class {
	case "bool", "uuid", "binary":
		return false
	case "date":
		return category == "birth-date"
	case "number":
		switch category {
		case "phone", "card", "national-id", "tax-id", "bank-account", "location", "passport":
			return true
		}
		return false
	case "inet":
		return category == "ip-address"
	case "geo":
		return category == "location"
	}
	return true
}

// valueStats counts what the detectors found in one column's sample.
type valueStats struct {
	n      int
	whole  map[string]int // detector -> values that are entirely that
	inText map[string]int // detector -> values containing one
	ids    map[string]int // national ID scheme -> values
	json   map[string]map[string]int
}

func newStats() *valueStats {
	return &valueStats{whole: map[string]int{}, inText: map[string]int{}, ids: map[string]int{},
		json: map[string]map[string]int{}}
}

// add looks at one value.
func (st *valueStats) add(v string, isJSON bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return
	}
	st.n++
	if isJSON {
		st.addJSON(v)
		return
	}
	for _, d := range wholeDetectors(v) {
		st.whole[d]++
	}
	if s := nationalID(v); s != "" {
		st.ids[s]++
	}
	// Free text: look for values inside it.
	if strings.ContainsAny(v, " \n\t:;,") {
		if !isEmail(v) && reEmailInText.MatchString(v) {
			st.inText["email"]++
		}
		if cardInText(v) {
			st.inText["card"]++
		}
		if ibanInText(v) {
			st.inText["bank-account"]++
		}
	}
	if isSecret(v) {
		st.inText["secret"]++
	}
}

func wholeDetectors(v string) []string {
	var out []string
	if isEmail(v) {
		out = append(out, "email")
	}
	if isPhone(v) {
		out = append(out, "phone")
	}
	if isCard(v) {
		out = append(out, "card")
	}
	if isIBAN(v) {
		out = append(out, "bank-account")
	}
	if isIP(v) {
		out = append(out, "ip-address")
	}
	if isPasswordHash(v) {
		out = append(out, "password-hash")
	}
	if reHexDigest.MatchString(v) {
		out = append(out, "hex-digest")
	}
	if highEntropy(v) {
		out = append(out, "random-token")
	}
	if isBirthDate(v) {
		out = append(out, "date")
	}
	return out
}

func cardInText(v string) bool {
	for _, m := range reCardInText.FindAllString(v, 5) {
		if isCard(m) {
			return true
		}
	}
	return false
}

func ibanInText(v string) bool {
	for _, m := range reIBANInText.FindAllString(v, 5) {
		if isIBAN(m) {
			return true
		}
	}
	return false
}

// addJSON walks a JSON document (possibly cut short by the sample limit)
// and classifies each key and string value by its path.
func (st *valueStats) addJSON(doc string) {
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.UseNumber()
	type frame struct {
		obj bool
		key string
	}
	var stack []frame
	path := func(last string) string {
		var parts []string
		for _, f := range stack {
			if f.obj && f.key != "" {
				parts = append(parts, f.key)
			}
		}
		if last != "" {
			parts = append(parts, last)
		}
		return strings.Join(parts, ".")
	}
	expectKey := false
	seen := map[string]bool{} // count each path and category once per document
	mark := func(p, cat string) {
		if p == "" || seen[p+"\x00"+cat] {
			return
		}
		seen[p+"\x00"+cat] = true
		if st.json[p] == nil {
			st.json[p] = map[string]int{}
		}
		st.json[p][cat]++
	}
	for steps := 0; steps < 10000; steps++ {
		tok, err := dec.Token()
		if err == io.EOF || err != nil {
			return
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, frame{obj: true})
				expectKey = true
			case '[':
				stack = append(stack, frame{})
				expectKey = false
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				expectKey = len(stack) > 0 && stack[len(stack)-1].obj
			}
			continue
		case string:
			if expectKey && len(stack) > 0 && stack[len(stack)-1].obj {
				stack[len(stack)-1].key = t
				expectKey = false
				continue
			}
			p := path("")
			for _, d := range wholeDetectors(t) {
				if c := detectorCategory(d); c != "" {
					mark(p, c)
				}
			}
			if isSecret(t) {
				mark(p, "secret")
			}
			if nationalID(t) != "" {
				mark(p, "national-id")
			}
			for _, c := range nameCategories("", lastKey(p)) {
				mark(p, "name:"+c)
			}
		default: // numbers, booleans, null
			p := path("")
			if n, ok := tok.(json.Number); ok && isCard(n.String()) {
				mark(p, "card")
			}
		}
		if len(stack) > 0 && stack[len(stack)-1].obj {
			expectKey = true
		}
	}
}

func lastKey(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// detectorCategory maps a whole-value detector to the category it proves
// on its own; weak detectors (dates, digests, random tokens) prove nothing
// without a matching column name.
func detectorCategory(d string) string {
	switch d {
	case "email", "card", "bank-account", "ip-address":
		return d
	case "password-hash":
		return "password"
	}
	return ""
}

// classify decides on one column from its name and, when sampled, its
// values. It returns nil when nothing points to personal data.
func classify(table string, col *Column, st *valueStats) *Personal {
	class := typeClass(col.Type)
	hints := map[string]bool{}
	for _, c := range nameCategories(table, col.Name) {
		if nameFits(c, class) {
			hints[c] = true
		}
	}
	p := &Personal{}
	found := map[string]string{} // category -> confidence
	addEv := func(cat, conf, ev string) {
		if old, ok := found[cat]; !ok || old == "medium" && conf == "high" {
			found[cat] = conf
		}
		p.Evidence = append(p.Evidence, ev)
	}
	if st != nil && st.n > 0 {
		n := st.n
		frac := func(k int) float64 { return float64(k) / float64(n) }
		of := func(k int, what string) string { return fmt.Sprintf("%d of %d sampled values %s", k, n, what) }
		type rule struct {
			det, cat, what string
			alone, hinted  float64 // share needed without and with a name hint
		}
		for _, r := range []rule{
			{"email", "email", "are email addresses", 0.5, 0.3},
			{"phone", "phone", "are phone numbers", 0.8, 0.5},
			{"card", "card", "are payment card numbers (valid check digit)", 0.8, 0.5},
			{"bank-account", "bank-account", "are IBANs (valid check digits)", 0.5, 0.3},
			{"ip-address", "ip-address", "are IP addresses", 0.8, 0.5},
			{"password-hash", "password", "are password hashes", 0.5, 0.3},
			{"hex-digest", "password", "are hex digests", 2, 0.5},         // only with a password name
			{"random-token", "secret", "look like random tokens", 2, 0.5}, // only with a secret name
			{"date", "birth-date", "are plausible dates of birth", 2, 0.5},
		} {
			k := st.whole[r.det]
			if k == 0 {
				continue
			}
			need := r.alone
			if hints[r.cat] {
				need = r.hinted
			}
			if frac(k) >= need {
				addEv(r.cat, "high", of(k, r.what))
			}
		}
		for scheme, k := range st.ids {
			need := 0.8
			if hints["national-id"] || scheme == "us-ssn" || scheme == "uk-nino" || scheme == "es-dni" || scheme == "it-codice-fiscale" {
				need = 0.5
			}
			if frac(k) >= need {
				addEv("national-id", "high", of(k, "are national ID numbers ("+scheme+")"))
			}
		}
		for _, r := range []struct{ det, cat, what string }{
			{"email", "email", "contain an email address"},
			{"card", "card", "contain a payment card number"},
			{"bank-account", "bank-account", "contain an IBAN"},
			{"secret", "secret", "contain a key or token"},
		} {
			if k := st.inText[r.det]; k > 0 && found[r.cat] == "" {
				conf := "medium"
				if r.cat == "secret" {
					conf = "high"
				}
				addEv(r.cat, conf, of(k, r.what))
			}
		}
		var paths []string
		for p := range st.json {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, path := range paths {
			cats := st.json[path]
			var names []string
			for c := range cats {
				names = append(names, c)
			}
			sort.Strings(names)
			for _, c := range names {
				k := cats[c]
				if strings.HasPrefix(c, "name:") {
					cat := strings.TrimPrefix(c, "name:")
					addEv(cat, "medium", fmt.Sprintf("JSON key %s (in %d of %d sampled values) is named like %s", path, k, n, CategoryLabel(cat)))
					continue
				}
				if frac(k) >= 0.3 || c == "secret" {
					addEv(c, "high", fmt.Sprintf("JSON key %s holds %s in %d of %d sampled values", path, CategoryLabel(c), k, n))
				}
			}
		}
	}
	var hinted []string
	for c := range hints {
		hinted = append(hinted, c)
	}
	sort.Strings(hinted)
	for _, c := range hinted {
		if _, ok := found[c]; !ok {
			addEv(c, "medium", "the column name suggests "+CategoryLabel(c))
		}
	}
	if len(found) == 0 {
		return nil
	}
	p.Confidence = "medium"
	for c, conf := range found {
		p.Categories = append(p.Categories, c)
		if conf == "high" {
			p.Confidence = "high"
		}
	}
	sort.Strings(p.Categories)
	return p
}
