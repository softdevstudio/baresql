# baresql.com

The site at github.com/photozeitgeist/baresql.com is generated from here:
`src/` holds the hand-written pages, posts and config.toml with markers
such as `{{v:rules}}` and `{{file:audit/shop/summary.txt}}` (listed in
build.py), `rules_site.py` holds the title, tag and opening paragraph of
each rule post, and everything else comes from `test/results` and the
README.

    git clone https://github.com/photozeitgeist/baresql.com ../baresql.com
    python3 site/build.py      # writes the checkout; --no-images skips the images
    python3 site/check.py      # post rules, then a Hugo build in a temp folder

The images need node and Playwright with Chromium. Edit the site here, never
in the checkout's content: every run of build.py overwrites it. The site
repo holds content and config values only; the Launch theme is never
changed.
