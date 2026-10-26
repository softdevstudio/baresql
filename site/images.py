"""The images of baresql.com's pages and posts (the rule posts' images
come from gen_rules.py). Everything shown is read from test/results, the
reports BareSQL wrote and the CI workflow.

Run by build.py; see there.
"""
import json, os, re, subprocess, sys, tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import img  # noqa: E402

import paths  # noqa: E402

RES = paths.RES
CI = paths.CI
OUT = os.path.join(paths.SITE, "static", "images")
esc = img.esc


def read(path):
    return open(os.path.join(RES, path)).read()


def lines_of(path):
    return read(path).rstrip("\n").split("\n")


def grep(path, pattern):
    return [l for l in lines_of(path) if re.search(pattern, l)]


def block(path, pattern):
    for b in re.split(r"\n\s*\n", read(path).rstrip("\n")):
        if re.search(pattern, b.split("\n")[0]):
            return b.split("\n")
    sys.exit("%s: no block %s" % (path, pattern))


def in_file_order(path, patterns):
    """The patterns sorted by where their blocks start in the file, so an image shows statements in line order."""
    text = read(path)
    def at(pat):
        for i, b in enumerate(re.split(r"\n\s*\n", text.rstrip("\n"))):
            if re.search(pat, b.split("\n")[0]):
                return i
        sys.exit("%s: no block %s" % (path, pat))
    return sorted(patterns, key=at)


def problem(path, code):
    """A lint problem: its message line, the flagged text and the advice."""
    ls = lines_of(path)
    for i, l in enumerate(ls):
        if l.endswith("[%s]" % code):
            return ls[i:i + 3]
    sys.exit("%s: no problem %s" % (path, code))


def wrap(text, width, indent="    "):
    """Wraps a terminal line at spaces, the continuation lines indented."""
    out, cur = [], ""
    for word in text.split(" "):
        if cur and len(cur) + 1 + len(word) > width:
            out.append(cur)
            cur = indent + word
        else:
            cur = (cur + " " + word) if cur else word
    out.append(cur)
    return out


def kind_of(line):
    s = line.strip()
    if s.startswith("high ") or "error:" in s or s.startswith("refused"):
        return "err"
    if s.startswith("medium ") or s.startswith("warning") or ": warning:" in s:
        return "warn"
    if s.startswith("low "):
        return "dim"
    return "out"


CARD_CSS = """
body { padding: 40px 60px 0; }
h1 { font-size: 36px; }
.sub { display: none; }
.content { margin-top: 22px; }
.kicker { color: #0e7c86; font-size: 17px; font-weight: 700; text-transform: uppercase; letter-spacing: .08em; margin-bottom: 8px; }
.foot { position: absolute; left: 60px; right: 60px; bottom: 26px; display: flex; justify-content: space-between; color: #5b6676; font-size: 17px; font-weight: 600; }
.mono { font-family: "DejaVu Sans Mono", monospace; }
.darkbox { font-family: "DejaVu Sans Mono", monospace; background: #13223a; color: #fff; padding: 16px 22px; border-radius: 10px; line-height: 1.5; font-size: 18px; }
"""


def card(kicker, title, body, css="", foot="BareSQL"):
    body += '<div class="foot"><span>%s</span><span>baresql.com</span></div>' % esc(foot)
    page = img.card(title, "", body, CARD_CSS + css)
    return page.replace("<h1>", '<div class="kicker">%s</div><h1>' % esc(kicker), 1)


def terminal(title, rows, size=18):
    return img.terminal(title, rows, size)


# --- cards -------------------------------------------------------------

def the_name():
    route = grep("audit/shop/lines.txt", r"^read shop_bob shop\.customers\.email ")[0]
    rules = json.load(open(os.path.join(RES, "portable", "rules.json")))
    dd = [r for r in rules["rules"] if r["code"] == "decimal-division"][0]
    rw = dd["rewrite"].strip().rstrip(";")
    answers = sorted(set(a.get("rows", ["?"])[0] for a in dd["answers"]["rewrite"].values()))
    assert len(answers) == 1, answers
    body = """<div class="two">
<div class="half"><div class="word">Access laid bare</div><div class="tool">BareSQL Audit</div>
<div class="what">Every column each PostgreSQL login can read, and the route that gets it there.</div>
<div class="darkbox small">%s</div></div>
<div class="half"><div class="word">Bare SQL</div><div class="tool">BareSQL Portable</div>
<div class="what">The SQL that answers the same on SQLite, PostgreSQL, MySQL and MariaDB.</div>
<div class="darkbox small">%s<br><span class="ok">%s on all four engines</span></div></div>
</div>""" % (esc(route), esc(rw), esc(answers[0]))
    css = """
.two { display: flex; gap: 28px; margin-top: 8px; }
.half { flex: 1; background: #f3f6f9; border-radius: 12px; padding: 22px 24px; }
.word { font-size: 30px; font-weight: 700; }
.tool { color: #0e7c86; font-weight: 700; font-size: 17px; margin-top: 4px; text-transform: uppercase; letter-spacing: .06em; }
.what { font-size: 18px; color: #3b4656; margin: 12px 0 16px; line-height: 1.4; }
.darkbox.small { font-size: 15px; line-height: 1.45; word-break: break-word; }
.ok { color: #79d48f; }
"""
    return card("The name", "BareSQL, bare in two ways", body, css)


def audit_explained():
    line = grep("audit/shop/lines.txt", r"^read shop_bob shop\.customers\.card_number ")[0]
    steps = [("Login", "shop_bob"), ("Role", "shop_support_team"), ("View", "support.customer_lookup"),
             ("Column", "shop.customers.card_number")]
    arrows = ["SET ROLE", "SELECT granted", "rights of shop_owner"]
    parts = []
    for i, (k, name) in enumerate(steps):
        parts.append('<div class="node"><div class="nk">%s</div><div class="nn">%s</div></div>' % (k, esc(name).replace(".", ".<wbr>")))
        if i < len(arrows):
            parts.append('<div class="arrow"><div class="al">%s</div><div class="ar">&#8594;</div></div>' % esc(arrows[i]))
    body = '<div class="chain">%s</div><div class="darkbox">%s</div>' % ("".join(parts), esc(line))
    css = """
.chain { display: flex; align-items: stretch; margin: 34px 0 40px; }
.node { background: #f3f6f9; border: 2px solid #d7e0e8; border-radius: 12px; padding: 16px 16px; width: 232px; }
.node:last-child { border-color: #e3a5a0; background: #fbefee; }
.nk { color: #5b6676; font-size: 15px; font-weight: 700; text-transform: uppercase; letter-spacing: .06em; }
.nn { font-family: "DejaVu Sans Mono", monospace; font-size: 18px; margin-top: 8px; line-height: 1.35; }
.arrow { flex: 1; text-align: center; color: #0e7c86; display: flex; flex-direction: column; justify-content: center; }
.al { font-size: 13px; font-weight: 700; }
.ar { font-size: 30px; line-height: 1; }
.darkbox { font-size: 18px; }
.cap2 { color: #5b6676; font-size: 16px; margin-top: 10px; }
"""
    body += '<div class="cap2">The same route as one line of baresql audit --format lines</div>'
    return card("BareSQL Audit", "How shop_bob reaches the customers' card numbers", body, css)


def audit_tested():
    rows = []
    names = [("shop", "An online shop"), ("saas", "A multi-tenant app"), ("clinic", "A clinic"),
             ("clean", "A database set up with care"), ("edge", "Corner cases from the review")]
    counts = {}
    for l in lines_of("audit/counts.txt"):
        name, rest = l.split(":", 1)
        facts = dict((int(b), a) for a, b in re.findall(r"(\d+) on (\d+)", rest.split("facts checked exactly:")[1]))
        counts[name] = facts
    head = "<tr><th></th>" + "".join("<th>%d</th>" % v for v in range(13, 19)) + "</tr>"
    for key, title in names:
        rows.append("<tr><td>%s</td>%s</tr>" % (esc(title), "".join("<td class='n'>%s</td>" % counts[key][v] for v in range(13, 19))))
    body = '<div class="cap">PostgreSQL version</div><table class="facts">%s%s</table>' % (head, "".join(rows))
    css = """
.cap { text-align: right; color: #5b6676; font-size: 16px; font-weight: 600; margin: 4px 6px 0 0; }
table.facts { border-spacing: 0 7px; font-size: 20px; }
table.facts th { font-size: 18px; color: #5b6676; text-align: right; padding: 0 18px; font-weight: 700; }
table.facts td { padding: 9px 18px; }
table.facts td:first-child { width: 360px; }
td.n { text-align: right; font-family: "DejaVu Sans Mono", monospace; font-weight: 600; color: #0e7c86; }
"""
    return card("BareSQL Audit, tested", "Facts each audit must get exactly right", body, css,
                foot="Checked line by line on every change")


def portable_tested():
    ci = open(CI).read()
    pg = re.findall(r'"(\d+)"', re.search(r"postgres:.*?version: \[([^\]]*)\]", ci, re.S).group(1))
    my = re.findall(r'engine: mysql, version: "([^"]+)"', ci)
    ma = re.findall(r'engine: mariadb, version: "([^"]+)"', ci)
    counts = read("portable/counts.txt")
    answers = "{:,}".format(int(re.search(r"recorded answers: (\d+)", counts).group(1)))
    on = re.search(r"recorded answers: \d+, on (.+)", counts).group(1)

    def row(name, versions):
        return '<tr><td>%s</td><td>%s</td></tr>' % (name, "".join('<span class="chip">%s</span>' % esc(v) for v in versions))
    body = '<table class="eng">%s%s%s<tr><td>SQLite</td><td class="sq">the sqlite3 of the Linux and macOS test machines</td></tr></table>' % (
        row("PostgreSQL", pg), row("MySQL", my), row("MariaDB", ma))
    body += '<div class="rec"><b>%s</b> answers, recorded on %s</div>' % (answers, esc(on))
    css = """
table.eng { border-spacing: 0 9px; font-size: 21px; }
table.eng td { padding: 10px 18px; }
table.eng td:first-child { width: 190px; font-weight: 700; }
.chip { display: inline-block; font-family: "DejaVu Sans Mono", monospace; font-size: 18px; font-weight: 600; color: #0e7c86; background: #e6f4f5; border-radius: 8px; padding: 4px 12px; margin-right: 10px; }
.sq { font-size: 19px; color: #3b4656; }
.rec { margin-top: 18px; font-size: 20px; color: #3b4656; }
.rec b { color: #1b2533; }
"""
    return card("BareSQL Portable, tested", "Every change runs the whole catalog again on", body, css,
                foot="Recorded by BareSQL Portable")


# --- terminals ---------------------------------------------------------

def summary_rows(path, start=0):
    out = []
    for l in lines_of(path)[start:]:
        out.append((kind_of(l), l))
    return out


def launch():
    rows = [("cmd", "baresql audit postgres://postgres@localhost/baresql_fx_shop")]
    rows += summary_rows("audit/shop/summary.txt")
    rows.append(("gap", ""))
    rows.append(("cmd", "baresql lint shop.sql"))
    lint = problem("portable/shop-lint.txt", "integer-division")
    for i, l in enumerate(wrap(lint[0], 96)):
        rows.append(("warn" if i == 0 else "out", l))
    rows.append(("out", lint[1]))
    return terminal("baresql", rows)


def portable_explained():
    rows = [("cmd", "baresql compare --sqlite --postgres postgres://dev@localhost/scratch \\"),
            ("cont", "    --mysql mysql://dev@localhost:3306/ --mariadb mariadb://dev@localhost:3307/ shop.sql")]
    out = lines_of("portable/shop-compare-4.txt")
    rows.append(("dim", out[0]))
    for pat in in_file_order("portable/shop-compare-4.txt", [r"SUM\(stock\) / COUNT", r"WHERE name = 'mug'"]):
        rows.append(("gap", ""))
        for l in block("portable/shop-compare-4.txt", pat):
            rows.append(("head" if l.startswith("shop.sql") else "dim" if l.strip().startswith("lint:") else "out", l))
    rows.append(("gap", ""))
    rows.append(("out", [l for l in out if re.match(r"\d+ statements:", l)][0]))
    return terminal("baresql compare", rows)


def quarterly():
    rows = [("cmd", "baresql audit --baseline q3/audit.json postgres://postgres@localhost/baresql_fx_shop")]
    base = lines_of("audit/quarterly/baseline.txt")
    i = 0
    while not base[i].startswith("Wrote "):
        rows.append((kind_of(base[i]), base[i]))
        i += 1
    rows.append(("out", base[i]))
    rest = base[i + 1:]
    rows.append(("head", rest[0]))
    rows.append(("err", rest[1]))
    for l in [l for l in rest if "iban" in l][:3]:
        rows.append(("out", l))
    rows.append(("dim", "  ..."))
    rows.append(("cmd", "echo $?"))
    rows.append(("out", "1"))
    return terminal("baresql audit --baseline", rows, 17)


def compare_case(title, cmd, path, patterns, size=18):
    rows = [("cmd" if i == 0 else "cont", c) for i, c in enumerate(cmd)]
    out = lines_of(path)
    rows.append(("dim", out[0]))
    for pat in in_file_order(path, patterns):
        rows.append(("gap", ""))
        for l in block(path, pat):
            rows.append(("head" if l.startswith("queries.sql") else "dim" if l.strip().startswith("lint:") else
                         "err" if "error:" in l else "out", l))
    rows.append(("gap", ""))
    rows.append(("out", [l for l in out if re.match(r"\d+ statements:", l)][0]))
    return terminal(title, rows, size)


# --- report crops ------------------------------------------------------

def crops():
    a = os.path.join(RES, "audit")
    jobs = [
        {"url": "file://%s/shop/report.html" % a, "out": OUT + "/hero-report.png", "width": 1200, "height": 760, "scale": 2},
        {"url": "file://%s/shop/report.html" % a, "out": OUT + "/audit-report.png", "width": 1200, "height": 1020,
         "heading": "Findings", "above": 24},
        {"url": "file://%s/agent/before/report.html" % a, "out": OUT + "/case-ai-agent.png", "width": 1200, "height": 630,
         "heading": "Findings", "above": 24},
        {"url": "file://%s/support/before/report.html" % a, "out": OUT + "/case-support-view.png", "width": 1200, "height": 630,
         "heading": "Who can read personal data", "above": 24},
    ]
    for name in ("shop", "saas", "clinic", "clean"):
        jobs.append({"url": "file://%s/%s/report.html" % (a, name), "out": "%s/sample-%s.png" % (OUT, name),
                     "width": 1200, "height": 630})
    tmp = tempfile.mkdtemp()
    spec = os.path.join(tmp, "crops.json")
    json.dump(jobs, open(spec, "w"))
    subprocess.run(["node", os.path.join(HERE, "crop.js"), spec], check=True)


def main():
    os.makedirs(OUT, exist_ok=True)
    jobs = [
        (the_name(), OUT + "/the-name.png"),
        (audit_explained(), OUT + "/audit-explained.png"),
        (audit_tested(), OUT + "/audit-tested.png"),
        (portable_tested(), OUT + "/portable-tested.png"),
        (launch(), OUT + "/launch.png"),
        (portable_explained(), OUT + "/portable-explained.png"),
        (quarterly(), OUT + "/case-quarterly-review.png"),
        (compare_case("baresql compare", ["baresql compare --sqlite --postgres postgres://dev@localhost/scratch schema.sql queries.sql"],
                      "portable/sqlite-tests-compare.txt", [r"LIKE 'bob%'", r"is_admin = 1"]), OUT + "/case-sqlite-tests.png"),
        (compare_case("baresql compare", ["baresql compare --mysql mysql://dev@localhost:3306/ \\",
                                           "    --postgres postgres://dev@localhost/scratch schema.sql queries.sql"],
                      "portable/mysql-to-postgres-compare.txt", [r"status = 'paid'", r"'Bob Stone'"]), OUT + "/case-mysql-to-postgres.png"),
    ]
    img.render(jobs)
    crops()
    print("wrote %d images" % (len(jobs) + 8))


if __name__ == "__main__":
    main()
