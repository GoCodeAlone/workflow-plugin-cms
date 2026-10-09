// Multisite admin UI — vanilla JS, no build step. Production hosts wrap
// this surface with their configured AdminAuth middleware.
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const api = {
  base: "/api/v1/admin",

  async req(method, path, body) {
    const res = await fetch(this.base + path, {
      method,
      headers: body ? { "Content-Type": "application/json" } : {},
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) {
      const text = await res.text();
      if (res.status === 409) {
        let error; try { error = JSON.parse(text); } catch {}
        if (error?.error === "version_conflict") throw new Error("This page changed. Your draft is still here. Keep a copy of your changes, then reload to review the latest page before saving.");
      }
      throw new Error(method + " " + path + " → " + res.status + ": " + text);
    }
    if (res.status === 204) return null;
    return res.json();
  },

  listTenants() { return this.req("GET", "/tenants"); },
  createTenant(b) { return this.req("POST", "/tenants", b); },
  listDomains(tid) { return this.req("GET", "/tenants/" + tid + "/domains"); },
  createDomain(tid, b) { return this.req("POST", "/tenants/" + tid + "/domains", b); },
  deleteDomain(tid, did) { return this.req("DELETE", "/tenants/" + tid + "/domains/" + did); },
  listPages(tid) { return this.req("GET", "/tenants/" + tid + "/pages"); },
  getPage(tid, pid) { return this.req("GET", "/tenants/" + tid + "/pages/" + pid); },
  createPage(tid, b) { return this.req("POST", "/tenants/" + tid + "/pages", b); },
  updatePage(tid, pid, b) { return this.req("PUT", "/tenants/" + tid + "/pages/" + pid, b); },
  deletePage(tid, pid, version) { return this.req("DELETE", "/tenants/" + tid + "/pages/" + pid, {expected_version:version}); },
  permissions(tid, pid) { return this.req("GET", "/tenants/" + tid + "/pages/permissions" + (pid ? "?page_id="+pid : "")); },
  templates(tid) { return this.req("GET", "/tenants/" + tid + "/pages/templates"); },
  async preview(tid, body) {
    const res = await fetch(this.base + "/tenants/" + tid + "/pages/preview", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error("Preview unavailable: " + res.status);
    return res.text();
  },
  reload() { return this.req("POST", "/reload"); },
};

const cmsMode = document.body.dataset.cmsMode;
const cmsBase = document.body.dataset.cmsBase;
let currentTenant = null, currentRoute = null, draftKey = "", baselineDraft = "";
let navigationGeneration = 0, historyPosition = 0, saving = false, restoringHistory = null;
let frozenDraftControls = null;
let pagePermissions = {create:false, edit:false, delete:false};
const savedPageBodies = new WeakMap();
// Classification metadata only: visual DOM always comes from the sanitizer.
// It never authorizes markup or changes the canonical source on its own.
const editorSanitizationChanges = new WeakMap();
const pageField = (form,name) => form.querySelector(':scope > input[data-page-field][name="'+name+'"], :scope > .settings-pane > label > [data-page-field][name="'+name+'"]');

function pagePayload(form) {
  syncRichEditors(form);
  const value = name => pageField(form,name).value;
  const payload = {path:value("path"),title:value("title"),status:value("status"),template_id:value("template_id")||"",body_html:$("[data-rich-editor] > [data-editor-source]",form).value,body_blocks:null};
  const saved=savedPageBodies.get(form);
  if(saved&&saved.html===payload.body_html)payload.body_blocks=saved.blocks||null;
  payload.publish_at=dateTimeLocalToISO(value("publish_at"))||null;
  payload.unpublish_at=dateTimeLocalToISO(value("unpublish_at"))||null;
  return payload;
}
function dateTimeLocalToISO(value){if(!value)return "";const d=new Date(value);return Number.isNaN(d.getTime())?"":d.toISOString()}
function isoToDateTimeLocal(value){if(!value)return "";const d=new Date(value);if(Number.isNaN(d.getTime()))return "";const pad=n=>String(n).padStart(2,"0");return d.getFullYear()+"-"+pad(d.getMonth()+1)+"-"+pad(d.getDate())+"T"+pad(d.getHours())+":"+pad(d.getMinutes())}
function toast(msg,kind){const t=$("#toast");t.textContent=msg;t.className=kind||"";t.hidden=false;setTimeout(()=>{t.hidden=true},3500)}
function fail(err){console.error(err);toast(err.message,"error")}
function escapeHTML(s){return String(s||"").replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;").replace(/'/g,"&#39;")}
function canonicalID(s){return /^[1-9][0-9]*$/.test(s)&&Number.isSafeInteger(Number(s))?Number(s):0}
function parseView(path){
 if(cmsMode==="platform"){
  if(["/","/tenants","/tenants/new","/cache"].includes(path))return{view:path==="/"?"tenants":path.slice(1)};
  const m=/^\/tenants\/([1-9][0-9]*)\/domains$/.exec(path);if(m&&canonicalID(m[1]))return{tid:Number(m[1]),view:"domains"};return null;
 }
 if(path==="/")return{view:"tenants"};
 const m=/^\/tenants\/([1-9][0-9]*)\/pages(?:\/([1-9][0-9]*|new)\/(content|appearance|publishing|html|preview)(?:\/([1-9][0-9]*))?)?$/.exec(path);
 if(!m||!canonicalID(m[1])||(m[2]&&m[2]!=="new"&&!canonicalID(m[2]))||(m[4]&&(m[3]!=="content"||!canonicalID(m[4])||Number(m[4])>10000)))return null;
 return{tid:Number(m[1]),pid:m[2]==="new"?"new":Number(m[2]||0),view:m[3]||"pages",section:Number(m[4]||0)};
}
function viewPath(){const p=location.pathname.slice(cmsBase.length);return p||"/"}
function pagePath(route,pane=route.view,section=0){return "/tenants/"+route.tid+"/pages/"+route.pid+"/"+pane+(section?"/"+section:"")}
function pageKey(route){return route?.pid?route.tid+":"+route.pid:""}
function isDirty(){return !!draftKey&&JSON.stringify(pagePayload($("#page-editor-form")))!==baselineDraft}
function updateSaveState(){if(cmsMode!=="editor"||!draftKey)return;$("#save-state").textContent=saving?"Saving…":isDirty()?"Unsaved changes":"Saved"}
function mayLeave(next){if(saving){toast("Wait for Save to finish before changing views.","error");return false}return pageKey(next)===draftKey||!isDirty()||confirm("Discard unsaved changes to this page?")}
async function navigate(path,replace=false){if(restoringHistory){toast("Wait for Back navigation to finish.","error");return}const next=parseView(path);if(!next){showRouteError("This view does not exist.");return}if(!mayLeave(next))return;
 if(replace)history.replaceState({cmsPosition:historyPosition},"",cmsBase+path);else{historyPosition++;history.pushState({cmsPosition:historyPosition},"",cmsBase+path)}
 await applyView(next);
}
function showView(id){$$("main > .view").forEach(el=>el.hidden=el.id!==id)}
function showRouteError(message){showView("route-error");$("#route-error-message").textContent=message;const recovery=$("#route-content");if(recovery){recovery.hidden=!draftKey;if(draftKey){recovery.dataset.route=pagePath(currentRoute,"content");recovery.href=cmsBase+recovery.dataset.route}}}
function resetEditingContext(){window.getSelection()?.removeAllRanges();$$("[data-event-tool]").forEach(el=>{el.hidden=true;$$("[data-event-field]",el).forEach(input=>input.value="");const picker=$("[data-event-picker]",el);if(picker)picker.value=""})}
function discardDraft(){draftKey="";baselineDraft="";savedPageBodies.delete($("#page-editor-form"));$("#section-view-style")?.replaceChildren();resetEditingContext()}
function routeLink(path,text){const a=document.createElement("a");a.href=cmsBase+path;a.dataset.route=path;a.textContent=text;return a}
function linkCell(path,text){const td=document.createElement("td");td.append(routeLink(path,text));return td}
async function applyView(route){
 const generation=++navigationGeneration, valid=()=>generation===navigationGeneration;
 currentRoute=route;resetEditingContext();
 if(cmsMode==="editor"&&pageKey(route)&&pageKey(route)===draftKey){try{showView("page-editor-section");await showPagePane(route,generation);const focus=$("[data-page-pane]:not([hidden]) h3",$("#page-editor-form"));if(focus){focus.tabIndex=-1;focus.focus({preventScroll:true})}}catch(e){if(valid())showRouteError(e.message)}return}
 if(cmsMode==="editor")discardDraft();
 showView("route-error");$("#route-error-message").textContent="Loading…";
 try{
  if(cmsMode==="platform"&&route.view==="tenants/new"){showView("new-tenant-section");return}
  if(cmsMode==="platform"&&route.view==="cache"){showView("cache-section");return}
  const list=await api.listTenants();if(!valid())return;
  if(route.view==="tenants"){
   currentTenant=null;const rows=$("#tenants-body");rows.replaceChildren();
   for(const t of list.tenants||[]){const tr=document.createElement("tr");tr.innerHTML="<td>"+escapeHTML(t.Label||t.Slug)+"</td><td>"+escapeHTML(t.Slug)+"</td>"+(cmsMode==="platform"?"<td>"+escapeHTML(t.ThemeID||"")+"</td>":"");tr.append(linkCell("/tenants/"+t.ID+(cmsMode==="platform"?"/domains":"/pages"),cmsMode==="platform"?"Manage domains":"Edit pages"));rows.append(tr)}
   showView("tenants-section");return;
  }
  const tenant=(list.tenants||[]).find(t=>t.ID===route.tid);if(!tenant)throw new Error("Site unavailable or access denied.");currentTenant=tenant;
  if(cmsMode==="platform"){await refreshDomains(tenant.ID,generation);if(!valid())return;$("#td-slug").textContent=tenant.Label+" · "+tenant.Slug;showView("domains-section");return}
  if(route.view==="pages"){
   const [data,permissions]=await Promise.all([api.listPages(tenant.ID),api.permissions(tenant.ID)]);if(!valid())return;
   $("#td-slug").textContent=tenant.Slug;$("#tenant-title").textContent=(tenant.Label||tenant.Slug)+" · Pages";
   const add=$("#new-page-link");add.hidden=!permissions.create;add.href=cmsBase+"/tenants/"+tenant.ID+"/pages/new/publishing";add.dataset.route="/tenants/"+tenant.ID+"/pages/new/publishing";
   const rows=$("#pages-body");rows.replaceChildren();for(const p of data.pages||[]){const tr=document.createElement("tr");tr.innerHTML="<td>"+escapeHTML(p.Title)+"</td><td>"+escapeHTML(p.Path)+"</td><td>"+escapeHTML(p.Status)+"</td>";tr.append(linkCell("/tenants/"+tenant.ID+"/pages/"+p.ID+"/content","Open page"));rows.append(tr)}showView("tenant-detail-section");return;
  }
  const [p,permissions,templates]=await Promise.all([route.pid==="new"?Promise.resolve({Status:"draft"}):api.getPage(tenant.ID,route.pid),api.permissions(tenant.ID,route.pid==="new"?0:route.pid),api.templates(tenant.ID)]);if(!valid())return;
  if(route.pid==="new"&&!permissions.create)throw new Error("Page creation is not permitted for this site.");
  const form=$("#page-editor-form");form.reset();pageField(form,"id").value=p.ID||"";
  for(const [field,key]of Object.entries({path:"Path",title:"Title",status:"Status",template_id:"TemplateID"}))pageField(form,field).value=p[key]|| (field==="status"?"draft":"");
  pageField(form,"publish_at").value=isoToDateTimeLocal(p.PublishAt);pageField(form,"unpublish_at").value=isoToDateTimeLocal(p.UnpublishAt);
  const body=p.RenderedBodyHTML??p.BodyHTML??"";setRichEditorHTML(form,body);savedPageBodies.set(form,{html:body,blocks:p.BodyBlocks,version:p.Version});pagePermissions=permissions;draftKey=pageKey(route);baselineDraft=JSON.stringify(pagePayload(form));
  $("#page-templates").replaceChildren(...(templates.templates||[]).map(name=>new Option(name,name)));
  $("#editor-tenant").textContent=(tenant.Label||tenant.Slug)+" · "+tenant.Slug;$("#editor-title").textContent=p.Title||"New page";
  const pages=$("#pages-link");pages.dataset.route="/tenants/"+tenant.ID+"/pages";pages.href=cmsBase+pages.dataset.route;
  const writable=route.pid==="new"?permissions.create:permissions.edit;$("#btn-save-page").hidden=!writable;$("#btn-delete-page").hidden=route.pid==="new"||!permissions.delete;
  $("#editor-permission-note").textContent=writable?"":"You have read-only access to this page.";
  $$("[data-page-field]",form).forEach(el=>el.disabled=!writable);$("[data-editor-source]",form).readOnly=!writable;$("[data-editor-surface]",form).contentEditable=String(writable);$(".editor-toolbar",form).hidden=!writable;
  showView("page-editor-section");await showPagePane(route,generation);updateSaveState();
 }catch(e){if(valid())showRouteError(e.message)}finally{
  if(valid()){const focus=$("main > .view:not([hidden]) h3")||$("main > .view:not([hidden]) h2");if(focus){focus.tabIndex=-1;focus.focus({preventScroll:true})}}
 }
}
async function refreshDomains(tid=currentTenant?.ID,generation=navigationGeneration){const data=await api.listDomains(tid);if(generation!==navigationGeneration||currentTenant?.ID!==tid)return;const tbody=$("#domains-body");tbody.replaceChildren();for(const d of data.domains||[]){const tr=document.createElement("tr");tr.innerHTML="<td>"+escapeHTML(d.Host)+"</td><td>"+escapeHTML(d.SubsiteLabel||"(root)")+"</td><td>"+escapeHTML(d.Kind)+"</td><td><button class=\"danger\" data-domain=\""+d.ID+"\">Delete</button></td>";tbody.append(tr)}}
async function showPagePane(route,generation){
 resetEditingContext();const form=$("#page-editor-form"),editor=$("[data-rich-editor]",form),surface=$("[data-editor-surface]",editor),source=$("[data-editor-source]",editor);
 syncRichEditors(form);
 const plainBody=[...surface.childNodes].some(n=>n.nodeType===Node.TEXT_NODE&&n.textContent.trim());
 if(route.view==="content"&&route.section>(surface.children.length>1&&!plainBody?surface.children.length:1))throw new Error("This content section no longer exists. Open Content to choose a section.");
 $("#section-view-style").textContent="";
 const tabs=$("#page-tabs");tabs.replaceChildren();for(const [pane,label]of Object.entries({content:"Content",appearance:"Appearance",publishing:"Publishing",html:"HTML",preview:"Preview"})){const a=routeLink(pagePath(route,pane),label);if(route.view===pane)a.setAttribute("aria-current","page");tabs.append(a)}
 $$("[data-page-pane]",form).forEach(el=>el.hidden=el.dataset.pagePane!==(route.view==="html"?"content":route.view));
 if(route.view==="content"||route.view==="html"){
  const isHTML=route.view==="html",advanced=editor.dataset.advanced==="true";
  source.hidden=!isHTML;surface.hidden=isHTML;$(".editor-toolbar",editor).hidden=isHTML||advanced||!(route.pid==="new"?pagePermissions.create:pagePermissions.edit);
  surface.contentEditable=String(!advanced&&(route.pid==="new"?pagePermissions.create:pagePermissions.edit));
  const sections=$("#section-tabs");sections.hidden=isHTML;sections.replaceChildren();
  const children=[...surface.children],plain=[...surface.childNodes].some(n=>n.nodeType===Node.TEXT_NODE&&n.textContent.trim());
  const split=children.length>1&&!plain;
  if(!isHTML&&split){const selected=route.section||1;if(selected>children.length)throw new Error("This content section no longer exists. Open Content to choose a section.");children.forEach((el,i)=>{const title=el.querySelector("h1,h2,h3")?.textContent?.trim()||("Section "+(i+1));const a=routeLink(pagePath(route,"content",i+1),title);if(i+1===selected){a.setAttribute("aria-current","page");$("#section-title").textContent=title}sections.append(a)});$("#section-view-style").textContent="#page-editor-form [data-editor-surface] > :not(:nth-child("+selected+")){display:none!important}";
  }else{if(!isHTML&&route.section>1)throw new Error("This page has one content view.");sections.append(routeLink(pagePath(route,"content"),"Page content"));$("#section-title").textContent=isHTML?"Page HTML source":"Page content"}
  if(advanced&&!isHTML){$("#section-title").textContent+=" · source editing required";surface.contentEditable="false"}
 }else if(route.view==="preview"){await refreshPreview(generation)}
}
async function refreshPreview(generation=navigationGeneration){const tid=currentTenant.ID,key=draftKey,form=$("#page-editor-form"),body=pagePayload(form),frame=$("#page-preview");frame.hidden=true;frame.setAttribute("sandbox", "allow-same-origin");frame.setAttribute("referrerpolicy","no-referrer");const html=await api.preview(tid,body);if(generation!==navigationGeneration||key!==draftKey||tid!==currentTenant?.ID)return;frame.srcdoc=html;frame.hidden=false}

function initRichEditors(root = document) {
  $$("[data-rich-editor]", root).forEach(editor => {
    const surface = $(":scope > [data-editor-surface]", editor);
    const source = $(":scope > [data-editor-source]", editor);
    const toolbar = $(":scope > .editor-toolbar", editor);
    installEventTool(editor);
    editor.addEventListener("click", ev => {
      const command = ev.target.closest("[data-editor-command]");
      if (command && toolbar.contains(command)) {
        ev.preventDefault();
        if (!source.hidden) { toast("Switch to visual editing before using formatting tools.", "error"); return; }
        if (editor.dataset.advanced === "true") { toast("Edit advanced markup in HTML source to preserve it.", "error"); return; }
        surface.focus(); ensureVisibleSelection(surface);
        document.execCommand(command.dataset.editorCommand, false, command.dataset.editorValue || null);
        editor.dataset.sourceAuthoritative = "false";
        source.value = editorHTML(surface);
        return;
      }
      const action = ev.target.closest("[data-editor-action]");
      if (!action || !toolbar.contains(action)) return;
      ev.preventDefault();
      if (["link","event"].includes(action.dataset.editorAction) && (!source.hidden || editor.dataset.advanced === "true")) { toast("Switch to visual editing before using this tool.", "error"); return; }
      if (action.dataset.editorAction === "link") {
        const url = prompt("Link URL");
        if (url && safeEditorURL(url)) {
          surface.focus(); ensureVisibleSelection(surface);
          document.execCommand("createLink", false, url);
          editor.dataset.sourceAuthoritative = "false";
          source.value = editorHTML(surface);
        } else if (url) {
          toast("Unsupported link URL", "error");
        }
        return;
      }
      if (action.dataset.editorAction === "source") {
        if (cmsMode === "editor" && currentRoute) navigate(pagePath(currentRoute,"html")).catch(fail);
        else toggleSourceMode(editor);
      }
      if (action.dataset.editorAction === "event") {
        const tool = $(":scope > [data-event-tool]", editor);
        syncRichEditors(editor);
        const picker = $("[data-event-picker]", tool);
        picker.replaceChildren(new Option("New event", ""), ...[...activeContentSection(surface).querySelectorAll("[data-event]")].map(el => new Option(el.dataset.eventTitle || el.dataset.eventId, el.dataset.eventId)));
        $$("[data-event-field]",tool).forEach(input=>input.value="");
        tool.hidden = !tool.hidden;
      }
    });
    surface.addEventListener("input", () => {
      editor.dataset.sourceAuthoritative = "false";
      source.value = editorHTML(surface);
    });
    surface.addEventListener("keydown", ev => {
      if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === "a" && currentRoute?.view === "content") {
        ev.preventDefault(); const section = activeContentSection(surface);
        if (section) { const range=document.createRange(); range.selectNodeContents(section); const selection=window.getSelection(); selection?.removeAllRanges(); selection?.addRange(range); }
      }
    });
    surface.addEventListener("beforeinput", ev => {
      if (editor.dataset.advanced === "true" || surface.contentEditable !== "true") { ev.preventDefault(); return; }
      ensureVisibleSelection(surface);
      const context=activeContentSection(surface),selection=window.getSelection(),range=selection?.rangeCount?selection.getRangeAt(0):null;
      if(context!==surface&&range?.collapsed&&ev.inputType.startsWith("delete")&&(ev.inputType.endsWith("Backward")||ev.inputType.endsWith("Forward"))){
        if(atContentBoundary(range,context,ev.inputType.endsWith("Backward")))ev.preventDefault();
      }
    });
    surface.addEventListener("cut",ev=>{
      const context=activeContentSection(surface),selection=window.getSelection(),range=selection?.rangeCount?selection.getRangeAt(0):null;
      if(!context||!range||!context.contains(range.startContainer)||!context.contains(range.endContainer)){ev.preventDefault();toast("Select content within the current section.","error")}
    });
    surface.addEventListener("paste", ev => {
      if (surface.contentEditable !== "true") { ev.preventDefault(); return; }
      ev.preventDefault();
      try {
        const html = ev.clipboardData?.getData("text/html");
        const fragment = html ? sanitizeEditorDOM(html) : document.createDocumentFragment();
        if (!html) fragment.append(document.createTextNode(ev.clipboardData?.getData("text/plain") || ""));
        scopeEditorImages(fragment);
        const selection = window.getSelection();
        let range = selection?.rangeCount ? selection.getRangeAt(0) : null;
        const context=activeContentSection(surface);
        if(!context)throw new Error("Choose a valid content section before pasting.");
        if (!range || !context.contains(range.startContainer) || !context.contains(range.endContainer)) {
          range = document.createRange(); range.selectNodeContents(context); range.collapse(false);
        }
        range.deleteContents();
        const last = fragment.lastChild;
        range.insertNode(fragment);
        if (last) range.setStartAfter(last);
        range.collapse(true); selection?.removeAllRanges(); selection?.addRange(range);
        editor.dataset.sourceAuthoritative = "false";
        source.value = editorHTML(surface);
        updateSaveState();
      } catch (e) { toast(e.message, "error"); }
    });
    surface.addEventListener("dragover", ev => ev.preventDefault());
    surface.addEventListener("drop", ev => {
      ev.preventDefault();
      toast("Paste content or use the HTML source editor to add it.", "error");
    });
  });
}

function installEventTool(editor) {
  const toolbar = $(".editor-toolbar", editor);
  const button = document.createElement("button");
  button.type = "button"; button.dataset.editorAction = "event"; button.textContent = "Event";
  toolbar.append(button);
  const tool = document.createElement("fieldset"); tool.dataset.eventTool = ""; tool.hidden = true;
  const fields = {
    id: "Stable event ID", title: "Event title", project: "Project / ensemble", persona: "Persona (classical, studio, metal)",
    start: "Start (ISO with UTC offset)", end: "End (ISO with UTC offset)", timezone: "Venue IANA timezone",
    venue: "Venue", city: "City", status: "Status (confirmed, postponed, cancelled)",
    ticket: "Ticket URL (optional)", source: "Source URL", verified: "Last verified (YYYY-MM-DD)",
  };
  tool.innerHTML = '<legend>Calendar event</legend><p>Use a confirmed source. Dates include an offset; the venue timezone is shown to visitors.</p><label>Edit event <select data-event-picker><option value="">New event</option></select></label>';
  Object.entries(fields).forEach(([key, label]) => {
    const wrap = document.createElement("label"); wrap.textContent = label;
    const input = document.createElement("input"); input.dataset.eventField = key;
    wrap.append(input); tool.append(wrap);
  });
  const apply = document.createElement("button"); apply.type = "button"; apply.textContent = "Apply event to page"; tool.append(apply); editor.append(tool);
  $("[data-event-picker]", tool).onchange = ev => {
    if (!$(":scope > [data-editor-source]", editor).hidden || editor.dataset.advanced === "true") { tool.hidden = true; return; }
    syncRichEditors(editor);
    const el = [...activeContentSection($(":scope > [data-editor-surface]", editor)).querySelectorAll("[data-event]")].find(el => el.dataset.eventId === ev.target.value);
    $$("[data-event-field]", tool).forEach(input => {
      input.value = el ? el.dataset["event" + input.dataset.eventField[0].toUpperCase() + input.dataset.eventField.slice(1)] || "" : "";
    });
  };
  apply.onclick = () => {
    if (!$(":scope > [data-editor-source]", editor).hidden || editor.dataset.advanced === "true") { tool.hidden = true; toast("Switch to visual editing before using this tool.", "error"); return; }
    const values = Object.fromEntries($$("[data-event-field]", tool).map(input => [input.dataset.eventField, input.value.trim()]));
    try {
      if (!/^[a-z0-9-]+$/.test(values.id) || !values.title || !values.project || !values.venue || !values.city) throw new Error("Complete the event identity, title, project and location.");
      const validDate = validEventDate;
      if (!validDate(values.start) || !validDate(values.end) || Date.parse(values.end) <= Date.parse(values.start)) throw new Error("Start and end are required: use valid ISO dates with offsets and an end after the start.");
      new Intl.DateTimeFormat("en", { timeZone: values.timezone }).format(new Date());
      if (!["classical", "studio", "metal"].includes(values.persona) || !["confirmed", "postponed", "cancelled"].includes(values.status)) throw new Error("Choose a supported persona and event status.");
      if (!/^https:\/\//.test(values.source) || !safeEditorURL(values.source) || (values.ticket && (!/^https:\/\//.test(values.ticket) || !safeEditorURL(values.ticket))) || !validEventDate(values.verified+"T00:00:00Z")) throw new Error("Use HTTPS source/ticket links and a real verification date.");
      syncRichEditors(editor);
      const source = $(":scope > [data-editor-source]", editor);
      const doc = $(":scope > [data-editor-surface]", editor).cloneNode(true);
      const selected = $("[data-event-picker]", tool).value;
      const prior = [...doc.querySelectorAll("[data-event]")].find(el => el.dataset.eventId === selected);
      if ([...doc.querySelectorAll("[data-event]")].some(el => el !== prior && el.dataset.eventId === values.id)) throw new Error("This event ID already exists.");
      const article = document.createElement("article"); article.dataset.event = ""; article.className = "event-card";
      Object.entries(values).forEach(([key, value]) => { article.dataset["event" + key[0].toUpperCase() + key.slice(1)] = value; });
      const title = document.createElement("h3"); title.textContent = values.title;
      const time = document.createElement("time"); time.dateTime = values.start; time.textContent = values.start + " · " + values.timezone;
      const location = document.createElement("p"); location.textContent = values.project + " · " + values.venue + ", " + values.city;
      const link = document.createElement("a"); link.href = values.ticket || values.source; link.textContent = values.ticket ? "Tickets & details" : "Event source";
      const verified = document.createElement("p"); verified.className = "small"; verified.textContent = "Verified " + values.verified + " · " + values.status;
      article.append(title, time, location, link, verified);
      const context = activeContentSection(doc);
      if (!context || (prior && !context.contains(prior))) throw new Error("Choose an event in the current content section.");
      if (prior) prior.replaceWith(article); else (context.querySelector("[data-event-list]") || context).append(article);
      setRichEditorHTML(editor, editorHTML(doc)); tool.hidden = true;
      if (currentRoute) showPagePane(currentRoute, navigationGeneration).then(updateSaveState).catch(fail);
      toast("Event updated in page. Save to persist.", "ok");
    } catch (e) { toast(e.message, "error"); }
  };
}

function validEventDate(value) {
  const match=/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if(!match)return false;
  const [year,month,day,hour,minute,second]=match.slice(1,7).map(Number);
  const days=new Date(Date.UTC(year,month,0)).getUTCDate();
  if(year<1900||month<1||month>12||day<1||day>days||hour>23||minute>59||(second||0)>59)return false;
  if(match[7]!=="Z"){const [offsetHour,offsetMinute]=match[7].slice(1).split(":").map(Number);if(offsetHour>14||offsetMinute>59||(offsetHour===14&&offsetMinute!==0))return false}
  return Number.isFinite(Date.parse(value));
}

function toggleSourceMode(editor) {
  const eventTool = $(":scope > [data-event-tool]", editor);
  if (eventTool) eventTool.hidden = true;
  const surface = $(":scope > [data-editor-surface]", editor);
  const source = $(":scope > [data-editor-source]", editor);
  if (source.hidden) {
    if (editor.dataset.sourceAuthoritative !== "true") {
      source.value = editorHTML(surface);
    }
    surface.hidden = true;
    source.hidden = false;
    source.focus();
    return;
  }
  const safe = sanitizeEditorDOM(source.value);
  if (hasAdvancedMarkup(source.value, safe)) { setSourceNotice(editor, true); return; }
  surface.replaceChildren(safe);
  setSourceNotice(editor, false);
  scopeEditorImages(surface);
  editor.dataset.sourceAuthoritative = "true";
  source.hidden = true;
  surface.hidden = false;
  surface.focus();
}

function syncRichEditors(root) {
  richEditors(root).forEach(editor => {
    const surface = $(":scope > [data-editor-surface]", editor);
    const source = $(":scope > [data-editor-source]", editor);
    if (source.hidden) {
      if (editor.dataset.sourceAuthoritative !== "true") {
        source.value = editorHTML(surface);
      }
    } else {
      const safe = sanitizeEditorDOM(source.value);
      const advanced = hasAdvancedMarkup(source.value, safe);
      surface.replaceChildren(safe);
      setSourceNotice(editor, advanced);
      scopeEditorImages(surface);
      editor.dataset.sourceAuthoritative = "true";
    }
  });
}

function setRichEditorHTML(root, html) {
  richEditors(root).forEach(editor => {
    const eventTool = $(":scope > [data-event-tool]", editor);
    if (eventTool) {
      eventTool.hidden = true;
      $("[data-event-picker]", eventTool).replaceChildren(new Option("New event", ""));
      $$("[data-event-field]", eventTool).forEach(input => { input.value = ""; });
    }
    const surface = $(":scope > [data-editor-surface]", editor);
    const source = $(":scope > [data-editor-source]", editor);
    const safe = sanitizeEditorDOM(html || "");
    const advanced = hasAdvancedMarkup(html || "", safe);
    surface.replaceChildren(safe);
    source.value = html || "";
    scopeEditorImages(surface);
    editor.dataset.sourceAuthoritative = "true";
    setSourceNotice(editor, advanced);
    surface.hidden = advanced;
    source.hidden = !advanced;
  });
}

function hasAdvancedMarkup(raw, safe) {
  // Entity escaping, quoting and empty attributes normalize during safe parsing.
  // Only a removed element/attribute (or a full document/form) requires source
  // editing. Keep the original source authoritative until a real visual edit.
  return /<\s*(?:!doctype|html|head|body|title|noscript|script|style|link|meta|base|iframe|object|embed|svg|math|template|form|input|textarea|select|option|video|audio|source)(?:[\s/>]|$)/i.test(raw) || editorSanitizationChanges.get(safe) !== false;
}

function setSourceNotice(editor, advanced) {
  editor.dataset.advanced = String(advanced);
  let note = $(":scope > [data-source-notice]", editor);
  if (!note) { note = document.createElement("p"); note.dataset.sourceNotice = ""; editor.append(note); }
  note.textContent = "This page contains advanced HTML. Edit its source to preserve all markup; visual formatting is unavailable.";
  note.hidden = !advanced;
}

function richEditors(root) {
  return root.matches?.("[data-rich-editor]") ? [root] : $$("[data-rich-editor]", root).filter(editor => !editor.closest("[data-editor-surface]"));
}

function scopeEditorImages(surface) {
  if (!currentTenant) return;
  surface.querySelectorAll('img[src^="/assets/"]').forEach(img => {
    img.src = "/api/v1/admin/tenants/" + currentTenant.ID + "/pages/assets" + img.getAttribute("src");
  });
  // Form controls are content here, not editor inputs. Keep them inert so their
  // required fields cannot block Save, and strip only our marker before saving.
  surface.querySelectorAll("input,select,textarea,button").forEach(control => {
    if (!control.disabled) { control.disabled = true; control.dataset.cmsInert = "true"; }
  });
}

function editorHTML(surface) {
  const clone = surface.cloneNode(true);
  clone.querySelectorAll('[data-cms-inert="true"]').forEach(control => {
    control.removeAttribute("disabled"); control.removeAttribute("data-cms-inert");
  });
  if (currentTenant) {
    const prefix = "/api/v1/admin/tenants/" + currentTenant.ID + "/pages/assets";
    clone.querySelectorAll("img[src]").forEach(img => {
      if (img.getAttribute("src").startsWith(prefix + "/assets/")) img.setAttribute("src", img.getAttribute("src").slice(prefix.length));
    });
  }
  return clone.innerHTML;
}

function safeEditorURL(value) {
  const trimmed = String(value || "").trim().toLowerCase();
  if (/[\u0000-\u0020\\]/.test(trimmed) || trimmed.startsWith("//")) return false;
  return trimmed.startsWith("/") ||
    trimmed.startsWith("#") ||
    trimmed.startsWith("https://") ||
    trimmed.startsWith("http://") ||
    trimmed.startsWith("mailto:") ||
    trimmed.startsWith("tel:");
}

function sanitizeEditorDOM(value) {
  if (!window.DOMPurify || !DOMPurify.isSupported) throw new Error("Safe editor unavailable. Reload before editing.");
  // Pinned local sanitizer returns inert HTML nodes. Insert nodes directly;
  // never reparse DOM text or sanitized strings through innerHTML/DOMParser.
  const fragment = DOMPurify.sanitize(String(value || ""), {
    USE_PROFILES: {html:true}, RETURN_DOM_FRAGMENT:true,
    ADD_ATTR:["target"],
    FORBID_TAGS:["script","style","link","meta","base","iframe","object","embed","svg","math","template","form","input","textarea","select","option","video","audio","source"],
    FORBID_ATTR:["srcdoc","style","action","formaction","srcset","autofocus","autoplay","poster","background","ping","lowsrc","dynsrc","archive","codebase","data"],
  });
  let removedMarkup = DOMPurify.removed.length > 0;
  fragment.querySelectorAll("*").forEach(el => {
    [...el.attributes].forEach(attr => {
      const name = attr.name.toLowerCase();
      if (name.startsWith("on") ||
          (name === "target" && !["_blank", "_self"].includes(attr.value)) ||
          (name === "href" && !safeEditorURL(attr.value)) ||
          (name === "src" && (el.tagName !== "IMG" || !/^\/assets\/[a-zA-Z0-9._/-]+$/.test(attr.value) || attr.value.split("/").some(part => part === "." || part === "..") || !safeEditorURL(attr.value)))) {
        removedMarkup = true;
        el.removeAttribute(attr.name);
      }
    });
    if (el.tagName === "A" && el.target === "_blank") el.rel = "noopener noreferrer";
  });
  editorSanitizationChanges.set(fragment, removedMarkup);
  return fragment;
}

function renderPreviewDocument(form) {
  return api.preview(currentTenant.ID, pagePayload(form));
}

// Navigation is owned by the current surface. Paths never carry credentials.
document.addEventListener("DOMContentLoaded",()=>{
 historyPosition=Number.isInteger(history.state?.cmsPosition)?history.state.cmsPosition:0;history.replaceState({cmsPosition:historyPosition},"",location.href);
 if(cmsMode==="editor"){
  initRichEditors();$("#schedule-zone").textContent="Publishing times use your browser timezone: "+Intl.DateTimeFormat().resolvedOptions().timeZone+". Saved dates use UTC.";
  document.addEventListener("submit",ev=>{if(ev.target.closest("[data-editor-surface]"))ev.preventDefault()});
  document.addEventListener("input",()=>updateSaveState());document.addEventListener("click",()=>queueMicrotask(updateSaveState));
  window.addEventListener("beforeunload",ev=>{if(saving||isDirty()){ev.preventDefault();ev.returnValue=""}});
  $("#page-editor-form").onsubmit=async ev=>{
   ev.preventDefault();if(saving)return;const form=ev.target,route={...currentRoute},tid=currentTenant.ID,key=draftKey,generation=navigationGeneration;
   const writable=route.pid==="new"?pagePermissions.create:pagePermissions.edit;if(!writable){toast("Page editing is not permitted.","error");return}
   if(!form.checkValidity()){await navigate(pagePath(route,"publishing"));form.reportValidity();return}
   const payload=pagePayload(form);saving=true;freezeDraft(true);$("#btn-save-page").disabled=true;updateSaveState();
   try{
    const saved=route.pid==="new"?await api.createPage(tid,payload):await api.updatePage(tid,route.pid,{...payload,expected_version:savedPageBodies.get(form)?.version});
    if(key!==draftKey||tid!==currentTenant?.ID||generation!==navigationGeneration)return;
    savedPageBodies.set(form,{html:payload.body_html,blocks:payload.body_blocks,version:saved.Version});baselineDraft=JSON.stringify(payload);$("#editor-title").textContent=payload.title;toast("Page saved", "ok");
    if(route.pid==="new"){freezeDraft(false);saving=false;discardDraft();await navigate("/tenants/"+tid+"/pages/"+saved.ID+"/content",true)}
   }catch(e){if(key===draftKey&&generation===navigationGeneration)fail(e)}finally{freezeDraft(false);saving=false;$("#btn-save-page").disabled=false;updateSaveState()}
  };
  $("#btn-page-preview").onclick=()=>refreshPreview().catch(fail);
  $("#btn-delete-page").onclick=async()=>{if(saving||!pagePermissions.delete||!confirm("Delete this page?"))return;const tid=currentTenant.ID,pid=currentRoute.pid; saving=true;try{await api.deletePage(tid,pid,savedPageBodies.get($("#page-editor-form"))?.version);discardDraft();saving=false;await navigate("/tenants/"+tid+"/pages")}catch(e){fail(e)}finally{saving=false;updateSaveState()}};
 }else{
  $("#new-tenant-form").onsubmit=async ev=>{ev.preventDefault();const fd=new FormData(ev.target);try{await api.createTenant({slug:fd.get("slug"),label:fd.get("label"),theme_id:fd.get("theme_id")});ev.target.reset();toast("Tenant created","ok");await navigate("/tenants")}catch(e){fail(e)}};
  $("#new-domain-form").onsubmit=async ev=>{ev.preventDefault();const tid=currentTenant.ID,generation=navigationGeneration,fd=new FormData(ev.target);try{await api.createDomain(tid,{host:fd.get("host"),subsite_label:fd.get("subsite_label"),kind:fd.get("kind")});if(generation!==navigationGeneration)return;ev.target.reset();toast("Domain added","ok");await refreshDomains(tid,generation)}catch(e){fail(e)}};
  $("#domains-body").onclick=async ev=>{const btn=ev.target.closest("button[data-domain]");if(!btn||!confirm("Delete this domain?"))return;const tid=currentTenant.ID,generation=navigationGeneration;try{await api.deleteDomain(tid,Number(btn.dataset.domain));await refreshDomains(tid,generation)}catch(e){fail(e)}};
  $("#btn-reload").onclick=async()=>{try{await api.reload();toast("Tenant cache reloaded","ok")}catch(e){fail(e)}};
 }
 document.addEventListener("click",ev=>{
  const link=ev.target.closest("a[data-route]");if(!link||ev.ctrlKey||ev.metaKey||ev.shiftKey||ev.altKey||ev.button!==0)return;ev.preventDefault();navigate(link.dataset.route).catch(fail);
 });
 window.addEventListener("popstate",ev=>{
  const next=parseView(viewPath()),target=Number.isInteger(ev.state?.cmsPosition)?ev.state.cmsPosition:0;
  if(restoringHistory){
   const expected=restoringHistory;
   if(target===expected.position&&viewPath()===expected.path){historyPosition=target;restoringHistory=null;return}
   const delta=expected.position-target;
   if(delta)history.go(delta);else{history.replaceState({cmsPosition:expected.position},"",cmsBase+expected.path);restoringHistory=null}
   return;
  }
  if(!next||!mayLeave(next)){
   const path=currentRoute?currentRoutePath():"/",delta=historyPosition-target;
   if(delta){restoringHistory={position:historyPosition,path};history.go(delta)}else history.replaceState({cmsPosition:historyPosition},"",cmsBase+path);
   return;
  }
  historyPosition=target;applyView(next).catch(fail);
 });
 const initial=parseView(viewPath());if(initial)applyView(initial).catch(fail);else showRouteError("This view does not exist.");
});
function currentRoutePath(){const r=currentRoute;if(r.pid)return pagePath(r,r.view,r.section);if(r.tid)return "/tenants/"+r.tid+(cmsMode==="platform"?"/domains":"/pages");return r.view==="tenants"?"/":"/"+r.view}
function ensureVisibleSelection(surface){
 const selected=activeContentSection(surface);
 if(!selected)return;const selection=window.getSelection(),range=selection?.rangeCount?selection.getRangeAt(0):null;
 if(!range||!selected.contains(range.commonAncestorContainer)){const visibleRange=document.createRange();visibleRange.selectNodeContents(selected);visibleRange.collapse(false);selection?.removeAllRanges();selection?.addRange(visibleRange)}
}

// Freeze owned draft controls during Save. The saved baseline is always the
// captured submitted snapshot, never any later visible form state.
function freezeDraft(freeze){
 const form=$("#page-editor-form"),surface=$("[data-editor-surface]",form);
 if(freeze){
  const controls=$$("[data-page-field], [data-editor-source], .editor-toolbar button, [data-event-tool] input, [data-event-tool] select, [data-event-tool] button",form);
  frozenDraftControls={controls:controls.map(el=>[el,el.disabled]),surface,editable:surface.contentEditable};
  controls.forEach(el=>el.disabled=true);surface.contentEditable="false";resetEditingContext();
 }else if(frozenDraftControls){
  frozenDraftControls.controls.forEach(([el,disabled])=>el.disabled=disabled);frozenDraftControls.surface.contentEditable=frozenDraftControls.editable;frozenDraftControls=null;
 }
}

function activeContentSection(surface){
 const plain=[...surface.childNodes].some(n=>n.nodeType===Node.TEXT_NODE&&n.textContent.trim());
 return currentRoute?.view==="content"&&surface.children.length>1&&!plain?surface.children[(currentRoute.section||1)-1]:surface;
}

// Walk actual DOM boundary points. Images/BRs count as content even when their
// textContent is empty; only a caret at the selected section's edge is blocked.
function atContentBoundary(range,context,backward){
 let node=range.startContainer,offset=range.startOffset;
 while(node){
  const length=node.nodeType===Node.TEXT_NODE?node.data.length:node.childNodes.length;
  if(offset!==(backward?0:length))return false;
  if(node===context)return true;
  const parent=node.parentNode;if(!parent||!context.contains(parent))return false;
  const index=[...parent.childNodes].indexOf(node);
  offset=backward?index:index+1;node=parent;
 }
 return false;
}
