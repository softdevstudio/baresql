"""Images for baresql.com, 1200 x 630, drawn as HTML and rendered by Chromium."""
import html, json, os, subprocess, tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
BASE_CSS = """
* { margin: 0; padding: 0; box-sizing: border-box; }
html, body { width: 1200px; height: 630px; overflow: hidden; }
body { font-family: Inter, sans-serif; -webkit-font-smoothing: antialiased; }
"""

def esc(s):
    return html.escape(s, quote=False)

def terminal(title, lines, size=19):
    """lines: (kind, text) with kind cmd, out, dim, ok, warn, err, head, gap."""
    rows = []
    for kind, text in lines:
        if kind == "gap":
            rows.append('<div class="l gap"></div>')
            continue
        t = esc(text)
        if kind == "cmd":
            t = '<span class="p">$</span> <b>' + t + "</b>"
        elif kind == "cont":
            t = "<b>  " + t + "</b>"
        rows.append('<div class="l %s">%s</div>' % (kind, t or "&nbsp;"))
    return """<!doctype html><html><head><meta charset="utf-8"><style>%s
body { background: #13223a; color: #dfe6ee; }
.bar { height: 44px; background: #0e1a2d; display: flex; align-items: center; padding: 0 22px; position: relative; }
.dot { width: 13px; height: 13px; border-radius: 50%%; margin-right: 9px; }
.t { position: absolute; left: 0; right: 0; text-align: center; color: #8a97ab; font-size: 15px; }
.body { padding: 22px 34px; font-family: "DejaVu Sans Mono", monospace; font-size: %dpx; line-height: 1.58; }
.l { white-space: pre; overflow: hidden; text-overflow: ellipsis; }
.gap { height: 10px; }
.p { color: #46c7c3; font-weight: bold; }
b { color: #ffffff; }
.dim { color: #8592a7; }
.ok { color: #79d48f; }
.warn { color: #f0b85a; }
.err { color: #ff8a80; }
.head { color: #ffffff; font-weight: bold; }
</style></head><body>
<div class="bar"><span class="dot" style="background:#ee6a5f"></span><span class="dot" style="background:#f5bf4f"></span><span class="dot" style="background:#61c454"></span><span class="t">%s</span></div>
<div class="body">%s</div></body></html>""" % (BASE_CSS, size, esc(title), "\n".join(rows))

def card(title, subtitle, body_html, extra_css=""):
    return """<!doctype html><html><head><meta charset="utf-8"><style>%s
body { background: #ffffff; color: #1b2533; padding: 48px 60px; }
h1 { font-size: 40px; font-weight: 700; letter-spacing: -0.5px; line-height: 1.15; }
.sub { font-size: 21px; color: #5b6676; margin-top: 10px; line-height: 1.4; }
.content { margin-top: 30px; }
code, .mono { font-family: "DejaVu Sans Mono", monospace; }
table { border-collapse: separate; border-spacing: 0 8px; width: 100%%; font-size: 19px; }
td { padding: 9px 16px; background: #f3f6f9; vertical-align: middle; }
td:first-child { border-radius: 8px 0 0 8px; font-weight: 600; width: 190px; }
td:last-child { border-radius: 0 8px 8px 0; }
.sql { font-family: "DejaVu Sans Mono", monospace; font-size: 22px; background: #13223a; color: #ffffff; padding: 14px 20px; border-radius: 10px; display: inline-block; }
.no { color: #b23b30; font-weight: 600; }
.yes { color: #0e7c86; font-weight: 600; }
.pill { display: inline-block; padding: 3px 12px; border-radius: 14px; font-size: 16px; font-weight: 600; color: #fff; }
%s
</style></head><body><h1>%s</h1><div class="sub">%s</div><div class="content">%s</div></body></html>""" % (BASE_CSS, extra_css, esc(title), esc(subtitle), body_html)

def render(jobs):
    """jobs: list of (html_text, out_path) or (html_text, out_path, extra)."""
    tmp = tempfile.mkdtemp()
    spec = []
    for i, job in enumerate(jobs):
        text, out = job[0], job[1]
        extra = job[2] if len(job) > 2 else {}
        path = os.path.join(tmp, "%d.html" % i)
        open(path, "w").write(text)
        d = {"html": path, "out": out}
        d.update(extra)
        spec.append(d)
    sp = os.path.join(tmp, "jobs.json")
    json.dump(spec, open(sp, "w"))
    subprocess.run(["node", os.path.join(HERE, "render.js"), sp], check=True)
