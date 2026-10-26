"""Writes one baresql.com post per BareSQL Portable rule, with its image,
from test/results/portable (never edited by hand) and rules_site.py.

Run by build.py; see there.
"""
import html, json, math, os, re, sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
from rules_site import RULES  # noqa: E402
import img  # noqa: E402

import paths  # noqa: E402

RES = os.path.join(paths.RES, "portable")
SITE = paths.SITE
DATE = os.environ.get("POST_DATE", "2026-10-26")
ENGINES = [("sqlite", "SQLite"), ("postgres", "PostgreSQL"), ("mysql", "MySQL"), ("mariadb", "MariaDB")]

data = json.load(open(os.path.join(RES, "rules.json")))
ON = data["recorded_on"]
CAT = {e["code"]: e for e in data["rules"]}
ORDER = [e["code"] for e in data["rules"]]


def slug(title):
    return title.lower().replace(" ", "-")


def url(code):
    return "https://baresql.com/%s/" % slug(RULES[code][0])


def label(eng):
    return dict(ENGINES)[eng] + " " + ON[eng]


def engines_list():
    names = [label(e) for e, _ in ENGINES]
    return ", ".join(names[:-1]) + " and " + names[-1]


SYNTAX = re.compile(r"^You have an error in your SQL syntax; check the manual that corresponds to your (MySQL|MariaDB) server version for the right syntax to use near ")


def answer_text(rec):
    if "error" in rec:
        return "error: " + SYNTAX.sub("You have an error in your SQL syntax ... near ", rec["error"])
    rows = rec.get("rows") or []
    s = "; ".join(rows) if rows else "no rows"
    if rec.get("columns"):
        s = "columns " + ", ".join(rec["columns"]) + ": " + s
    return s


def code_span(text):
    """text as a Markdown code span for a table cell: Hugo's typographer and
    backslash escapes leave code spans alone, so answers stay verbatim."""
    text = text.replace("|", "\\|")
    n = 1
    while "`" * n in text:
        n += 1
    if text.startswith("`") or text.endswith("`"):
        text = " " + text + " "
    return "`" * n + text + "`" * n


def cell(rec):
    if "error" in rec:
        return "error: " + code_span(answer_text(rec)[len("error: "):])
    if not rec.get("rows"):
        return answer_text(rec)
    return code_span(answer_text(rec))


def link_codes(text):
    """A rule code such as decimal-division becomes a link to its post."""
    def rep(m):
        code = m.group(1)
        if code not in CAT:
            return m.group(0)
        return "[%s](%s)" % (CAT[code]["title"], url(code))
    return re.sub(r"(?<![\w-])([a-z]+(?:-[a-z]+)+)(?![\w-])", rep, text)


def md_escape(text):
    """Text from the catalog into Markdown: keep * _ ` < > [ ] literal, and
    keep Hugo's typographer from turning -- into a dash, >> into a guillemet,
    ... into an ellipsis and the quotes of SQL strings and names into curly
    ones. Apostrophes inside words are written
    curly here, as the typographer would print them, so no entity is left for
    the theme's cards to show."""
    text = text.replace("\\", "\\\\")
    for ch in "*_`<>[]":
        text = text.replace(ch, "\\" + ch)
    text = text.replace("--", "\\-\\-").replace("...", "\\.\\.\\.")
    text = text.replace('"', '\\"')
    text = re.sub(r"(?<=[A-Za-z])'(?=[A-Za-z])", "\u2019", text)
    text = text.replace("'", "\\'")
    # A paragraph must not start like a heading, a quote or a list item.
    if re.match(r"[#>+-]", text):
        text = "\\" + text
    return re.sub(r"^(\d+)([.)] )", r"\1\\\2", text)


def lead_typo(text):
    """Hand-written opening text with its apostrophes and quotation marks
    already curly, outside code spans. The theme's cards print the opening
    as plain text and would show Hugo's &rsquo; and &ldquo; entities literally."""
    out, at = [], 0
    for m in re.finditer(r"(`+).+?(?<!`)\1(?!`)", text):
        out += [curl(text[at:m.start()]), m.group(0)]
        at = m.end()
    return "".join(out + [curl(text[at:])])


def curl(text):
    text = re.sub(r"(?<=[A-Za-z])'(?=[A-Za-z])", "\u2019", text)
    return re.sub(r'"([^"]*)"', "\u201c\\1\u201d", text)


def prose(text):
    return link_codes(md_escape(text))


def answers_table(m):
    out = ["| Engine | Answer |", "|---|---|"]
    for eng, _ in ENGINES:
        if eng in m:
            out.append("| %s | %s |" % (label(eng), cell(m[eng])))
    return "\n".join(out)


def rule_text(code):
    return open(os.path.join(RES, "rules", code + ".txt")).read()


def notes_line(code):
    """The closing note of `baresql rules CODE` on versions and builds."""
    out = []
    for para in rule_text(code).strip().split("\n\n"):
        p = " ".join(para.split())
        if p.startswith("These answers need") or p.startswith("The SQLite answers assume"):
            out.append(p)
    return out


def forms(code):
    """The forms table of `baresql rules CODE`: name, four cells, advice."""
    e = CAT[code]
    items = e.get("items") or []
    if not items:
        return []
    lines = rule_text(code).split("\n")
    hdr = None
    for i, l in enumerate(lines):
        if re.match(r"^\s+SQLite\s+PostgreSQL\s+MySQL\s+MariaDB\s*$", l):
            hdr = i
            break
    assert hdr is not None, code
    cols = [lines[hdr].index(n) for n in ("SQLite", "PostgreSQL", "MySQL", "MariaDB")]
    rows = []
    for it, l in zip(items, lines[hdr + 1:hdr + 1 + len(items)]):
        assert l[:cols[0]].rstrip() == it["name"], (code, it["name"], l)
        rest = l
        adv = it.get("advice", "")
        if adv:
            a = rest.find(adv, cols[3])
        else:
            a = rest.find(" The ", cols[3])
            a = a + 1 if a >= 0 else -1
        if a < 0:
            a = len(rest)
        cells = [rest[cols[0]:cols[1]].strip(), rest[cols[1]:cols[2]].strip(), rest[cols[2]:cols[3]].strip(), rest[cols[3]:a].strip()]
        # The terminal cuts long values; the post shows them whole.
        rec = e["answers"]["items"][it["name"]]
        for k, (eng, _) in enumerate(ENGINES):
            if cells[k] not in ("yes", "no", "?"):
                cells[k] = answer_text(rec[eng])
        rows.append((it["name"], cells, rest[a:].strip()))
    return rows


def forms_table(code):
    rows = forms(code)
    show_advice = any(adv for _, _, adv in rows)
    head = "| Form | SQLite | PostgreSQL | MySQL | MariaDB |" + (" What to write instead |" if show_advice else "")
    out = [head, "|---|---|---|---|---|" + ("---|" if show_advice else "")]
    for name, cells, adv in rows:
        cs = []
        for c in cells:
            if c in ("yes", "no", "?"):
                cs.append(c)
            else:
                cs.append(code_span(c))
        line = "| %s | %s |" % (code_span(name), " | ".join(cs))
        if show_advice:
            line += " %s |" % prose(adv).replace("|", "\\|")
        out.append(line)
    return "\n".join(out)


def sql_block(text):
    text = text.strip()
    if not text.endswith(";"):
        text += ";"
    return "```\n" + text + "\n```"


def same(a, b):
    if ("error" in a) != ("error" in b):
        return False
    if "error" in a:
        return True
    ra, rb = a.get("rows") or [], b.get("rows") or []
    if len(ra) != len(rb):
        return False
    for x, y in zip(ra, rb):
        xs, ys = x.split(", "), y.split(", ")
        if len(xs) != len(ys):
            return False
        for p, q in zip(xs, ys):
            try:
                if not math.isclose(float(p), float(q), rel_tol=1e-9):
                    return False
            except ValueError:
                if p != q:
                    return False
    return True


def image_job(code):
    e = CAT[code]
    ex = e["answers"]["example"]
    groups = []  # list of answers seen, to colour engines that agree alike
    rows = []
    for eng, name in ENGINES:
        rec = ex[eng]
        k = None
        for i, g in enumerate(groups):
            if same(g, rec):
                k = i
                break
        if k is None:
            groups.append(rec)
            k = len(groups) - 1
        rows.append((label(eng), answer_text(rec), "error" in rec, k))
    sql = e["example"].strip()
    lines = sql.split("\n")
    longest = max(len(l) for l in lines)
    size = 26 if longest <= 46 and len(lines) <= 2 else 22 if longest <= 58 and len(lines) <= 3 else 18 if longest <= 72 else 15
    palette = ["#0e7c86", "#b25e09", "#5b4fc7", "#2f6db5"]
    trs = []
    for lab, text, err, k in rows:
        if len(text) > 70:
            text = text[:67] + "..."
        colour = "#b3261e" if err else palette[k % len(palette)]
        trs.append('<tr><td>%s</td><td class="ans" style="color:%s">%s</td></tr>' % (img.esc(lab), colour, img.esc(text)))
    rw = (e.get("rewrite") or "").strip()
    if rw:
        parts = rw.split("\n")
        last = parts[0].rstrip(";") + (" ..." if len(parts) > 1 else "")
        if len(last) > 74:
            last = last[:70] + " ..."
        strip = '<div class="same"><span class="same-k">The same on every engine</span><code>%s</code></div>' % img.esc(last)
    else:
        strip = '<div class="same"><span class="same-k">No form answers the same on all four engines</span></div>'
    body = """<div class="sqlbox" style="font-size:%dpx">%s</div>
<table class="ans-table">%s</table>
%s
<div class="foot"><span>Recorded by BareSQL Portable</span><span>baresql.com</span></div>""" % (
        size, "<br>".join(img.esc(l) for l in lines), "".join(trs), strip)
    css = """
body { padding: 40px 60px 0; }
h1 { font-size: 36px; }
.sub { display: none; }
.content { margin-top: 22px; }
.sqlbox { font-family: "DejaVu Sans Mono", monospace; background: #13223a; color: #fff; padding: 16px 22px; border-radius: 10px; line-height: 1.45; white-space: pre; overflow: hidden; }
.ans-table { margin-top: 16px; border-spacing: 0 7px; font-size: 21px; }
.ans-table td { padding: 9px 18px; }
.ans-table td:first-child { width: 250px; }
.ans { font-family: "DejaVu Sans Mono", monospace; font-weight: 600; font-size: 19px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 770px; }
.foot { position: absolute; left: 60px; right: 60px; bottom: 26px; display: flex; justify-content: space-between; color: #5b6676; font-size: 17px; font-weight: 600; }
.same { margin-top: 14px; display: flex; align-items: center; gap: 16px; font-size: 19px; }
.same-k { color: #0e7c86; font-weight: 700; white-space: nowrap; }
.same code { font-family: "DejaVu Sans Mono", monospace; font-size: 18px; color: #1b2533; background: #e6f4f5; padding: 7px 12px; border-radius: 8px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.kicker { color: #0e7c86; font-size: 17px; font-weight: 700; text-transform: uppercase; letter-spacing: .08em; margin-bottom: 8px; }
"""
    page = img.card(e["title"], "", body, css)
    page = page.replace("<h1>", '<div class="kicker">SQL difference: %s</div><h1>' % img.esc(code), 1)
    alt = "%s, run on four engines: %s." % (alt_span(sql.replace("\n", " ")),
                                             "; ".join("%s %s" % (lab, alt_answer(text)) for lab, text, _, _ in rows))
    return page, alt


def alt_span(text):
    """A code span inside alt text: Hugo writes its text into the alt as it
    stands, without the backticks, so quotes, -- and backslashes survive."""
    n = 1
    while "`" * n in text:
        n += 1
    if text.startswith("`") or text.endswith("`"):
        text = " " + text + " "
    return "`" * n + text + "`" * n


def alt_answer(text):
    if text.startswith("error: "):
        return "error: " + alt_span(text[len("error: "):])
    if text == "no rows":
        return text
    return alt_span(text)


def post(code, alt):
    e = CAT[code]
    title, topic, intro = RULES[code]
    n = len(e.get("items") or [])
    intro = intro.replace("{forms}", str(n))
    imgurl = "https://baresql.com/images/rule-%s.png" % code
    out = ["---", 'title: "%s"' % title, "date: %s" % DATE, "draft: false",
           'tags: ["SQL differences", "%s"]' % topic, 'featured_image: "%s"' % imgurl, "---", ""]
    # The summary is lint's message, quoted further down, so the opening is
    # the hand-written intro and the catalog's explanation.
    out += [lead_typo(intro), "", prose(e["detail"]), ""]
    out += ["![%s](%s)" % (alt, imgurl), ""]

    out += ["## What Each Engine Answered", ""]
    if e.get("setup"):
        out += ["On a table made with", "", sql_block(e["setup"]), "", "the example"]
    else:
        out += ["The example"]
    out += ["", sql_block(e["example"]), "", "gave these answers:", "", answers_table(e["answers"]["example"]), ""]
    for note in notes_line(code):
        out += ["*%s*" % prose(note), ""]

    if n:
        out += ["## Every Form BareSQL Checks", ""]
        out += ["BareSQL Portable checks %d forms of this, and each was run on all four engines. *yes* means the engine ran it and the engines that ran it agreed, *no* means the engine refused it, and a value is what the engine answered where they disagreed." % n, ""]
        out += ['<div style="overflow-x:auto">', "", forms_table(code), "", "</div>", ""]

    out += ["## What to Write Instead", ""]
    if e.get("rewrite"):
        out += ["This form answers the same on every engine:", "", sql_block(e["rewrite"]), "",
                answers_table(e["answers"]["rewrite"]), "", prose(e["advice"]), ""]
    else:
        out += [prose(e["advice"]), ""]

    ex = os.path.join(RES, "examples", code + ".txt")
    if os.path.exists(ex):
        lint = open(ex).read().rstrip("\n")
        out += ["## What BareSQL Portable Reports", "",
                "Saved as example.sql, the example gets this from `baresql lint --all`:", "",
                "```", "$ baresql lint --all example.sql", lint, "```", ""]
        others = sorted(set(re.findall(r"\[([a-z-]+)\]$", lint, re.M)) - {code}, key=ORDER.index)
        if others:
            names = ["[%s](%s)" % (CAT[o]["title"], url(o)) for o in others]
            out += ["The example also runs into %s, which %s a post of %s own." % (
                " and ".join(names), "has" if len(names) == 1 else "have", "its" if len(names) == 1 else "their"), ""]
    out += ["`baresql rules %s` prints the same answers in the terminal, and the [BareSQL Portable page](https://baresql.com/portable/) lists every difference BareSQL knows." % code, ""]
    return "\n".join(out)


TOPICS = [
    ("numbers", "Numbers"), ("text", "Text"), ("dates", "Dates"), ("NULL and sorting", "NULL and Sorting"),
    ("queries", "Queries"), ("inserts and updates", "Inserts and Updates"), ("schemas", "Schemas"),
    ("syntax", "Syntax"), ("functions and operators", "Functions and Operators"),
]


def index_markdown():
    """The rule posts by topic, for the BareSQL Portable page."""
    out = []
    for topic, heading in TOPICS:
        codes = [c for c in ORDER if RULES[c][1] == topic]
        assert codes, topic
        out += ["### " + heading, ""]
        for c in codes:
            e = CAT[c]
            sev = {"error": "fails somewhere", "warning": "answers differ", "info": "depends on the data"}[e["severity"]]
            out.append("- [%s](%s) `%s`, %s" % (RULES[c][0], url(c), c, sev))
        out.append("")
    missing = [c for c in ORDER if RULES[c][1] not in dict(TOPICS)]
    assert not missing, missing
    return "\n".join(out).rstrip("\n")


def main(images=True):
    jobs = []
    os.makedirs(os.path.join(SITE, "content", "posts"), exist_ok=True)
    os.makedirs(os.path.join(SITE, "static", "images"), exist_ok=True)
    only = [c for c in os.environ.get("ONLY", "").split(",") if c]
    for code in ORDER:
        if only and code not in only:
            continue
        page, alt = image_job(code)
        jobs.append((page, os.path.join(SITE, "static", "images", "rule-%s.png" % code)))
        with open(os.path.join(SITE, "content", "posts", code + ".md"), "w") as f:
            f.write(post(code, alt))
    if images:
        img.render(jobs)
    print("wrote %d rule posts" % len(ORDER))


if __name__ == "__main__":
    main("--no-images" not in sys.argv)
