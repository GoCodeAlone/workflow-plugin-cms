const assert = require('node:assert/strict')
const path = require('node:path')
const { createRequire } = require('node:module')

async function main() {
  const chunks = []
  for await (const chunk of process.stdin) chunks.push(chunk)
  const fixture = JSON.parse(Buffer.concat(chunks).toString())
  assert.equal(new URL(fixture.url).hostname, '127.0.0.1')
  if (!process.env.CMS_PLAYWRIGHT_MODULE) throw new Error('CMS_PLAYWRIGHT_MODULE is required')
  const { chromium } = createRequire(path.resolve(process.env.CMS_PLAYWRIGHT_MODULE))('playwright')
  const browser = await chromium.launch({ headless: true, ...(process.env.CMS_BROWSER_CHANNEL ? { channel: process.env.CMS_BROWSER_CHANNEL } : {}) })
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true, serviceWorkers: 'block' })
    await context.addCookies([{ name: fixture.cookie, value: fixture.token, url: fixture.url, secure: true, httpOnly: true }])
    const external = [], errors = []
    await context.route('**/*', route => {
      if (new URL(route.request().url()).origin !== fixture.url) { external.push('blocked'); return route.abort() }
      return route.continue()
    })
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    page.on('pageerror', error => errors.push(error.message))
    const route = fixture.url + '/admin/tenants/' + fixture.tenant + '/pages/' + fixture.page + '/content'
    const api = '/api/v1/admin/tenants/' + fixture.tenant + '/pages/' + fixture.page
    await page.goto(route)
    await page.locator('[data-editor-surface]').waitFor()
    if (!(await page.getByRole('button', { name: 'Event', exact: true }).isVisible())) {
      const removed = await page.evaluate(() => {
        sanitizeEditorDOM(document.querySelector('[data-editor-source]').value)
        return DOMPurify.removed.map(item => ({ element: item.element?.nodeName, attribute: item.attribute?.name, from: item.from?.nodeName }))
      })
      throw new Error('safe fixture classified advanced: ' + JSON.stringify(removed))
    }
    await page.getByRole('button', { name: 'Event', exact: true }).waitFor()
    assert.equal(await page.getByRole('textbox', { name: 'Page body', exact: true }).getAttribute('contenteditable'), 'true')
    // A no-op save preserves the exact source, including entities, quotes and
    // empty data attributes; parser normalization does not rewrite the record.
    async function save() {
      const response = page.waitForResponse(response => response.url().endsWith(api) && response.request().method() === 'PUT')
      await page.getByRole('button', { name: 'Save page', exact: true }).click()
      const saved = await response
      assert.equal(saved.status(), 200)
      await page.waitForFunction(() => document.getElementById('save-state').textContent === 'Saved')
      return saved.json()
    }
    assert.equal((await save()).BodyHTML, fixture.body)
    const cases = [
      ['entities', '<p>Voice & music &amp; teaching</p>', false],
      ['attribute quotes and empty data attributes', "<section title='Voice'><div data-event-list></div><br></section>", false],
      ['safe blank-target link', '<p><a href="/local" target="_blank">Local link</a></p>', false],
      ['form', '<form><input required></form>', true],
      ['style', '<p style="color:red">Source-only style</p>', true],
      ['event handler', '<img src="/assets/absent.jpg" onerror="window.cmsUnsafeRan=true">', true],
      ['script URL', '<a href="javascript:window.cmsUnsafeRan=true">Unsafe link</a>', true],
      ['remote image', '<img src="https://outside.example.test/track">', true],
      ['invalid target', '<a href="/local" target="unknown">Unknown target</a>', true],
      ['iframe', '<iframe src="https://outside.example.test/frame"></iframe>', true],
      ['SVG payload', '<svg onload="window.cmsUnsafeRan=true"></svg>', true],
      ['script', '<script>window.cmsUnsafeRan=true</script><p>Text</p>', true],
    ]
    for (const [name, html, advanced] of cases) {
      await page.getByRole('link', { name: 'HTML', exact: true }).click()
      await page.getByRole('textbox', { name: 'Page HTML source', exact: true }).fill(html)
      await page.getByRole('link', { name: 'Content', exact: true }).click()
      assert.equal(await page.locator('[data-source-notice]').isVisible(), advanced, name)
      assert.equal(await page.locator('[data-editor-surface]').getAttribute('contenteditable'), String(!advanced), name)
      assert.equal(await page.locator('[data-editor-source]').inputValue(), html, name + ' canonical source retained')
      assert.equal(await page.evaluate(() => window.cmsUnsafeRan === true), false, name + ' did not execute')
    }
    await page.getByRole('link', { name: 'HTML', exact: true }).click()
    await page.getByRole('textbox', { name: 'Page HTML source', exact: true }).fill(fixture.body)
    await page.getByRole('link', { name: 'Content', exact: true }).click()
    await page.getByRole('textbox', { name: 'Page body', exact: true }).fill('Edited safe content')
    assert.match((await save()).BodyHTML, /Edited safe content/)
    await page.reload()
    await page.getByRole('textbox', { name: 'Page body', exact: true }).waitFor()
    assert.match(await page.getByRole('textbox', { name: 'Page body', exact: true }).textContent(), /Edited safe content/)
    assert.deepEqual(external, [])
    assert.deepEqual(errors, [])
    process.stdout.write(JSON.stringify({ pass: true, boundary: 'actual native CMS UI/API; loopback TLS; memory stores; fixture cookie auth hook', cases: cases.length, checks: ['safe-markup-visual-editing', 'no-op-source-preservation', 'unsafe-markup-readonly-source', 'no-payload-execution', 'no-remote-media-request', 'visual-save-reload'] }) + '\n')
  } finally { await browser.close() }
}
main().catch(error => { process.stderr.write(error.message + '\n'); process.exitCode = 1 })
