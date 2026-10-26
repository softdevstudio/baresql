package audit

import (
	"math/rand"
	"strings"
	"testing"
)

func TestSplitName(t *testing.T) {
	for in, want := range map[string]string{
		"customer_email":   "customer email",
		"customerEmail2":   "customer email 2",
		"HTTPServerIP":     "http server ip",
		"e-mail":           "e mail",
		"lastLoginIP":      "last login ip",
		"date_of_birth":    "date of birth",
		"ZIPCode":          "zip code",
		"phone1":           "phone 1",
		"Straße":           "strasse",
		"Prénom":           "prenom",
		"__weird__name___": "weird name",
	} {
		if got := strings.Join(splitName(in), " "); got != want {
			t.Errorf("splitName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNameCategories(t *testing.T) {
	for _, tc := range []struct {
		table, column, want string
	}{
		{"customers", "email", "email"},
		{"customers", "email_address", "email"},
		{"customers", "contactEmail", "email"},
		{"customers", "email_verified", ""},
		{"customers", "email_sent_at", ""},
		{"campaigns", "email_template", ""},
		{"customers", "mobile_phone", "phone"},
		{"devices", "mobile_app_version", ""},
		{"customers", "first_name", "name"},
		{"customers", "name", "name"},
		{"products", "name", ""},
		{"products", "product_name", ""},
		{"users", "street_address", "address"},
		{"users", "ip_address", "ip-address"},
		{"users", "mac_address", ""},
		{"patients", "date_of_birth", "birth-date"},
		{"patients", "dob", "birth-date"},
		{"people", "ssn", "national-id"},
		{"people", "national_id", "national-id"},
		{"payments", "card_number", "card"},
		{"payments", "cvv", "card"},
		{"payments", "iban", "bank-account"},
		{"users", "password_hash", "password"},
		{"users", "password_reset_token", "password,secret"},
		{"users", "password_changed_at", ""},
		{"settings", "api_key", "secret"},
		{"sessions", "access_token", "secret"},
		{"users", "last_login_ip", "ip-address"},
		{"stores", "latitude", "location"},
		{"patients", "diagnosis", "health"},
		{"services", "health_check", ""},
		{"patients", "blood_group", "health"},
		{"experiments", "treatment_group", ""},
		{"users", "gender", "gender"},
		{"orders", "address_id", ""},
		{"orders", "zip", "address"},
		{"orders", "tracking_number", ""},
		// From the independent review: names it missed, and false alarms.
		{"orders", "customer_name", "name"},
		{"payments", "cardholder_name", "name"},
		{"users", "display_name", "name"},
		{"users", "fname", "name"},
		{"users", "lname", "name"},
		{"orders", "billing_name", "name"},
		{"addresses", "street_name", "address"},
		{"kunden", "telefonnummer", "phone"},
		{"kunden", "handynummer", "phone"},
		{"kunden", "emailadresse", "email"},
		{"kunden", "lieferadresse", "address"},
		{"kunden", "postleitzahl", "address"},
		{"kunden", "geburtstag", "birth-date"},
		{"kunden", "straße", "address"},
		{"clientes", "correo", "email"},
		{"clientes", "correo_electronico", "email"},
		{"clientes", "celular", "phone"},
		{"clientes", "contraseña", "password"},
		{"clientes", "teléfono", "phone"},
		{"clients", "prénom", "name"},
		{"clients", "téléphone", "phone"},
		{"clients", "courriel", "email"},
		{"clients", "nom", "name"},
		{"produits", "nom", ""},
		{"clienti", "cellulare", "phone"},
		{"clienti", "nome", "name"},
		{"klanten", "naam", "name"},
		{"clientes", "telefone", "phone"},
		{"clientes", "endereco", "address"},
		{"klienci", "nazwisko", "name"},
		{"klienci", "imię", "name"},
		{"uploads", "zip_file", ""},
		{"orders", "zip_code", "address"},
		{"customers", "pan", "card"},
		{"customers", "pan_card", "tax-id"},
		{"cameras", "pan_angle", ""},
		{"vendors", "tin", "tax-id"},
		{"vendors", "tin_number", "tax-id"},
		{"materials", "tin_plate", ""},
		{"services", "health_score", ""},
		{"trials", "treatment_arm", ""},
		{"mail", "mail_server", ""},
	} {
		got := strings.Join(nameCategories(tc.table, tc.column), ",")
		if got != tc.want {
			t.Errorf("%s.%s: got %q, want %q", tc.table, tc.column, got, tc.want)
		}
	}
}

func TestValueDetectors(t *testing.T) {
	g := &fakeGen{r: rand.New(rand.NewSource(7))}
	yes := map[string][]string{
		"email":       {"anna@example.com", "a.b+c@sub.example.co.uk"},
		"phone":       {"+44 20 7946 0958", "(555) 123-4567", "030 1234567", "+15551234567", "01.23.45.67.89"},
		"card":        {g.card("visa"), g.card("mastercard"), g.card("amex"), "4111 1111 1111 1111", "4111-1111-1111-1111"},
		"iban":        {"DE89370400440532013000", "GB82 WEST 1234 5698 7654 32", g.iban("FR"), g.iban("NL")},
		"ip":          {"192.168.1.10", "2001:db8::1", "10.0.0.0/8"},
		"secret":      {"AKIA" + strings.Repeat("A", 16), "ghp_" + strings.Repeat("a1B2", 9), "xoxb-123456789012-abcdef", "-----BEGIN RSA PRIVATE KEY-----"},
		"hash":        {"$2b$12$" + strings.Repeat("a", 53), "$argon2id$v=19$m=65536,t=3,p=4$abc$def", "md5" + strings.Repeat("0a", 16), "pbkdf2_sha256$600000$salt$hash"},
		"national-id": {"123-45-6789", g.dni(), "AB123456C", "811228-9874", "131052-308T"},
	}
	no := map[string][]string{
		"email":       {"anna@", "@example.com", "not an email", "a@b"},
		"phone":       {"2024-01-02", "192.168.1.1", "1234567", "12.50", "12345", "call me", "123-45-6789", "978-3-16-148410-0", "0-306-40615-2"},
		"card":        {"4111111111111112", "1234567812345678", "4111", g.notCard()},
		"iban":        {"DE89370400440532013001", "XX89370400440532013000", "DE8937040044053201300"},
		"ip":          {"999.1.1.1", "hello", "1234"},
		"secret":      {"AKIA", "ghp_short", "plain text"},
		"hash":        {"password123", "$2b$12$short"},
		"national-id": {"000-12-3456", "666-12-3456", "12345678A", "123456789"},
	}
	check := func(kind, v string) bool {
		switch kind {
		case "email":
			return isEmail(v)
		case "phone":
			return isPhone(v)
		case "card":
			return isCard(v)
		case "iban":
			return isIBAN(v)
		case "ip":
			return isIP(v)
		case "secret":
			return isSecret(v)
		case "hash":
			return isPasswordHash(v)
		case "national-id":
			return nationalID(v) != ""
		}
		return false
	}
	for kind, vs := range yes {
		for _, v := range vs {
			if !check(kind, v) {
				t.Errorf("%s: %q not recognised", kind, v)
			}
		}
	}
	for kind, vs := range no {
		for _, v := range vs {
			if check(kind, v) {
				t.Errorf("%s: %q wrongly recognised", kind, v)
			}
		}
	}
}

func TestNationalIDSchemes(t *testing.T) {
	for v, want := range map[string]string{
		"123-45-6789":           "us-ssn",
		"AB123456C":             "uk-nino",
		"12345678Z":             "es-dni",
		"X1234567L":             "es-nie",
		"RSSMRA85T10A562S":      "it-codice-fiscale",
		"811228-9874":           "se-personnummer",
		"131052-308T":           "fi-hetu",
		"44051401458":           "pl-pesel",
		"1 85 05 78 006 084 91": "fr-nir",
		"1 85 05 78 006 084 36": "", // wrong key
	} {
		if got := nationalID(v); got != want {
			t.Errorf("%q: got %q, want %q", v, got, want)
		}
	}
}

func stats(vals ...string) *valueStats {
	st := newStats()
	for _, v := range vals {
		st.add(v, false)
	}
	return st
}

func TestClassifyByValues(t *testing.T) {
	g := &fakeGen{r: rand.New(rand.NewSource(3))}
	var cards, notCards, emails, phones []string
	for i := 0; i < 20; i++ {
		cards = append(cards, g.card("visa"))
		notCards = append(notCards, g.notCard())
		emails = append(emails, "user"+itoa(i)+"@example.org")
		phones = append(phones, g.funcs()["phone"].(func() string)())
	}
	text := &Column{Name: "data", Type: "text"}
	cases := []struct {
		name string
		col  *Column
		st   *valueStats
		want string // categories/confidence, "" for none
	}{
		{"cards by values", text, stats(cards...), "card/high"},
		{"luhn failures are not cards", &Column{Name: "tracking_number", Type: "text"}, stats(notCards...), ""},
		{"emails by values", text, stats(emails...), "email/high"},
		{"phones by values", text, stats(phones...), "phone/high"},
		{"name only", &Column{Name: "email", Type: "text"}, nil, "email/medium"},
		{"name fits type", &Column{Name: "email_verified", Type: "boolean"}, nil, ""},
		{"timestamp named phone", &Column{Name: "phone", Type: "timestamp with time zone"}, nil, ""},
		{"date of birth", &Column{Name: "dob", Type: "date"}, stats("1980-01-02", "1975-12-31", "2001-07-04"), "birth-date/high"},
		{"a free-text email", &Column{Name: "notes", Type: "text"}, stats("call back", "write to x@example.com please", "ok"), "email/medium"},
		{"hex digests need a name", text, stats(strings.Repeat("ab", 16), strings.Repeat("cd", 16)), ""},
		{"hex digests with a password name", &Column{Name: "pwd", Type: "text"}, stats(strings.Repeat("ab", 16), strings.Repeat("cd", 16)), "password/high"},
		{"secrets anywhere", text, stats("token: ghp_"+strings.Repeat("Zz09", 9), "other"), "secret/high"},
	}
	for _, tc := range cases {
		p := classify("t", tc.col, tc.st)
		got := ""
		if p != nil {
			got = strings.Join(p.Categories, ",") + "/" + p.Confidence
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q (evidence %v)", tc.name, got, tc.want, evidence(p))
		}
	}
}

func evidence(p *Personal) []string {
	if p == nil {
		return nil
	}
	return p.Evidence
}

func TestClassifyJSON(t *testing.T) {
	st := newStats()
	for i := 0; i < 10; i++ {
		st.add(`{"contact": {"email": "a`+itoa(i)+`@example.com", "phone_number": "+1 555 0100"}, "plan": "pro", "tags": ["x"]}`, true)
	}
	st.add(`{"truncated": "doc`, true) // cut off by the sample limit
	p := classify("events", &Column{Name: "payload", Type: "jsonb", JSON: true}, st)
	if p == nil || strings.Join(p.Categories, ",") != "email,phone" || p.Confidence != "high" {
		t.Fatalf("got %+v", p)
	}
	if !strings.Contains(strings.Join(p.Evidence, "|"), "JSON key contact.email") {
		t.Fatalf("evidence %v", p.Evidence)
	}
}

// TestJSONKeysStayOut: keys can be data (maps keyed by email address or by
// name). Evidence shows only column-name words and document structure, so
// none of a sampled key reaches a report, and documents keyed by different
// people collapse into one line.
func TestJSONKeysStayOut(t *testing.T) {
	st := newStats()
	st.add(`{"by_user":{"alice.smith@example.com":{"email":"alice.smith@example.com"}}}`, true)
	st.add(`{"by_user":{"bob.jones@example.org":{"email":"bob.jones@example.org"}}}`, true)
	st.add(`{"tokens":{"Carol Danvers +1-202-555-0143":"ghp_`+strings.Repeat("a1B2", 9)+`"}, "ssn-101-45-6789": "x"}`, true)
	p := classify("prefs", &Column{Name: "data", Type: "jsonb", JSON: true}, st)
	if p == nil {
		t.Fatal("nothing found")
	}
	ev := strings.Join(p.Evidence, "|")
	for _, leak := range []string{"alice", "bob", "example", "Carol", "202", "ssn-101", "45-6789"} {
		if strings.Contains(ev, leak) {
			t.Errorf("evidence shows %q from a sampled key: %v", leak, p.Evidence)
		}
	}
	for _, want := range []string{"JSON key *.*.email holds email address in 2 of 3 sampled values",
		"JSON key tokens.* holds key, token or secret in 1 of 3 sampled values"} {
		if !strings.Contains(ev, want) {
			t.Errorf("evidence lacks %q: %v", want, p.Evidence)
		}
	}
	// Top-level arrays and scalars are classified too.
	st = newStats()
	for i := 0; i < 5; i++ {
		st.add(`["a`+itoa(i)+`@example.com", "b`+itoa(i)+`@example.com"]`, true)
	}
	p = classify("events", &Column{Name: "data", Type: "jsonb", JSON: true}, st)
	if p == nil || strings.Join(p.Categories, ",") != "email" ||
		!strings.Contains(strings.Join(p.Evidence, "|"), "the top level of the JSON holds email address in 5 of 5") {
		t.Fatalf("top-level array: %+v", p)
	}
}
