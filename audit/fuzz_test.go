package audit

import "testing"

// None of these may panic, whatever the input.

func FuzzParseNodeTree(f *testing.F) {
	f.Add(pg16Tree)
	f.Add(`({QUERY :targetList ({TARGETENTRY :expr {VAR :varno 1 :varattno 1 :varlevelsup 0} :resno 1 :resname x :resjunk false}) :rtable ({RANGETBLENTRY :rtekind 0 :relid 5})})`)
	f.Add(`({QUERY :setOperations {SETOPERATIONSTMT :larg {RANGETBLREF :rtindex 1} :rarg {RANGETBLREF :rtindex 9}} :targetList ({TARGETENTRY :resno 1})})`)
	f.Add("({QUERY :a 3 [ 1 2 ]})")
	f.Fuzz(func(t *testing.T, s string) {
		viewLineage(s)
	})
}

func FuzzValueStats(f *testing.F) {
	f.Add("anna@example.com", false)
	f.Add("4111 1111 1111 1111", false)
	f.Add(`{"a": {"email": "x@y.zz", "n": [1, 2, {"b": null}]}}`, true)
	f.Add(`{"unterminated": [`, true)
	f.Add("DE89370400440532013000 and 131052-308T", false)
	f.Fuzz(func(t *testing.T, v string, isJSON bool) {
		st := newStats()
		st.add(v, isJSON)
		classify("t", &Column{Name: "c", Type: "text", JSON: isJSON}, st)
	})
}

func FuzzNameCategories(f *testing.F) {
	f.Add("customers", "email_address")
	f.Add("", "ÄÖÜ_straße2Name")
	f.Fuzz(func(t *testing.T, table, column string) {
		nameCategories(table, column)
	})
}

func FuzzParseTextArray(f *testing.F) {
	f.Add(`{a,"b c",NULL,"x\"y"}`)
	f.Add(`{`)
	f.Fuzz(func(t *testing.T, s string) {
		parseTextArray(s)
	})
}
