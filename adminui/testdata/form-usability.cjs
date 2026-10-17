// Real embedded UI, synthetic loopback API only. No Go build or live credentials.
// CMS_PLAYWRIGHT_MODULE=/existing/package.json CMS_BROWSER_CHANNEL=msedge \
// CMS_UI_ARTIFACTS=/tmp/cms-ui node adminui/testdata/form-usability.cjs
const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const { createRequire } = require('node:module');

async function main() {
  if (!process.env.CMS_PLAYWRIGHT_MODULE) throw new Error('CMS_PLAYWRIGHT_MODULE is required; reuse an installed Playwright');
  const { chromium } = createRequire(path.resolve(process.env.CMS_PLAYWRIGHT_MODULE))('playwright');
  const staticDir = process.env.CMS_UI_STATIC_DIR || path.resolve(__dirname, '../static');
  const artifacts = process.env.CMS_UI_ARTIFACTS;
  const measureOnly = process.argv.includes('--measure-only');
  if (artifacts) fs.mkdirSync(artifacts, { recursive: true });
  const originalBody = "<section><h2>Voice &amp; music</h2><p>Source 'quotes' and spacing.</p></section>\n<section><h2>Calendar</h2><div data-event-list></div></section>";
  const original = { ID: 2, Version: 7, Title: 'A long page title for practical editorial work', Path: '/voice', Status: 'published', TemplateID: 'studio', PublishAt: '2026-11-02T15:00:00Z', UnpublishAt: null, BodyHTML: originalBody, BodyBlocks: { type: 'retained-fixture' } };
  let stored = structuredClone(original), readOnly = false, conflict = false, failPlatform = false;
  const requests = [], errors = [], external = [], measurements = [];
  const tenants = [{ ID: 1, Slug: 'fixture', Label: 'Fixture site with a long editorial label', ThemeID: 'default' }];
  let domains = [{ ID: 3, Host: 'long-review-hostname.example.test', SubsiteLabel: 'Review', Kind: 'preview' }];
  const server = http.createServer(async (req, res) => {
    try {
      const url = new URL(req.url, 'http://127.0.0.1');
      const json = (code, body) => { res.writeHead(code, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(body)); };
      if (url.pathname.startsWith('/api/')) {
        const chunks = []; for await (const chunk of req) chunks.push(chunk);
        const body = chunks.length ? JSON.parse(Buffer.concat(chunks)) : null;
        requests.push({ method: req.method, path: url.pathname, body });
        if (req.method === 'GET' && url.pathname.endsWith('/permissions')) return json(200, { create: !readOnly, edit: !readOnly, delete: !readOnly });
        if (req.method === 'GET' && url.pathname.endsWith('/templates')) return json(200, { templates: ['studio', 'classical', 'metal'] });
        if (url.pathname === '/api/v1/admin/tenants') {
          if (req.method === 'GET') return json(200, { tenants });
          if (failPlatform) return json(422, { error: 'The tenant identifier is already in use. Choose another identifier and try again.' });
          const tenant = { ID: 9, Slug: body.slug, Label: body.label, ThemeID: body.theme_id }; tenants.push(tenant); return json(201, tenant);
        }
        if (url.pathname.endsWith('/domains')) {
          if (req.method === 'GET') return json(200, { domains });
          if (failPlatform) return json(422, { error: 'This hostname is already registered to another fixture site.' });
          const domain = { ID: 4, Host: body.host, SubsiteLabel: body.subsite_label, Kind: body.kind }; domains.push(domain); return json(201, domain);
        }
        if (/\/domains\/\d+$/.test(url.pathname) && req.method === 'DELETE') { domains = domains.filter(d => d.ID !== Number(url.pathname.split('/').pop())); res.writeHead(204); return res.end(); }
        if (url.pathname.endsWith('/preview')) { res.writeHead(200, { 'Content-Type': 'text/html' }); return res.end('<!doctype html><title>Fixture preview</title>' + body.body_html); }
        if (url.pathname === '/api/v1/admin/reload') return json(200, { ok: true });
        if (url.pathname === '/api/v1/admin/tenants/1/pages' && req.method === 'GET') return json(200, { pages: [stored] });
        if (req.method === 'POST' && url.pathname.endsWith('/pages')) {
          stored = { ID: 2, Version: 1, ...Object.fromEntries(Object.entries(body).map(([k,v]) => [({title:'Title',path:'Path',status:'Status',template_id:'TemplateID',body_html:'BodyHTML',body_blocks:'BodyBlocks',publish_at:'PublishAt',unpublish_at:'UnpublishAt'})[k],v])) }; return json(201, stored);
        }
        if (url.pathname === '/api/v1/admin/tenants/1/pages/2') {
          if (req.method === 'GET') return json(200, stored);
          if (req.method === 'PUT') {
            if (conflict || body.expected_version !== stored.Version) return json(409, { error: 'version_conflict' });
            stored = { ...stored, Version: stored.Version + 1, Title: body.title, Path: body.path, Status: body.status, TemplateID: body.template_id, BodyHTML: body.body_html, BodyBlocks: body.body_blocks, PublishAt: body.publish_at, UnpublishAt: body.unpublish_at };
            return json(200, stored);
          }
        }
        return json(404, { error: 'Unknown fixture API' });
      }
      if (url.pathname === '/embedded') {
        res.writeHead(200, { 'Content-Type': 'text/html' });
        return res.end('<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><title>Embedded fixture</title><style>body{margin:0;padding:16px}iframe{width:100%;height:1000px;border:0}</style><iframe title="Embedded editor" src="/editor/tenants/1/pages/2/html"></iframe>');
      }
      const base = url.pathname.startsWith('/platform') ? '/platform' : '/editor';
      const name = path.basename(url.pathname);
      const asset = ['admin.css', 'admin.js', 'purify.min.js'].includes(name) ? name : base === '/platform' ? 'platform.html' : 'index.html';
      res.writeHead(200, { 'Content-Type': asset.endsWith('.css') ? 'text/css' : asset.endsWith('.js') ? 'text/javascript' : 'text/html' });
      res.end(fs.readFileSync(path.join(staticDir, asset), 'utf8').replaceAll('{{.BasePath}}', base));
    } catch (error) { res.writeHead(500); res.end(error.message); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = 'http://127.0.0.1:' + server.address().port;
  const browser = await chromium.launch({ headless: true, ...(process.env.CMS_BROWSER_CHANNEL ? { channel: process.env.CMS_BROWSER_CHANNEL } : {}) });
  const checks = [];
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, timezoneId: 'America/New_York', serviceWorkers: 'block' });
    await context.route('**/*', route => {
      if (new URL(route.request().url()).origin !== origin) { external.push(route.request().url()); return route.abort(); }
      return route.continue();
    });
    const page = await context.newPage(); page.setDefaultTimeout(10000);
    page.on('pageerror', e => errors.push(e.message));
    page.on('dialog', d => d.type() === 'beforeunload' ? d.accept() : d.dismiss());
    const go = async route => { await page.goto(origin + route); await page.locator('main > .view:not([hidden]) h2').first().waitFor(); await page.waitForFunction(() => document.querySelector('#route-error').hidden); };
    const pane = async name => { await page.locator('#page-tabs').getByRole('link', { name, exact: true }).click(); await page.locator(`[data-page-pane="${name === 'HTML' ? 'content' : name.toLowerCase()}"]`).waitFor(); };
    const save = async status => {
      const response = page.waitForResponse(r => r.url().endsWith('/pages/2') && r.request().method() === 'PUT');
      await page.getByRole('button', { name: 'Save page', exact: true }).click();
      assert.equal((await response).status(), status);
      await page.waitForFunction(() => !document.querySelector('#btn-save-page').disabled);
    };
    const capture = async (label, root = page) => {
      const m = await root.evaluate(() => {
        const rect = el => { const r = el.getBoundingClientRect(); return { x:r.x, y:r.y, width:r.width, height:r.height, right:r.right }; };
        const source = document.querySelector('[data-editor-source]');
        const visible = el => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
        return { viewport: innerWidth, document: document.documentElement.scrollWidth, source: source && visible(source) ? rect(source) : null, content: source && visible(source) ? rect(document.querySelector('.content-layout')) : null, controls: [...document.querySelectorAll('input:not([type=hidden]),select,textarea,button')].filter(visible).map(el => ({ name:el.name || el.dataset.eventField || el.textContent.trim(), localScroll: !!el.closest('.table-wrap'), ...rect(el) })) };
      });
      m.label = label; measurements.push(m);
      if (!measureOnly) {
        assert.ok(m.document <= m.viewport + 1, label + ' has no document overflow: ' + JSON.stringify(m));
        if (m.source) { assert.ok(m.source.width >= m.content.width - 2, label + ' source fills available content'); assert.ok(m.source.height >= 320, label + ' source has working height'); }
        for (const control of m.controls.filter(c => !c.localScroll)) assert.ok(control.right <= m.viewport + 1 && control.x >= 0, label + ' control fits: ' + control.name);
      }
      if (artifacts && root === page) await page.screenshot({ path: path.join(artifacts, label + '.png'), fullPage: true });
      return m;
    };
    for (const width of [1440, 768, 375, 320]) {
      await page.setViewportSize({ width, height: 1000 });
      await go('/editor/tenants/1/pages/2/html'); await capture('html-' + width);
      await pane('Publishing'); await capture('publishing-' + width);
      await pane('Appearance'); await capture('appearance-' + width);
      await pane('Content'); await page.getByRole('button', { name: 'Event', exact: true }).click(); await capture('event-' + width);
      await go('/platform/tenants/new'); await capture('tenant-' + width);
      await go('/platform/tenants/1/domains'); await capture('domain-' + width);
      await go('/platform/cache'); await capture('cache-' + width);
      await page.goto(origin + '/embedded'); const frame = page.frameLocator('iframe'); await frame.locator('[data-editor-source]').waitFor();
      await capture('embedded-' + width, page.frames()[1]);
    }
    checks.push('all CMS forms: desktop/tablet/375px/320px and embedded HTML geometry');
    if (!measureOnly) {
      await page.setViewportSize({ width: 1440, height: 1000 });
      await go('/editor/tenants/1/pages/2/html');
      const source = page.getByRole('textbox', { name: 'Page HTML source', exact: true });
      await source.scrollIntoViewIfNeeded();
      const before = await source.boundingBox();
      await page.mouse.move(before.x + before.width - 3, before.y + before.height - 3); await page.mouse.down(); await page.mouse.move(before.x + before.width - 3, before.y + before.height + 120, { steps: 6 }); await page.mouse.up();
      assert.ok((await source.boundingBox()).height > before.height + 50, 'native vertical resize grows source');
      await source.focus(); await page.keyboard.press('ControlOrMeta+a');
      assert.equal(await source.evaluate(el => el.selectionEnd - el.selectionStart), originalBody.length);
      assert.equal(await source.evaluate(el => getComputedStyle(el).outlineStyle), 'solid');
      await save(200); assert.deepEqual(stored.BodyBlocks, original.BodyBlocks); assert.equal(stored.BodyHTML, originalBody);
      const advanced = '<!doctype html>\n<form><input required name="private"></form>\n<script>window.fixtureUnsafe=true</script>\n' + originalBody + '\n<!-- exact spacing preserved -->';
      await source.fill(advanced); await pane('Appearance'); await pane('Publishing'); await pane('HTML'); assert.equal(await source.inputValue(), advanced);
      await pane('Content'); assert.equal(await page.locator('[data-editor-surface]').getAttribute('contenteditable'), 'false'); assert.equal(await page.evaluate(() => window.fixtureUnsafe), undefined);
      await pane('HTML'); await save(200); assert.equal(stored.BodyHTML, advanced); assert.equal(stored.BodyBlocks, null); assert.equal(stored.Path, original.Path); assert.equal(stored.TemplateID, original.TemplateID); assert.equal(Date.parse(stored.PublishAt), Date.parse(original.PublishAt));
      await page.reload(); await source.waitFor(); assert.equal(await source.inputValue(), advanced);
      const draft = advanced + '\n<p>Unsaved draft</p>'; await source.fill(draft); conflict = true; await save(409);
      assert.equal(await source.inputValue(), draft); assert.match(await page.locator('#save-state').innerText(), /Unsaved/);
      assert.match(await page.locator('#toast').innerText(), /draft is still here/);
      await page.waitForTimeout(3700); assert.equal(await page.locator('#toast').isVisible(), true, 'errors remain until dismissed');
      await page.locator('#btn-save-page').focus(); await page.keyboard.press('Tab');
      for(let i=0;i<40&&!(await page.getByRole('button', { name: 'Dismiss notification' }).evaluate(el=>el===document.activeElement));i++)await page.keyboard.press('Tab');
      assert.deepEqual(await page.getByRole('button', { name: 'Dismiss notification' }).evaluate(el => ({ focused:el===document.activeElement,visible:el.matches(':focus-visible'),color:getComputedStyle(el).outlineColor,style:getComputedStyle(el).outlineStyle,width:getComputedStyle(el).outlineWidth })), {focused:true,visible:true,color:'rgb(255, 255, 255)',style:'solid',width:'3px'});
      await page.getByRole('button', { name: 'Dismiss notification' }).click(); assert.equal(await page.locator('#toast').isVisible(), false);
      await pane('Publishing'); await page.locator('input[name=title]').fill(''); const count = requests.length; await page.getByRole('button', { name: 'Save page', exact: true }).click();
      assert.equal(await page.locator('input[name=title]').evaluate(el => el === document.activeElement && !el.validity.valid), true); assert.equal(requests.length, count);
      conflict = false; stored = structuredClone(original);
      await go('/editor/tenants/1/pages/2/publishing');
      assert.match(await page.locator('#schedule-zone').innerText(), /America\/New_York/);
      assert.equal(await page.locator('[name=publish_at]').inputValue(), '2026-11-02T10:00');
      await page.locator('[name=publish_at]').fill('2026-11-03T10:30'); await save(200); assert.equal(stored.PublishAt, '2026-11-03T15:30:00.000Z');
      await pane('Preview'); assert.equal(await page.locator('#page-preview').getAttribute('sandbox'), 'allow-same-origin');
      assert.equal(await page.locator('#page-preview').getAttribute('referrerpolicy'), 'no-referrer');
      await pane('HTML'); await source.fill('Draft retained on cancelled navigation');
      await page.getByRole('link', { name: 'All pages', exact: true }).click(); assert.equal(await source.inputValue(), 'Draft retained on cancelled navigation'); assert.match(page.url(), /\/html$/);
      readOnly = true; await go('/editor/tenants/1/pages/2/html'); assert.equal(await source.getAttribute('readonly'), ''); assert.equal(await page.locator('#btn-save-page').isVisible(), false);
      assert.equal(await page.locator('#new-tenant-form').count(), 0); assert.equal(requests.some(r => r.method === 'POST' && (r.path.endsWith('/domains') || r.path === '/api/v1/admin/tenants')), false);
      readOnly = false;
      await go('/editor/tenants/1/pages/new/publishing');
      await page.locator('[name=title]').fill('New fixture page'); await page.locator('[name=path]').fill('/new-fixture');
      await pane('HTML'); await source.fill('<h1>New draft page</h1>');
      const created = page.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/pages'));
      await page.getByRole('button', { name: 'Save page', exact: true }).click(); assert.equal((await created).status(), 201);
      await page.waitForURL('**/pages/2/content'); await pane('HTML'); await page.reload(); await source.waitFor();
      assert.equal(await source.inputValue(), '<h1>New draft page</h1>'); assert.equal(stored.Status, 'draft'); assert.equal(stored.Path, '/new-fixture');
      await pane('Content'); await page.getByRole('button', { name: 'Event', exact: true }).click();
      const event = page.locator('[data-event-tool]'); await event.getByRole('button', { name: 'Apply event to page' }).click(); await page.locator('#toast.error').waitFor();
      const values = { id:'fixture-show',title:'Synthetic event',project:'Fixture ensemble',persona:'classical',start:'2026-11-03T19:00:00-05:00',end:'2026-11-03T21:00:00-05:00',timezone:'America/New_York',venue:'Fixture hall',city:'Fixture city',status:'confirmed',source:'https://example.test/source',verified:'2026-10-17' };
      for (const [key,value] of Object.entries(values)) await event.locator('[data-event-field="'+key+'"]').fill(value);
      await event.getByRole('button', { name: 'Apply event to page' }).click();
      await page.locator('#section-tabs').getByRole('link', { name: 'Synthetic event', exact: true }).click(); await page.locator('[data-event-id=fixture-show]').waitFor();
      await save(200); assert.match(stored.BodyHTML, /data-event-timezone="America\/New_York"/); assert.match(stored.BodyHTML, /2026-11-03T19:00:00-05:00/);
      await go('/platform/tenants/new'); const tenant = page.locator('#new-tenant-form'); await tenant.getByRole('button', { name: 'Create tenant' }).click(); assert.equal(await tenant.locator('[name=slug]').evaluate(el => el === document.activeElement), true);
      await tenant.locator('[name=slug]').fill('new-fixture'); await tenant.locator('[name=label]').fill('New fixture tenant'); failPlatform = true;
      await tenant.getByRole('button', { name: 'Create tenant' }).click(); await page.locator('#toast.error').waitFor(); assert.equal(await tenant.locator('[name=slug]').inputValue(), 'new-fixture'); failPlatform = false;
      await tenant.getByRole('button', { name: 'Create tenant' }).click(); await page.locator('#tenants-section').waitFor(); assert.equal(tenants.at(-1).Slug, 'new-fixture');
      await go('/platform/tenants/1/domains'); const domain = page.locator('#new-domain-form'); await domain.locator('[name=host]').fill('new-review.example.test'); await domain.locator('[name=kind]').selectOption('preview'); await domain.getByRole('button', { name: 'Add domain' }).click(); await page.getByRole('cell', { name: 'new-review.example.test', exact: true }).waitFor(); assert.equal(await domain.locator('[name=host]').inputValue(), '');
      assert.equal(await page.locator('[data-editor-source]').count(), 0);
      await page.setViewportSize({ width: 320, height: 1000 }); failPlatform = true; await domain.locator('[name=host]').fill('duplicate.example.test'); await domain.getByRole('button', { name: 'Add domain' }).click(); await page.locator('#toast.error').waitFor(); await capture('error-320');
      checks.push('native textarea resize, keyboard selection/focus', 'exact advanced HTML and structured-body preservation', 'save/reload, unsaved-navigation guard, CAS conflict draft retention', 'persistent dismissible errors and native required-field focus', 'browser-local publishing dates roundtrip to UTC', 'preview sandbox unchanged', 'read-only and editor/platform separation', 'new page create/reload and event validation/offset preservation', 'tenant and domain synthetic create/error recovery');
    }
    assert.deepEqual(external, []); assert.deepEqual(errors, []);
    const result = { pass: true, measureOnly, browser: browser.version(), boundary: 'actual static CMS UI; synthetic loopback memory API; fresh isolated browser profile; no live auth or writes', checks, measurements, requests: requests.map(({method,path}) => ({method,path})) };
    if (artifacts) fs.writeFileSync(path.join(artifacts, 'results.json'), JSON.stringify(result, null, 2) + '\n');
    console.log(JSON.stringify({ ...result, measurements: measurements.map(m => ({ label:m.label, viewport:m.viewport, document:m.document, source:m.source, content:m.content })), requests: requests.length }));
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
}
main().catch(error => { console.error(error); process.exitCode = 1; });
