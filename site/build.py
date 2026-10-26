"""Builds baresql.com from site/src and test/results.

Markers in the sources:
    {{v:name}}             a value read from test/results (see values())
    {{file:path}}          a file of test/results, as it is
    {{lines:path:a-b}}     lines a to b (1-based) of such a file
    {{grep:path:regex}}    the lines of such a file that match regex
    {{blocks:path:regex}}  the blank-line separated blocks whose first line
                           matches regex
    {{readme:Heading}}     the first table under that heading of the README
    {{table:audit-facts}}  facts checked exactly, per fixture and version
    {{slug:code}}          the URL path of a rule's post
    {{index:rules}}        the list of rule posts (gen_rules.py)
Writes config.toml, content/pages and content/posts into the site
checkout (see paths.py), the rule posts (gen_rules.py) and the images
(images.py, which need node and Playwright), and copies the sample reports.

    python3 site/build.py [--no-images]

Edit the site here, never in the site checkout: every run overwrites it.
"""
import glob, json, os, re, shutil, sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from paths import SRC, RES, SITE, README, CI  # noqa: E402


def read(path):
    return open(os.path.join(RES, path)).read()


def comma(n):
    return "{:,}".format(int(n))


def values():
    v = {}
    counts = read("portable/counts.txt")
    m = re.search(r"rules: (\d+) \((\d+) errors, (\d+) warnings, (\d+) notes\)", counts)
    v["rules"], v["rule_errors"], v["rule_warnings"], v["rule_notes"] = m.groups()
    v["rows"] = re.search(r"rows in all: (\d+)", counts).group(1)
    for key, name in [("rows_functions", "function-not-everywhere"), ("rows_operators", "operator-not-everywhere"),
                      ("rows_types", "type-not-everywhere"), ("rows_forms", "forms of other rules")]:
        v[key] = re.search(r"rows of %s: (\d+)" % re.escape(name), counts).group(1)
    v["answers"] = comma(re.search(r"recorded answers: (\d+)", counts).group(1))
    v["version_keys"] = re.search(r"answers kept for other versions and builds: (\d+)", counts).group(1)

    scale = read("audit/scale.txt")
    m = re.search(r"(\d+) tables of (\d+) columns, (\d+) logins, values sampled: the audit took ([\d.]+) seconds "
                  r"and found (\d+) readable login-columns \((PostgreSQL [\d.]+)[^,]*, (\d+) CPUs", scale)
    (v["scale_tables"], v["scale_columns"], v["scale_logins"], v["scale_seconds"], found, v["scale_pg"],
     v["scale_cpus"]) = m.groups()
    v["scale_tables"] = comma(v["scale_tables"])
    v["scale_found"] = comma(found)

    ver = read("versions.txt")
    v["recorded"] = re.search(r"recorded: (\S+)", ver).group(1)
    v["engines"] = re.search(r"engines: (.+)", ver).group(1).strip()
    on = json.load(open(os.path.join(RES, "portable", "rules.json")))["recorded_on"]
    v["sqlite"], v["postgres"], v["mysql"], v["mariadb"] = on["sqlite"], on["postgres"], on["mysql"], on["mariadb"]

    v["version"] = re.search(r"binary: baresql (\S+),", ver).group(1)
    v["findings"] = str(len(readme_table("Findings").strip().splitlines()) - 2)

    def count(path, pattern):
        return re.search(pattern, read(path), re.M).group(1)
    v["agent_grew"] = count("audit/agent/diff.txt", r"^Reach that grew: (\d+)")
    v["support_gone"] = count("audit/support/diff.txt", r"^Reach that is gone: (\d+)")
    v["quarterly_grew"] = count("audit/quarterly/diff.txt", r"^Reach that grew: (\d+)")
    v["quarterly_gone"] = count("audit/quarterly/diff.txt", r"^Reach that is gone: (\d+)")
    v["quarterly_fixed_grew"] = count("audit/quarterly/fixed-diff.txt", r"^Reach that grew: (\d+)")
    v["quarterly_fixed_gone"] = count("audit/quarterly/fixed-diff.txt", r"^Reach that is gone: (\d+)")
    if v["quarterly_fixed_grew"] != "0":
        sys.exit("quarterly fix: reach grew, the case study says none did")
    v["quarterly_intern"] = str(len([l for l in read("audit/quarterly/diff.txt").splitlines()
                                     if l.startswith("  + read shop_intern ") and "granted to PUBLIC" not in l]))
    v["sqlite_tests_notes"] = count("portable/sqlite-tests-lint.txt", r"(\d+) notes? not shown")
    m = re.search(r"^(\d+) statements: (\d+) answered alike, (\d+) differ\.", read("portable/sqlite-tests-compare.txt"), re.M)
    v["sqlite_tests_statements"], v["sqlite_tests_alike"], v["sqlite_tests_differ"] = m.groups()
    m = re.search(r"^(\d+) problems: (\d+) errors, (\d+) warnings\. (\d+) notes not shown", read("portable/mysql-to-postgres-lint.txt"), re.M)
    v["m2p_lint_problems"], v["m2p_lint_errors"], v["m2p_lint_warnings"], v["m2p_lint_notes"] = m.groups()
    m = re.search(r"^(\d+) statements: (\d+) answered alike, (\d+) differ\.", read("portable/mysql-to-postgres-compare.txt"), re.M)
    v["m2p_statements"], v["m2p_alike"], v["m2p_differ"] = m.groups()
    v["m2p_refused"] = str(len(re.findall(r"^  refused by ", read("portable/mysql-to-postgres-compare.txt"), re.M)))
    v["m2p_silent"] = str(int(v["m2p_differ"]) - int(v["m2p_refused"]))
    v["sqlite_tests_refused"] = str(len(re.findall(r"^  refused by ", read("portable/sqlite-tests-compare.txt"), re.M)))
    v["sqlite_tests_silent"] = str(int(v["sqlite_tests_differ"]) - int(v["sqlite_tests_refused"]))
    m = re.search(r"^(\d+) problems: (\d+) errors\.", read("portable/sqlite-tests-lint.txt"), re.M)
    v["sqlite_tests_lint_problems"], v["sqlite_tests_lint_errors"] = m.groups()

    for line in read("audit/counts.txt").splitlines():
        m = re.match(r"(\w+): (\d+) logins; (\d+) readable login-columns, (\d+) of them personal data; "
                     r"findings (\d+) high, (\d+) medium, (\d+) low; facts checked exactly: (.+)", line)
        if not m:
            continue
        name = m.group(1)
        v[name + "_logins"], v[name + "_readable"], v[name + "_personal"] = m.group(2), m.group(3), m.group(4)
        v[name + "_high"], v[name + "_medium"], v[name + "_low"] = m.group(5), m.group(6), m.group(7)
        facts = dict((int(b), int(a)) for a, b in re.findall(r"(\d+) on (\d+)", m.group(8)))
        v[name + "_facts"] = m.group(8)
        v[name + "_facts_16"] = str(facts[16])
        v[name + "_facts_max"] = str(max(facts.values()))
        v[name + "_facts_min"] = str(min(facts.values()))
    return v


def readme_table(heading):
    """The first Markdown table after the README heading with this text."""
    lines = open(README).read().split("\n")
    for i, l in enumerate(lines):
        if re.match(r"#+ " + re.escape(heading) + r"$", l):
            break
    else:
        sys.exit("README: no heading %s" % heading)
    out = []
    for l in lines[i + 1:]:
        if l.startswith("#"):
            break
        if l.startswith("|"):
            out.append(l)
        elif out:
            break
    if not out:
        sys.exit("README: no table under %s" % heading)
    return "\n".join(out)


def blocks(path, pattern):
    text = read(path).rstrip("\n")
    out = [b for b in re.split(r"\n\s*\n", text) if re.search(pattern, b.split("\n")[0])]
    if not out:
        sys.exit("%s: no block matches %s" % (path, pattern))
    return "\n\n".join(out)


def grep(path, pattern):
    out = [l for l in read(path).rstrip("\n").split("\n") if re.search(pattern, l)]
    if not out:
        sys.exit("%s: no line matches %s" % (path, pattern))
    return "\n".join(out)


def facts_table(v):
    names = [("shop", "An online shop"), ("saas", "A multi-tenant app"), ("clinic", "A clinic"),
             ("clean", "A database set up with care"), ("edge", "Corner cases from the review")]
    head = "| Database | " + " | ".join("%d" % n for n in range(13, 19)) + " |"
    rows = [head, "|---|" + "---:|" * 6]
    for key, title in names:
        facts = dict((int(b), a) for a, b in re.findall(r"(\d+) on (\d+)", v[key + "_facts"]))
        rows.append("| %s | %s |" % (title, " | ".join(facts[n] for n in range(13, 19))))
    return ('<div style="overflow-x:auto">\n\n' + "\n".join(rows) +
            "\n\n*Facts checked exactly on each PostgreSQL version, 13 to 18.*\n\n</div>")


def fill(text, v, where):
    def rep(m):
        kind, arg = m.group(1), m.group(2)
        if kind == "v":
            if arg not in v:
                sys.exit("%s: no value %s" % (where, arg))
            return v[arg]
        if kind == "file":
            return read(arg).rstrip("\n")
        if kind == "lines":
            path, span = arg.rsplit(":", 1)
            a, b = (int(x) for x in span.split("-"))
            return "\n".join(read(path).rstrip("\n").split("\n")[a - 1:b])
        if kind == "grep":
            path, pattern = arg.split(":", 1)
            return grep(path, pattern)
        if kind == "blocks":
            path, pattern = arg.split(":", 1)
            return blocks(path, pattern)
        if kind == "readme":
            return readme_table(arg)
        if kind == "table" and arg == "audit-facts":
            return facts_table(v)
        if kind == "slug":
            import gen_rules
            if arg not in gen_rules.RULES:
                sys.exit("%s: no rule %s" % (where, arg))
            return gen_rules.slug(gen_rules.RULES[arg][0])
        if kind == "index" and arg == "rules":
            import gen_rules
            return gen_rules.index_markdown()
        sys.exit("%s: unknown marker %s" % (where, m.group(0)))
    out = re.sub(r"\{\{(v|file|lines|grep|blocks|readme|table|slug|index):(.+?)\}\}", rep, text)
    if "\n---\n" in out and "\n![" in out and where in POSTS:
        # The opening paragraphs, up to the image, are what the theme's cards show.
        import gen_rules
        head, rest = out.split("\n![", 1)
        fm, body = head.split("\n---\n", 1)
        out = fm + "\n---\n" + gen_rules.lead_typo(body) + "\n![" + rest
    if "{{" in out:
        sys.exit("%s: a marker is left: %s" % (where, out[out.index("{{"):][:60]))
    return out


POSTS = set(os.path.basename(p) for p in glob.glob(os.path.join(SRC, "posts", "*.md")))


def main():
    if not os.path.isdir(os.path.join(SITE, "themes", "launch")):
        sys.exit("no checkout of the baresql.com site at %s (set BARESQL_SITE)" % SITE)
    images = "--no-images" not in sys.argv
    v = values()
    # Start from empty content folders, so a source that was removed leaves no page behind.
    for kind in ("pages", "posts"):
        for old in glob.glob(os.path.join(SITE, "content", kind, "*.md")):
            os.remove(old)
    cfg = open(os.path.join(SRC, "config.toml")).read()
    cfg = cfg.replace("FIG_RULES", v["rules"]).replace("FIG_ANSWERS", v["answers"])
    cfg = cfg.replace("FIG_SCALE", v["scale_seconds"] + " s")
    ci = open(CI).read()
    pg = re.findall(r'"(\d+)"', re.search(r"postgres:.*?version: \[([^\]]*)\]", ci, re.S).group(1))
    my = re.findall(r"engine: (?:mysql|mariadb), version:", ci)
    cfg = cfg.replace("FIG_VERSIONS", str(len(pg) + len(my)))
    cfg = cfg.replace("FIG_VERSION", v["version"]).replace("FIG_TABLES", v["scale_tables"])
    cfg = cfg.replace("FIG_LOGINS", v["scale_logins"]).replace("FIG_CPUS", v["scale_cpus"])
    if "FIG_" in cfg:
        sys.exit("config.toml: a FIG_ value is left")
    cfg = cfg.replace("FIGURES_NOTE", "Answers recorded on SQLite %s, PostgreSQL %s, MySQL %s and MariaDB %s; the audit timing on PostgreSQL %s, %s CPUs. The recorded runs are in the code repository on GitHub." % (
        v["sqlite"], v["postgres"], v["mysql"], v["mariadb"], v["scale_pg"].split()[1], v["scale_cpus"]))
    open(os.path.join(SITE, "config.toml"), "w").write(cfg)
    for kind in ("pages", "posts"):
        os.makedirs(os.path.join(SITE, "content", kind), exist_ok=True)
        for src in sorted(glob.glob(os.path.join(SRC, kind, "*.md"))):
            name = os.path.basename(src)
            open(os.path.join(SITE, "content", kind, name), "w").write(fill(open(src).read(), v, name))
    # Sample reports, as BareSQL wrote them.
    for name in ("shop", "saas", "clinic", "clean"):
        dst = os.path.join(SITE, "static", "samples", name)
        os.makedirs(dst, exist_ok=True)
        for f in ("report.html", "audit.json"):
            shutil.copy(os.path.join(RES, "audit", name, f), os.path.join(dst, f))
    for case, parts in (("agent", ("before", "after")), ("support", ("before", "after")), ("quarterly", ("q3", "q4", "fixed"))):
        for part in parts:
            dst = os.path.join(SITE, "static", "samples", case + "-" + part)
            os.makedirs(dst, exist_ok=True)
            for f in ("report.html", "audit.json"):
                shutil.copy(os.path.join(RES, "audit", case, part, f), os.path.join(dst, f))
    print("built: %d values" % len(v))
    import gen_rules
    gen_rules.main(images)
    if images:
        import images as pictures
        pictures.main()


if __name__ == "__main__":
    main()
