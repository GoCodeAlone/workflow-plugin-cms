const assert = require("node:assert/strict");
const {chromium} = require(process.env.CMS_TEST_PLAYWRIGHT_PATH);

(async () => {
  const origin = process.env.CMS_TEST_EDITOR_ORIGIN;
  const tid = process.env.CMS_TEST_TENANT_ID;
  const pid = process.env.CMS_TEST_PAGE_ID;
  const api = `/api/v1/admin/tenants/${tid}/pages/${pid}`;
  const browser = await chromium.launch({
    executablePath: process.env.CMS_TEST_BROWSER_EXECUTABLE,
    headless: true,
  });
  try {
    const context = await browser.newContext({viewport:{width:1100,height:820}});
    const page = await context.newPage();
    page.setDefaultTimeout(10000);
    page.on("dialog", dialog => dialog.accept());
    const versions = [];
    page.on("request", request => {
      if (request.url()===origin+api && ["PUT","DELETE"].includes(request.method())) {
        versions.push({method:request.method(),version:request.postDataJSON().expected_version});
      }
    });
    await page.goto(`${origin}/admin/tenants/${tid}/pages/${pid}/publishing`);
    const title = page.locator('input[name="title"][data-page-field]');
    await title.waitFor({state:"visible"});
    assert.equal(await title.inputValue(),"Browser initial");
    const save = async (value,status) => {
      await title.fill(value);
      const response = page.waitForResponse(r=>r.url()===origin+api&&r.request().method()==="PUT");
      await page.locator("#btn-save-page").click();
      assert.equal((await response).status(),status);
      await page.waitForFunction(()=>!document.querySelector("#btn-save-page").disabled);
    };
    await save("First browser save",200);
    await save("Second browser save",200);
    assert.equal((await page.request.post(origin+"/fixture/promote")).status(),200);
    await save("Unsaved browser draft",409);
    assert.equal(await title.inputValue(),"Unsaved browser draft");
    assert.match(await page.locator("#save-state").innerText(),/Unsaved/i);
    assert.match(await page.locator("#toast").innerText(),/reload/i);
    const persisted = await (await page.request.get(origin+api)).json();
    assert.equal(persisted.Title,"Approved fixture promotion");
    assert.equal(persisted.Version,4);
    if (process.env.CMS_TEST_SCREENSHOT) await page.screenshot({path:process.env.CMS_TEST_SCREENSHOT,fullPage:true});
    const deletion=page.waitForResponse(r=>r.url()===origin+api&&r.request().method()==="DELETE");
    await page.locator("#btn-delete-page").click();
    assert.equal((await deletion).status(),409);
    assert.equal((await page.request.get(origin+api)).status(),200);
    await page.reload();
    await title.waitFor({state:"visible"});
    assert.equal(await title.inputValue(),"Approved fixture promotion");
    await save("Fresh reviewed edit",200);
    await page.reload();
    await title.waitFor({state:"visible"});
    assert.equal(await title.inputValue(),"Fresh reviewed edit");
    assert.deepEqual(versions,[{method:"PUT",version:1},{method:"PUT",version:2},{method:"PUT",version:3},{method:"DELETE",version:3},{method:"PUT",version:4}]);
    assert.equal((await page.request.get(origin+`/api/v1/admin/tenants/${Number(tid)+1}/pages/${pid}`)).status(),403);
    console.log(JSON.stringify({browser:"Chrome",store:"isolated Postgres",saveDeleteExpectedVersions:versions,staleSave:409,staleDelete:409,draftRetained:true,reloadPersistence:true,wrongTenant:403}));
  } finally {
    await browser.close();
  }
})().catch(error => {console.error(error);process.exitCode=1});
