package audit

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/softdevstudio/baresql/internal/pgwire"
	"github.com/softdevstudio/baresql/internal/testenv"
)

// A fixture is a planted database under testdata/audit/<name>:
//
//	roles.sql     roles (they are global, so every name starts with <name>_)
//	setup.sql     run in a fresh database baresql_fx_<name>
//	expected.txt  the exact Lines() the audit must produce
//
// Both SQL files are Go templates, so fake secrets (card numbers, keys,
// tokens) are made when the test runs and never sit in the repository.
// Lines between "--@ pg>=15" and "--@ end" run only on those versions; in
// expected.txt a line may start with a condition too: "pg>=15: read ...".

type fixture struct {
	name   string
	admin  pgwire.Config // superuser, in the fixture database
	logins []string
	ver    int
}

var condRe = regexp.MustCompile(`^pg(>=|<)(\d+): (.*)$`)

func versionOK(op string, n, ver int) bool {
	if op == ">=" {
		return ver >= n*10000
	}
	return ver < n*10000
}

// filterVersion keeps the lines whose conditions hold on this server.
func filterVersion(lines []string, ver int, sqlStyle bool) []string {
	var out []string
	skip := false
	for _, l := range lines {
		if sqlStyle {
			if m := regexp.MustCompile(`^--@ pg(>=|<)(\d+)\s*$`).FindStringSubmatch(l); m != nil {
				n, _ := strconv.Atoi(m[2])
				skip = !versionOK(m[1], n, ver)
				continue
			}
			if strings.TrimSpace(l) == "--@ end" {
				skip = false
				continue
			}
			if !skip {
				out = append(out, l)
			}
			continue
		}
		if m := condRe.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[2])
			if versionOK(m[1], n, ver) {
				out = append(out, m[3])
			}
			continue
		}
		out = append(out, l)
	}
	return out
}

func render(t *testing.T, path string, seed int64) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGen{r: rand.New(rand.NewSource(seed))}
	tmpl, err := template.New(filepath.Base(path)).Funcs(g.funcs()).Parse(string(src))
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, nil); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return b.String()
}

func loadFixture(t *testing.T, name string) *fixture {
	t.Helper()
	base, err := pgwire.ParseDSN(testenv.DSN(t, testenv.PG))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	admin, err := pgwire.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	vr, err := admin.Query(ctx, "SHOW server_version_num")
	if err != nil {
		t.Fatal(err)
	}
	ver, _ := strconv.Atoi(vr[0].Rows[0][0].String())
	db := "baresql_fx_" + name
	exec := func(c *pgwire.Conn, sql string) {
		t.Helper()
		if err := c.Exec(ctx, sql); err != nil {
			t.Fatalf("fixture %s: %v", name, err)
		}
	}
	exec(admin, "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
	dropFixtureRoles(t, admin, name)
	dir := filepath.Join("testdata", name)
	roles := strings.Join(filterVersion(strings.Split(render(t, filepath.Join(dir, "roles.sql"), 1), "\n"), ver, true), "\n")
	exec(admin, roles)
	exec(admin, "CREATE DATABASE "+db+" OWNER "+name+"_dbowner")
	cfg := base
	cfg.Database = db
	fx, err := pgwire.Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer fx.Close()
	setup := strings.Join(filterVersion(strings.Split(render(t, filepath.Join(dir, "setup.sql"), 2), "\n"), ver, true), "\n")
	exec(fx, setup)
	res, err := admin.QueryArgs(ctx, "SELECT rolname FROM pg_roles WHERE rolcanlogin AND rolname LIKE $1 ORDER BY 1", name+`\_%`)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{name: name, admin: cfg, ver: ver}
	for _, r := range res.Rows {
		f.logins = append(f.logins, r[0].String())
	}
	t.Cleanup(func() {
		if os.Getenv("BARESQL_KEEP_FIXTURES") != "" {
			return
		}
		c, err := pgwire.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close()
		c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)")
		dropFixtureRoles(t, c, name)
	})
	return f
}

func dropFixtureRoles(t *testing.T, admin *pgwire.Conn, name string) {
	ctx := context.Background()
	res, err := admin.QueryArgs(ctx, "SELECT rolname FROM pg_roles WHERE rolname LIKE $1 ORDER BY 1", name+`\_%`)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Rows {
		role := quoteAlways(r[0].String())
		admin.Exec(ctx, "DROP OWNED BY "+role+" CASCADE")
	}
	for _, r := range res.Rows {
		if err := admin.Exec(ctx, "DROP ROLE IF EXISTS "+quoteAlways(r[0].String())); err != nil {
			t.Fatalf("dropping fixture role %s: %v", r[0].String(), err)
		}
	}
}

// auditFixture runs the audit on a fixture, limited to its own logins.
func auditFixture(t *testing.T, f *fixture, sample bool) *Result {
	t.Helper()
	res, err := Run(context.Background(), f.admin, RunOptions{Logins: f.logins, Sample: sample,
		Now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// checkLines compares the result with expected.txt, line by line.
func checkLines(t *testing.T, f *fixture, res *Result, file string) {
	t.Helper()
	got := res.Lines()
	path := filepath.Join("testdata", f.name, file)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(raw), "\n") {
		if l = strings.TrimRight(l, " \t\r"); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	want := filterVersion(lines, f.ver, false)
	sort.Strings(want)
	if os.Getenv("BARESQL_WRITE_GOT") != "" {
		os.WriteFile(path+".got", []byte(strings.Join(got, "\n")+"\n"), 0o644)
	}
	gotSet := map[string]bool{}
	for _, g := range got {
		gotSet[g] = true
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	var missing, extra []string
	for _, w := range want {
		if !gotSet[w] {
			missing = append(missing, w)
		}
	}
	for _, g := range got {
		if !wantSet[g] {
			extra = append(extra, g)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("%s on PostgreSQL %d: %d missing, %d unexpected\nmissing (a miss):\n  %s\nunexpected (a false alarm):\n  %s",
			path, f.ver, len(missing), len(extra), strings.Join(missing, "\n  "), strings.Join(extra, "\n  "))
	}
	if len(res.Mismatches) > 0 {
		t.Errorf("mismatches with the server:\n  %s", strings.Join(res.Mismatches, "\n  "))
	}
}

// fakeGen makes values that look real to the detectors: valid check
// digits, real prefixes. Seeded, so a fixture is the same on every run.
type fakeGen struct{ r *rand.Rand }

func (g *fakeGen) digits(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('0' + g.r.Intn(10))
	}
	return string(b)
}

func luhnComplete(prefix string, length int) string {
	for d := 0; d < 10; d++ {
		s := prefix + string(rune('0'+d))
		if len(s) == length && luhn(s) {
			return s
		}
	}
	return ""
}

func (g *fakeGen) card(network string) string {
	prefix := map[string]string{"visa": "4", "mastercard": "5" + string(rune('1'+g.r.Intn(5))), "amex": "37"}[network]
	length := 16
	if network == "amex" {
		length = 15
	}
	body := prefix + g.digits(length-1-len(prefix))
	return luhnComplete(body, length)
}

// notCard is a 16-digit number that fails the Luhn check.
func (g *fakeGen) notCard() string {
	for {
		s := "4" + g.digits(15)
		if !luhn(s) {
			return s
		}
	}
}

func (g *fakeGen) iban(country string) string {
	bban := map[string]int{"DE": 18, "FR": 23, "NL": 14, "GB": 18}[country]
	var b strings.Builder
	if country == "NL" || country == "GB" {
		for i := 0; i < 4; i++ {
			b.WriteByte(byte('A' + g.r.Intn(26)))
		}
		bban -= 4
	}
	body := b.String() + g.digits(bban)
	for check := 2; check < 99; check++ {
		s := fmt.Sprintf("%s%02d%s", country, check, body)
		if isIBAN(s) {
			return s
		}
	}
	return ""
}

func (g *fakeGen) ssn() string {
	return fmt.Sprintf("%03d-%02d-%04d", 100+g.r.Intn(500), 10+g.r.Intn(89), 1000+g.r.Intn(8999))
}

func (g *fakeGen) dni() string {
	d := g.digits(8)
	return d + string(dniLetter(d))
}

func (g *fakeGen) alnum(n int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[g.r.Intn(len(chars))]
	}
	return string(b)
}

func (g *fakeGen) upper(n int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[g.r.Intn(len(chars))]
	}
	return string(b)
}

func (g *fakeGen) funcs() template.FuncMap {
	return template.FuncMap{
		"seq": func(a, b int) []int {
			var s []int
			for i := a; i <= b; i++ {
				s = append(s, i)
			}
			return s
		},
		"card":    g.card,
		"notcard": g.notCard,
		"iban":    g.iban,
		"ssn":     g.ssn,
		"dni":     g.dni,
		"digits":  g.digits,
		"ghtoken": func() string { return "ghp_" + g.alnum(36) },
		"awskey":  func() string { return "AKIA" + g.upper(16) },
		"stripe":  func() string { return "sk_" + "live_" + g.alnum(24) },
		"jwt": func() string {
			header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
			return header + ".eyJ" + g.alnum(30) + "." + g.alnum(43)
		},
		"bcrypt": func() string { return "$2b$12$" + g.alnum(53) },
		"hex":    func(n int) string { return hexString(g.r, n) },
		"ip": func() string {
			return fmt.Sprintf("%d.%d.%d.%d", 1+g.r.Intn(222), g.r.Intn(256), g.r.Intn(256), 1+g.r.Intn(254))
		},
		"phone": func() string {
			return fmt.Sprintf("+1 %03d-%03d-%04d", 200+g.r.Intn(700), g.r.Intn(1000), g.r.Intn(10000))
		},
		"pick": func(xs ...string) string { return xs[g.r.Intn(len(xs))] },
	}
}

func hexString(r *rand.Rand, n int) string {
	const h = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = h[r.Intn(16)]
	}
	return string(b)
}
