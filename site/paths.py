"""Where the site generator reads and writes.

The code repository holds the site's sources (site/src), this generator and
test/results. The site itself is a checkout of
github.com/photozeitgeist/baresql.com, by default next to the code
repository; set BARESQL_SITE to use another one."""
import os

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
SRC = os.path.join(HERE, "src")
RES = os.path.join(REPO, "test", "results")
README = os.path.join(REPO, "README.md")
CI = os.path.join(REPO, ".github", "workflows", "ci.yml")
SITE = os.environ.get("BARESQL_SITE") or os.path.join(os.path.dirname(REPO), "baresql.com")
