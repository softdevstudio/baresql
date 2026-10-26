"""Checks the baresql.com checkout that build.py wrote: the owner's post
rules on the content, then a Hugo build of it in a temporary folder (never
public/ in the checkout).

    python3 site/check.py

Needs hugo. Prints each problem and exits 1 when there is one.
"""
import glob, html, os, re, shutil, subprocess, sys, tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from paths import SITE  # noqa: E402

problems = []


def bad(where, what):
    problems.append("%s: %s" % (os.path.relpath(where, SITE), what))


def check_posts():
    for f in sorted(glob.glob(os.path.join(SITE, "content", "posts", "*.md"))):
        s = open(f).read()
        m = re.match(r"---\n(.*?)\n---\n(.*)", s, re.S)
        if not m:
            bad(f, "no front matter")
            continue
        fm, body = m.groups()
        keys = [l.split(":")[0] for l in fm.split("\n") if l and not l.startswith(" ")]
        if keys != ["title", "date", "draft", "tags", "featured_image"]:
            bad(f, "front matter fields %s" % keys)
        t = re.search(r'^title: "(.*)"$', fm, re.M)
        title = t.group(1) if t else ""
        if not re.fullmatch(r"[A-Za-z0-9 -]+", title):
            bad(f, "title %r" % title)
        if not re.search(r"^date: \d{4}-\d\d-\d\d$", fm, re.M) or "draft: false" not in fm:
            bad(f, "date or draft")
        fi = re.search(r'^featured_image: "https://baresql.com/images/([^"]+)"$', fm, re.M)
        if not fi or not os.path.exists(os.path.join(SITE, "static", "images", fi.group(1))):
            bad(f, "featured_image missing")
            continue
        images = re.findall(r"!\[[^\]]*\]\(([^)]+)\)", body)
        if images != ["https://baresql.com/images/" + fi.group(1)]:
            bad(f, "inline images %s" % images)
        blocks = [b for b in re.split(r"\n\s*\n", body.split("![")[0].strip()) if b.strip()]
        kinds = ["code" if b.startswith("```") else "heading" if b.startswith("#") else "p" for b in blocks]
        if kinds not in (["p", "p"], ["p", "p", "p"]):
            bad(f, "the image should follow paragraph 2 or 3, not %s" % kinds)
        if title and title.lower() in body.lower():
            bad(f, "the title is repeated in the body")
        check_text(f, s)


def check_pages():
    for f in sorted(glob.glob(os.path.join(SITE, "content", "pages", "*.md"))):
        s = open(f).read()
        fm = re.match(r"---\n(.*?)\n---\n", s, re.S)
        keys = [l.split(":")[0] for l in fm.group(1).split("\n") if l] if fm else []
        if not fm or not set(keys) <= {"title", "type", "draft", "url"} or "type: page" not in s or "draft: false" not in s:
            bad(f, "page front matter %s" % keys)
        check_text(f, s)


def check_text(f, s):
    if re.search("[\u2013\u2014]", s):
        bad(f, "an en or em dash")
    for link in re.findall(r"\]\((?!https?://|mailto:)([^)]+)\)", s):
        bad(f, "relative link %s" % link)
    for link in re.findall(r"\]\((https?://(?:www\.)?baresql\.com[^)]*)\)", s):
        if not link.startswith("https://baresql.com/"):
            bad(f, "link %s" % link)


def check_build():
    out = tempfile.mkdtemp()
    try:
        subprocess.run(["hugo", "--quiet", "-s", SITE, "-d", out], check=True)
        for f in glob.glob(os.path.join(out, "**", "*.html"), recursive=True):
            s = open(f, errors="replace").read()
            where = os.path.join(SITE, "public", os.path.relpath(f, out))
            # The theme prints each card's opening as plain text, so an
            # entity there shows literally.
            for m in re.finditer(r'<p class="card-text">(.*?)</p>', s, re.S):
                if re.search(r"&amp;[a-z#0-9]+;", m.group(1)):
                    bad(where, "a card shows an entity: %s" % m.group(1)[:90])
            for m in re.finditer(r"<(code|pre)[^>]*>(.*?)</\1>", s, re.S):
                if re.search(r"&[lr][sd]quo;|&ndash;|&mdash;|&hellip;|[\u2018\u2019\u201c\u201d\u2013\u2014\u2026]", m.group(2)):
                    bad(where, "Hugo changed code: %s" % m.group(2)[:90])
            if re.search(r"&ndash;|&mdash;|[\u2013\u2014]", s):
                bad(where, "an en or em dash")
            # In a post or page, a guillemet is Hugo's typographer at work on
            # >>, and in a rule post an ellipsis is its work on the ... of SQL.
            # (Cards end in the theme's own ellipsis, pagers use guillemets.)
            article = re.search(r"<article>(.*?)</article>", s, re.S)
            if article:
                marks = r"&[lr]aquo;|[\u00ab\u00bb]"
                if "/tags/sql-differences/" in article.group(1):
                    marks += r"|&hellip;|\u2026"
                for m in re.finditer(marks, article.group(1)):
                    bad(where, "Hugo's typographer made %s of: %s" % (m.group(0), article.group(1)[max(0, m.start() - 50):m.start()]))
            for m in re.finditer(r'(?:href|src)="([^"]+)"', s):
                u = html.unescape(m.group(1))
                if u.startswith("https://baresql.com/"):
                    path = u[len("https://baresql.com"):]
                elif u.startswith("/") and not u.startswith("//"):
                    path = u
                else:
                    continue
                path = path.split("#")[0].split("?")[0]
                target = os.path.join(out, path.lstrip("/"))
                if path.endswith("/"):
                    target = os.path.join(target, "index.html")
                if not os.path.exists(target):
                    bad(where, "broken link %s" % u)
    finally:
        shutil.rmtree(out)
        lock = os.path.join(SITE, ".hugo_build.lock")
        if os.path.exists(lock):
            os.remove(lock)


check_posts()
check_pages()
check_build()
for p in problems:
    print(p)
print("%d problems" % len(problems))
sys.exit(1 if problems else 0)
