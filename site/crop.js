// node crop.js jobs.json: screenshots of part of a page.
// Each job: {url, out, width, height, scale, selector?, heading?, above?, left?}
// The clip starts `above` px above the element (a CSS selector, or an h2/h3
// with that text), or at the top of the page, and is `height` px tall.
const { chromium } = require('playwright');
const fs = require('fs');
(async () => {
  const jobs = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  const b = await chromium.launch();
  for (const j of jobs) {
    const p = await b.newPage({ viewport: { width: j.width, height: j.height }, deviceScaleFactor: j.scale || 1 });
    await p.goto(j.url, { waitUntil: 'load' });
    if (j.css) await p.addStyleTag({ content: j.css });
    let y = 0;
    if (j.selector || j.heading) {
      const el = j.selector ? p.locator(j.selector).first() : p.locator('h2, h3', { hasText: j.heading }).first();
      const box = await el.boundingBox();
      if (!box) throw new Error('not found: ' + (j.selector || j.heading) + ' in ' + j.url);
      y = Math.max(0, box.y - (j.above || 0));
    }
    await p.screenshot({ path: j.out, fullPage: true, clip: { x: j.left || 0, y, width: j.width - (j.left || 0), height: j.height } });
    await p.close();
  }
  await b.close();
})();
