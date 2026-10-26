package audit

import (
	"math/big"
	"net"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Value detectors look at one sampled value and say which kind of personal
// data it is, if any. Each is strict on its own (check digits, prefixes,
// structure), because a column is only called personal when most of its
// sampled values agree.

var (
	reEmail       = regexp.MustCompile(`^[A-Za-z0-9.!#$%&'*+/=?^_{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.[A-Za-z]{2,63}$`)
	reEmailInText = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,63}`)
	rePhoneChars  = regexp.MustCompile(`^\+?[0-9 ()./-]{7,24}$`)
	reCardInText  = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)
	reIBANInText  = regexp.MustCompile(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,4})?\b`)
	reSSN         = regexp.MustCompile(`^(\d{3})-(\d{2})-(\d{4})$`)
	reNINO        = regexp.MustCompile(`^([A-CEGHJ-PR-TW-Z][A-CEGHJ-NPR-TW-Z]) ?\d{2} ?\d{2} ?\d{2} ?[A-D]$`)
	reDNI         = regexp.MustCompile(`^(\d{8})-?([A-Z])$`)
	reNIE         = regexp.MustCompile(`^([XYZ])-?(\d{7})-?([A-Z])$`)
	reCF          = regexp.MustCompile(`^[A-Z]{6}\d{2}[A-EHLMPR-T]\d{2}[A-Z]\d{3}[A-Z]$`)
	rePersonnr    = regexp.MustCompile(`^(\d{2})?(\d{2})(\d{2})(\d{2})[-+](\d{4})$`)
	reHETU        = regexp.MustCompile(`^(\d{2})(\d{2})(\d{2})([-+A-FU-Y])(\d{3})([0-9A-Y])$`)
	reNIR         = regexp.MustCompile(`^([12])\s?(\d{2})\s?(0[1-9]|1[0-2]|[2-9]\d)\s?(\d{2}|2[AB])\s?(\d{3})\s?(\d{3})\s?(\d{2})$`)
	rePESEL       = regexp.MustCompile(`^\d{11}$`)

	reSecret = []*regexp.Regexp{
		regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),                                // AWS access key ID
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`),                               // GitHub tokens
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{60,}\b`),                             // GitHub fine-grained
		regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}\b`),                            // Slack
		regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`),                 // Stripe
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),                                    // Google API key
		regexp.MustCompile(`\bsk-(?:proj-|ant-)?[A-Za-z0-9_-]{20,}\b`),                     // OpenAI/Anthropic style
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`), // JWT
		regexp.MustCompile(`-----BEGIN (?:[A-Z]+ )*PRIVATE KEY-----`),                      // PEM private key
		regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`),                                 // GitLab
		regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`),                                      // npm
	}
	rePasswordHash = []*regexp.Regexp{
		regexp.MustCompile(`^\$2[abxy]\$\d{2}\$[./A-Za-z0-9]{53}$`),                               // bcrypt
		regexp.MustCompile(`^\$argon2(?:id|i|d)\$`),                                               // argon2
		regexp.MustCompile(`^\$(?:scrypt|7)\$`),                                                   // scrypt
		regexp.MustCompile(`^\$[156]\$(?:rounds=\d+\$)?[./A-Za-z0-9]{1,16}\$[./A-Za-z0-9]{22,}$`), // crypt(3)
		regexp.MustCompile(`^(?:pbkdf2_sha256|pbkdf2_sha1|bcrypt_sha256|argon2)\$`),               // Django
		regexp.MustCompile(`^\$[PH]\$[./A-Za-z0-9]{31}$`),                                         // phpass
		regexp.MustCompile(`^md5[0-9a-f]{32}$`),                                                   // PostgreSQL md5
		regexp.MustCompile(`^SCRAM-SHA-256\$\d+:`),                                                // PostgreSQL SCRAM
		regexp.MustCompile(`^\{(?:SSHA|SHA|SSHA256|SSHA512|CRYPT)\}`),                             // LDAP
	}
	reHexDigest = regexp.MustCompile(`^(?:[0-9a-fA-F]{32}|[0-9a-fA-F]{40}|[0-9a-fA-F]{64}|[0-9a-fA-F]{128})$`)
)

// isEmail: the whole value is one address.
func isEmail(v string) bool { return len(v) <= 254 && reEmail.MatchString(v) }

// isPhone: digits with the usual separators, 7 to 15 digits, and something
// that marks it as a phone number rather than a plain number: a leading +
// or 0, or separators.
func isPhone(v string) bool {
	v = strings.TrimSpace(v)
	if !rePhoneChars.MatchString(v) {
		return false
	}
	digits := 0
	for _, r := range v {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if digits < 7 || digits > 15 {
		return false
	}
	if strings.Count(v, "(") != strings.Count(v, ")") {
		return false
	}
	// Dates, IP addresses, decimal numbers, US social security numbers and
	// book numbers have the same characters.
	if reDateLike.MatchString(v) || isIP(v) || reDecimal.MatchString(v) || reSSN.MatchString(v) || isISBN(v) {
		return false
	}
	return v[0] == '+' || v[0] == '0' || v[0] == '(' || strings.ContainsAny(v, " -./")
}

// isISBN: a book number written with hyphens, ISBN-13 (978 or 979 first)
// or ISBN-10, with a valid check digit.
func isISBN(v string) bool {
	if strings.Count(v, "-") < 3 {
		return false
	}
	var d []byte
	for i := 0; i < len(v); i++ {
		switch c := v[i]; {
		case c >= '0' && c <= '9':
			d = append(d, c)
		case (c == 'X' || c == 'x') && i == len(v)-1:
			d = append(d, 'X')
		case c == '-':
		default:
			return false
		}
	}
	switch len(d) {
	case 13:
		if string(d[:3]) != "978" && string(d[:3]) != "979" {
			return false
		}
		sum := 0
		for i, c := range d {
			if c == 'X' {
				return false
			}
			w := 1
			if i%2 == 1 {
				w = 3
			}
			sum += int(c-'0') * w
		}
		return sum%10 == 0
	case 10:
		sum := 0
		for i, c := range d {
			n := int(c - '0')
			if c == 'X' {
				if i != 9 {
					return false
				}
				n = 10
			}
			sum += n * (10 - i)
		}
		return sum%11 == 0
	}
	return false
}

var (
	reDateLike = regexp.MustCompile(`^(?:\d{4}[-/.]\d{1,2}[-/.]\d{1,2}|\d{1,2}[-/.]\d{1,2}[-/.]\d{2,4})(?:[ T].*)?$`)
	reDecimal  = regexp.MustCompile(`^-?\d+\.\d+$`)
)

// luhn reports whether a digit string passes the Luhn check.
func luhn(digits string) bool {
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if d < 0 || d > 9 {
			return false
		}
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

func digitsOnly(v string) (string, bool) {
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-':
		default:
			return "", false
		}
	}
	return b.String(), true
}

// isCard: 13 to 19 digits (spaces or dashes allowed), a known card network
// prefix with a length that network uses, and a valid Luhn check digit.
func isCard(v string) bool {
	d, ok := digitsOnly(strings.TrimSpace(v))
	if !ok || len(d) < 13 || len(d) > 19 || !luhn(d) {
		return false
	}
	return cardNetwork(d) != ""
}

func cardNetwork(d string) string {
	p := func(n int) int {
		x := 0
		for i := 0; i < n && i < len(d); i++ {
			x = x*10 + int(d[i]-'0')
		}
		return x
	}
	l := len(d)
	switch {
	case d[0] == '4' && (l == 13 || l == 16 || l == 19):
		return "visa"
	case (p(2) >= 51 && p(2) <= 55 || p(4) >= 2221 && p(4) <= 2720) && l == 16:
		return "mastercard"
	case (p(2) == 34 || p(2) == 37) && l == 15:
		return "amex"
	case (p(4) == 6011 || p(2) == 65 || p(3) >= 644 && p(3) <= 649) && l >= 16:
		return "discover"
	case p(4) >= 3528 && p(4) <= 3589 && l >= 16:
		return "jcb"
	case (p(3) >= 300 && p(3) <= 305 || p(2) == 36 || p(2) == 38 || p(2) == 39) && l >= 14:
		return "diners"
	case (p(2) == 62) && l >= 16:
		return "unionpay"
	case (p(4) == 5018 || p(4) == 5020 || p(4) == 5038 || p(4) == 5893 || p(4) == 6304 || p(4) == 6759 || p(4) == 6761 || p(4) == 6762 || p(4) == 6763) && l >= 12:
		return "maestro"
	}
	return ""
}

// ibanLength is the IBAN length per country.
var ibanLength = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28, "BA": 20, "BE": 16, "BG": 22, "BH": 22, "BR": 29,
	"BY": 28, "CH": 21, "CR": 22, "CY": 28, "CZ": 24, "DE": 22, "DK": 18, "DO": 28, "EE": 20, "EG": 29,
	"ES": 24, "FI": 18, "FO": 18, "FR": 27, "GB": 22, "GE": 22, "GI": 23, "GL": 18, "GR": 27, "GT": 28,
	"HR": 21, "HU": 28, "IE": 22, "IL": 23, "IQ": 23, "IS": 26, "IT": 27, "JO": 30, "KW": 30, "KZ": 20,
	"LB": 28, "LC": 32, "LI": 21, "LT": 20, "LU": 20, "LV": 21, "LY": 25, "MC": 27, "MD": 24, "ME": 22,
	"MK": 19, "MR": 27, "MT": 31, "MU": 30, "NL": 18, "NO": 15, "PK": 24, "PL": 28, "PS": 29, "PT": 25,
	"QA": 29, "RO": 24, "RS": 22, "SA": 24, "SC": 31, "SE": 24, "SI": 19, "SK": 24, "SM": 27, "ST": 25,
	"SV": 28, "TL": 23, "TN": 24, "TR": 26, "UA": 29, "VA": 22, "VG": 24, "XK": 20,
}

// isIBAN checks the country length and the mod-97 check digits.
func isIBAN(v string) bool {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(v), " ", ""))
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	if n, ok := ibanLength[s[:2]]; !ok || n != len(s) {
		return false
	}
	if s[2] < '0' || s[2] > '9' || s[3] < '0' || s[3] > '9' {
		return false
	}
	rearranged := s[4:] + s[:4]
	var b strings.Builder
	for _, r := range rearranged {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteString(itoa(int(r-'A') + 10))
		default:
			return false
		}
	}
	n, ok := new(big.Int).SetString(b.String(), 10)
	return ok && new(big.Int).Mod(n, big.NewInt(97)).Int64() == 1
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// nationalID recognises national ID numbers that carry structure or check
// digits; it returns the scheme's name.
func nationalID(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	switch {
	case reSSN.MatchString(v):
		m := reSSN.FindStringSubmatch(v)
		if m[1] != "000" && m[1] != "666" && m[1][0] != '9' && m[2] != "00" && m[3] != "0000" {
			return "us-ssn"
		}
	case reNINO.MatchString(v):
		pre := v[:2]
		if pre != "BG" && pre != "GB" && pre != "NK" && pre != "KN" && pre != "TN" && pre != "NT" && pre != "ZZ" {
			return "uk-nino"
		}
	case reDNI.MatchString(v):
		m := reDNI.FindStringSubmatch(v)
		if dniLetter(m[1]) == m[2][0] {
			return "es-dni"
		}
	case reNIE.MatchString(v):
		m := reNIE.FindStringSubmatch(v)
		prefix := string(rune('0' + strings.Index("XYZ", m[1]))) // X=0, Y=1, Z=2
		if dniLetter(prefix+m[2]) == m[3][0] {
			return "es-nie"
		}
	case reCF.MatchString(v):
		if codiceFiscaleCheck(v) {
			return "it-codice-fiscale"
		}
	case rePersonnr.MatchString(v):
		m := rePersonnr.FindStringSubmatch(v)
		if validDate(m[2], m[3], m[4]) && luhn(m[2]+m[3]+m[4]+m[5]) {
			return "se-personnummer"
		}
	case reHETU.MatchString(v):
		m := reHETU.FindStringSubmatch(v)
		if hetuCheck(m[1]+m[2]+m[3]+m[5]) == m[6][0] && validDate(m[3], m[2], m[1]) {
			return "fi-hetu"
		}
	case reNIR.MatchString(v):
		if nirCheck(v) {
			return "fr-nir"
		}
	case rePESEL.MatchString(v):
		if peselCheck(v) {
			return "pl-pesel"
		}
	}
	return ""
}

func dniLetter(digits string) byte {
	n := 0
	for _, r := range digits {
		n = (n*10 + int(r-'0')) % 23
	}
	return "TRWAGMYFPDXBNJZSQVHLCKE"[n]
}

func codiceFiscaleCheck(v string) bool {
	odd := map[byte]int{'0': 1, '1': 0, '2': 5, '3': 7, '4': 9, '5': 13, '6': 15, '7': 17, '8': 19, '9': 21,
		'A': 1, 'B': 0, 'C': 5, 'D': 7, 'E': 9, 'F': 13, 'G': 15, 'H': 17, 'I': 19, 'J': 21, 'K': 2, 'L': 4,
		'M': 18, 'N': 20, 'O': 11, 'P': 3, 'Q': 6, 'R': 8, 'S': 12, 'T': 14, 'U': 16, 'V': 10, 'W': 22,
		'X': 25, 'Y': 24, 'Z': 23}
	sum := 0
	for i := 0; i < 15; i++ {
		c := v[i]
		if i%2 == 0 { // 1st, 3rd ... (odd positions, counted from 1)
			sum += odd[c]
		} else if c >= '0' && c <= '9' {
			sum += int(c - '0')
		} else {
			sum += int(c - 'A')
		}
	}
	return byte('A'+sum%26) == v[15]
}

func hetuCheck(nine string) byte {
	n := 0
	for _, r := range nine {
		n = (n*10 + int(r-'0')) % 31
	}
	return "0123456789ABCDEFHJKLMNPRSTUVWXY"[n]
}

func nirCheck(v string) bool {
	s := strings.ReplaceAll(v, " ", "")
	body, key := s[:13], s[13:]
	// Corsica: 2A and 2B count as 19 and 18.
	body = strings.Replace(strings.Replace(body, "2A", "19", 1), "2B", "18", 1)
	n, ok := new(big.Int).SetString(body, 10)
	k, ok2 := new(big.Int).SetString(key, 10)
	if !ok || !ok2 {
		return false
	}
	want := 97 - new(big.Int).Mod(n, big.NewInt(97)).Int64()
	return want == k.Int64()
}

func peselCheck(v string) bool {
	w := []int{1, 3, 7, 9, 1, 3, 7, 9, 1, 3}
	sum := 0
	for i := 0; i < 10; i++ {
		sum += int(v[i]-'0') * w[i]
	}
	if (10-sum%10)%10 != int(v[10]-'0') {
		return false
	}
	month := int(v[2]-'0')*10 + int(v[3]-'0')
	day := int(v[4]-'0')*10 + int(v[5]-'0')
	m := month % 20
	return m >= 1 && m <= 12 && day >= 1 && day <= 31
}

// validDate checks a two-digit year, month and day.
func validDate(yy, mm, dd string) bool {
	m := atoi2(mm)
	d := atoi2(dd)
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return false
	}
	_, err := time.Parse("2006-01-02", "20"+yy+"-"+mm+"-"+dd)
	if err != nil {
		_, err = time.Parse("2006-01-02", "19"+yy+"-"+mm+"-"+dd)
	}
	return err == nil
}

func atoi2(s string) int {
	if len(s) != 2 || s[0] < '0' || s[0] > '9' || s[1] < '0' || s[1] > '9' {
		return -1
	}
	return int(s[0]-'0')*10 + int(s[1]-'0')
}

// isIP: an IPv4 or IPv6 address, with an optional /prefix (inet output).
func isIP(v string) bool {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, '/'); i >= 0 {
		v = v[:i]
	}
	if !strings.ContainsAny(v, ".:") {
		return false
	}
	return net.ParseIP(v) != nil
}

func isSecret(v string) bool {
	for _, re := range reSecret {
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

func isPasswordHash(v string) bool {
	for _, re := range rePasswordHash {
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

// highEntropy: a long random-looking token (hex, base64 or base62), used
// only together with a name like token or secret.
func highEntropy(v string) bool {
	if len(v) < 20 || strings.ContainsAny(v, " \t\n") {
		return false
	}
	var lower, upper, digit int
	counts := map[rune]int{}
	for _, r := range v {
		counts[r]++
		switch {
		case unicode.IsLower(r):
			lower++
		case unicode.IsUpper(r):
			upper++
		case unicode.IsDigit(r):
			digit++
		}
	}
	if digit == 0 && (lower == 0 || upper == 0) {
		return false
	}
	return float64(len(counts)) >= 0.4*float64(len(v)) || len(counts) >= 16
}

// isDate: an ISO date or timestamp as PostgreSQL prints it, with a
// plausible year of birth.
func isBirthDate(v string) bool {
	if len(v) < 10 {
		return false
	}
	t, err := time.Parse("2006-01-02", v[:10])
	if err != nil {
		return false
	}
	return t.Year() >= 1900 && t.Before(time.Now())
}
