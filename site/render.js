// node render.js jobs.json: each job {html: path, out: path, width, height}
const { chromium } = require('playwright');
const fs = require('fs');
(async () => {
  const jobs = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  const b = await chromium.launch();
  for (const j of jobs) {
    const p = await b.newPage({ viewport: { width: j.width || 1200, height: j.height || 630 }, deviceScaleFactor: 1 });
    await p.goto('file://' + j.html);
    await p.waitForTimeout(50);
    if (j.clip) {
      await p.screenshot({ path: j.out, clip: j.clip });
    } else {
      await p.screenshot({ path: j.out });
    }
    await p.close();
  }
  await b.close();
})();
