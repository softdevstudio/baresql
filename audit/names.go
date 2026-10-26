package audit

import (
	"strings"
	"unicode"
)

// Personal data categories. The keys are part of audit.json: never rename.
var categoryLabel = map[string]string{
	"email":        "email address",
	"phone":        "phone number",
	"name":         "person's name",
	"address":      "postal address",
	"birth-date":   "date of birth",
	"national-id":  "national ID number",
	"passport":     "passport or licence number",
	"tax-id":       "tax ID",
	"card":         "payment card number",
	"bank-account": "bank account (IBAN)",
	"password":     "password or password hash",
	"secret":       "key, token or secret",
	"ip-address":   "IP address",
	"location":     "precise location",
	"health":       "health data",
	"sensitive":    "special category data",
	"gender":       "gender",
}

// CategoryLabel is the plain name of a category.
func CategoryLabel(c string) string {
	if l, ok := categoryLabel[c]; ok {
		return l
	}
	return c
}

// splitName breaks an identifier into lower-case words: customerEmail2 and
// customer_email_2 both give customer, email, 2.
func splitName(s string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1])):
			flush()
			cur = append(cur, r)
		case unicode.IsDigit(r) && i > 0 && !unicode.IsDigit(rs[i-1]):
			flush()
			cur = append(cur, r)
		case !unicode.IsDigit(r) && i > 0 && unicode.IsDigit(rs[i-1]):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

// nameRule matches a sequence of words anywhere in a column name.
type nameRule struct {
	category string
	words    []string
}

// nameRules hold the column names that mean personal data. Single words
// match one word of the name; longer rules match consecutive words. The
// spellings run together (firstname) are listed too, since splitName can't
// split those.
var nameRules = func() []nameRule {
	var rules []nameRule
	add := func(cat string, names ...string) {
		for _, n := range names {
			rules = append(rules, nameRule{cat, strings.Fields(n)})
		}
	}
	add("email", "email", "e mail", "mail", "emailaddress", "mailaddress")
	add("phone", "phone", "telephone", "tel", "mobile", "cellphone", "cell phone", "fax", "msisdn",
		"phonenumber", "telefon", "telefono", "telephone number", "handy", "mobil")
	add("name", "first name", "firstname", "last name", "lastname", "surname", "family name", "familyname",
		"given name", "givenname", "full name", "fullname", "middle name", "maiden name", "middlename",
		"vorname", "nachname", "nombre", "apellido", "apellidos", "prenom", "cognome", "achternaam", "voornaam")
	add("address", "address", "street", "streetaddress", "address line", "addressline", "postcode",
		"postal code", "postalcode", "zip", "zipcode", "zip code", "house number", "housenumber",
		"strasse", "plz", "calle", "direccion", "adresse")
	add("birth-date", "birth", "birthdate", "birthday", "dob", "born", "date of birth", "geburtsdatum",
		"nacimiento", "naissance", "nascita")
	add("national-id", "ssn", "social security", "national id", "nationalid", "nino", "personnummer",
		"bsn", "dni", "nie", "cpf", "pesel", "codice fiscale", "fiscal code", "aadhaar", "nir", "hetu",
		"id card", "idcard", "id number", "national insurance")
	add("passport", "passport", "drivers license", "driver license", "drivers licence", "driving licence",
		"driving license", "licence number")
	add("tax-id", "tax id", "taxid", "tax number", "tin", "steuer id", "steuernummer")
	add("card", "card number", "cardnumber", "credit card", "creditcard", "cc number", "ccnumber", "ccnum",
		"cvv", "cvc", "cvv2", "card cvv", "pan")
	add("bank-account", "iban", "bank account", "bankaccount", "account iban", "sort code", "routing number")
	add("password", "password", "passwd", "pwd", "pass hash", "passhash", "passcode", "pin code", "pincode",
		"passwort", "contrasena")
	add("secret", "secret", "api key", "apikey", "access key", "accesskey", "secret key", "private key",
		"privatekey", "token", "otp secret", "totp", "mfa secret", "client secret", "credentials", "credential")
	add("ip-address", "ip", "ip address", "ipaddress", "ip addr", "ipaddr", "remote addr", "client ip", "ipv4", "ipv6")
	add("location", "latitude", "longitude", "lat", "lng", "lon", "geolocation", "geo location", "coordinates",
		"gps")
	add("health", "diagnosis", "diagnoses", "medical", "health", "allergy", "allergies", "medication",
		"medications", "prescription", "blood type", "blood group", "bloodtype", "disease", "symptoms", "treatment",
		"disability", "icd10", "icd")
	add("sensitive", "religion", "ethnicity", "ethnic", "sexual orientation", "orientation", "political",
		"union member", "trade union", "biometric", "fingerprint", "face encoding", "genetic", "dna")
	add("gender", "gender", "sex")
	return rules
}()

// negativeWords turn a match off: email_verified is a flag, not an
// address; phone_count is a number.
var negativeWords = map[string]bool{
	"verified": true, "verification": true, "sent": true, "status": true, "count": true, "template": true,
	"type": true, "enabled": true, "optin": true, "opt": true, "consent": true, "confirmed": true,
	"confirmation": true, "bounce": true, "bounced": true, "flag": true, "is": true, "has": true,
	"at": true, "date": true, "time": true, "ts": true, "timestamp": true, "preference": true,
	"preferences": true, "frequency": true, "format": true, "provider": true, "domain": true,
	"length": true, "valid": true, "validated": true, "campaign": true, "subject": true, "body": true,
	"notification": true, "notifications": true, "reminder": true, "expires": true, "expiry": true,
	"expiration": true, "required": true, "policy": true, "rule": true, "rules": true, "changed": true,
	"updated": true, "reset": true, "attempts": true, "strength": true, "hint": true, "name": true,
	"label": true, "key": true, "column": true, "field": true, "visible": true, "public": true,
	"allowed": true, "country": true, "code": true, "app": true, "version": true, "device": true, "os": true,
	"platform": true, "browser": true, "layout": true, "view": true, "theme": true, "check": true, "group": true,
	"server": true, "host": true, "port": true, "smtp": true, "queue": true, "id": true, "ids": true,
	"uuid": true, "ref": true, "fk": true,
}

// negativeExceptions: words that are negative in general but part of a
// rule for some categories (date of birth, pin code, password reset token).
var negativeExceptions = map[string]map[string]bool{
	"birth-date":   {"date": true},
	"national-id":  {"id": true, "code": true},
	"password":     {"code": true, "reset": true},
	"secret":       {"reset": true, "key": true},
	"bank-account": {"code": true},
	"address":      {"code": true, "country": true},
	"card":         {"code": true},
	"name":         {"name": true},
}

// peopleTables are words in a table name that mean its rows are people,
// so a bare "name" column is a person's name.
var peopleTables = map[string]bool{
	"customer": true, "customers": true, "user": true, "users": true, "member": true, "members": true,
	"person": true, "persons": true, "people": true, "employee": true, "employees": true, "staff": true,
	"patient": true, "patients": true, "client": true, "clients": true, "contact": true, "contacts": true,
	"student": true, "students": true, "subscriber": true, "subscribers": true, "applicant": true,
	"applicants": true, "candidate": true, "candidates": true, "guest": true, "guests": true,
	"passenger": true, "passengers": true, "driver": true, "drivers": true, "author": true, "authors": true,
	"profile": true, "profiles": true, "lead": true, "leads": true, "donor": true, "donors": true,
	"volunteer": true, "volunteers": true, "resident": true, "residents": true, "buyer": true, "buyers": true,
	"recipient": true, "recipients": true, "beneficiary": true, "beneficiaries": true, "doctor": true,
	"doctors": true, "physician": true, "physicians": true, "nurse": true, "nurses": true, "teacher": true,
	"teachers": true, "parent": true, "parents": true, "account": true, "accounts": true, "player": true,
	"players": true, "athlete": true, "athletes": true, "tenant_user": true, "attendee": true, "attendees": true,
}

// nameCategories returns the categories a column name points to.
func nameCategories(table, column string) []string {
	words := splitName(column)
	var out []string
	seen := map[string]bool{}
	for _, r := range nameRules {
		if seen[r.category] || !containsSeq(words, r.words) {
			continue
		}
		if hasNegative(words, r) {
			continue
		}
		out = append(out, r.category)
		seen[r.category] = true
	}
	// "email address" and "ip address" are not postal addresses.
	if seen["address"] && (seen["email"] || seen["ip-address"] || containsSeq(words, []string{"mac"}) ||
		containsSeq(words, []string{"web"}) || containsSeq(words, []string{"url"}) || containsSeq(words, []string{"wallet"})) {
		out = remove(out, "address")
	}
	// A bare name column in a table of people.
	if !seen["name"] && len(words) == 1 && (words[0] == "name" || words[0] == "fullname") {
		for _, w := range splitName(table) {
			if peopleTables[w] {
				out = append(out, "name")
				break
			}
		}
	}
	return out
}

func hasNegative(words []string, r nameRule) bool {
	inRule := map[string]bool{}
	for _, w := range r.words {
		inRule[w] = true
	}
	for _, w := range words {
		if inRule[w] || negativeExceptions[r.category][w] {
			continue
		}
		if negativeWords[w] {
			return true
		}
	}
	return false
}

func containsSeq(words, seq []string) bool {
	for i := 0; i+len(seq) <= len(words); i++ {
		match := true
		for j := range seq {
			if words[i+j] != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func remove(ss []string, x string) []string {
	var out []string
	for _, s := range ss {
		if s != x {
			out = append(out, s)
		}
	}
	return out
}
