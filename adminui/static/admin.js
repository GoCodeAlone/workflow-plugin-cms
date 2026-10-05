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
  deletePage(tid, pid) { return this.req("DELETE", "/tenants/" + tid + "/pages/" + pid); },
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

let currentTenant = null;
const savedPageBodies = new WeakMap();

function pagePayload(form) {
  syncRichEditors(form);
  const fd = new FormData(form);
  const payload = {
    path: fd.get("path"),
    title: fd.get("title"),
    status: fd.get("status"),
    body_html: fd.get("body_html"),
    template_id: fd.get("template_id") || "",
    body_blocks: null,
  };
  const saved = savedPageBodies.get(form);
  if (saved && saved.html === payload.body_html) payload.body_blocks = saved.blocks || null;
  const publishAt = dateTimeLocalToISO(fd.get("publish_at"));
  payload.publish_at = publishAt || null;
  const unpublishAt = dateTimeLocalToISO(fd.get("unpublish_at"));
  payload.unpublish_at = unpublishAt || null;
  return payload;
}

function dateTimeLocalToISO(value) {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  return d.toISOString();
}

function isoToDateTimeLocal(value) {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  const pad = n => String(n).padStart(2, "0");
  return (
    d.getFullYear() + "-" +
    pad(d.getMonth() + 1) + "-" +
    pad(d.getDate()) + "T" +
    pad(d.getHours()) + ":" +
    pad(d.getMinutes())
  );
}

function toast(msg, kind) {
  const t = $("#toast");
  t.textContent = msg;
  t.className = kind || "";
  t.hidden = false;
  setTimeout(() => { t.hidden = true; }, 3000);
}

function fail(err) {
  console.error(err);
  toast(err.message, "error");
}

async function refreshTenants() {
  try {
    const data = await api.listTenants();
    const tbody = $("#tenants-body");
    tbody.innerHTML = "";
    for (const t of data.tenants || []) {
      const tr = document.createElement("tr");
      tr.innerHTML =
        "<td>" + t.ID + "</td>" +
        "<td>" + escapeHTML(t.Slug) + "</td>" +
        "<td>" + escapeHTML(t.Label) + "</td>" +
        "<td>" + escapeHTML(t.ThemeID || "") + "</td>" +
        "<td><button class=\"link\" data-tenant=\"" + t.ID + "\">manage →</button></td>";
      tbody.appendChild(tr);
    }
    tbody.onclick = onTenantOpen;
  } catch (e) { fail(e); }
}

function onTenantOpen(ev) {
  const btn = ev.target.closest("button[data-tenant]");
  if (!btn) return;
  openTenant(parseInt(btn.dataset.tenant, 10));
}

async function openTenant(tid) {
  try {
    const list = await api.listTenants();
    const t = (list.tenants || []).find(x => x.ID === tid);
    if (!t) throw new Error("tenant " + tid + " not found");
    currentTenant = t;

    $("#tenants-section").hidden = true;
    $("#tenant-detail-section").hidden = false;
    $("#td-slug").textContent = t.Slug;

    await refreshDomains();
    await refreshPages();
    const templates = await api.templates(tid);
    $("#page-templates").replaceChildren(...(templates.templates || []).map(name => {
      const option = document.createElement("option"); option.value = name; return option;
    }));
  } catch (e) { fail(e); }
}

async function refreshDomains() {
  try {
    const data = await api.listDomains(currentTenant.ID);
    const tbody = $("#domains-body");
    tbody.innerHTML = "";
    for (const d of data.domains || []) {
      const tr = document.createElement("tr");
      tr.innerHTML =
        "<td>" + escapeHTML(d.Host) + "</td>" +
        "<td>" + escapeHTML(d.SubsiteLabel || "(root)") + "</td>" +
        "<td>" + escapeHTML(d.Kind) + "</td>" +
        "<td><button class=\"danger\" data-domain=\"" + d.ID + "\">delete</button></td>";
      tbody.appendChild(tr);
    }
    tbody.onclick = async (ev) => {
      const btn = ev.target.closest("button[data-domain]");
      if (!btn) return;
      if (!confirm("Delete this domain?")) return;
      try {
        await api.deleteDomain(currentTenant.ID, parseInt(btn.dataset.domain, 10));
        refreshDomains();
      } catch (e) { fail(e); }
    };
  } catch (e) { fail(e); }
}

async function refreshPages() {
  try {
    const data = await api.listPages(currentTenant.ID);
    const tbody = $("#pages-body");
    tbody.innerHTML = "";
    for (const p of data.pages || []) {
      const tr = document.createElement("tr");
      tr.innerHTML =
        "<td>" + p.ID + "</td>" +
        "<td>" + escapeHTML(p.Path) + "</td>" +
        "<td>" + escapeHTML(p.Title) + "</td>" +
        "<td>" + escapeHTML(p.Status) + "</td>" +
        "<td><button class=\"link\" data-edit-page=\"" + p.ID + "\">edit</button> " +
        "<button class=\"danger\" data-delete-page=\"" + p.ID + "\">delete</button></td>";
      tbody.appendChild(tr);
    }
    tbody.onclick = async (ev) => {
      const edit = ev.target.closest("button[data-edit-page]");
      if (edit) {
        await openPageEditor(parseInt(edit.dataset.editPage, 10));
        return;
      }
      const btn = ev.target.closest("button[data-delete-page]");
      if (!btn || !confirm("Delete this page?")) return;
      try {
        await api.deletePage(currentTenant.ID, parseInt(btn.dataset.deletePage, 10));
        closePageEditor();
        refreshPages();
      } catch (e) { fail(e); }
    };
  } catch (e) { fail(e); }
}

async function openPageEditor(pid) {
  try {
    const p = await api.getPage(currentTenant.ID, pid);
    const form = $("#page-editor-form");
    const field = name => form.querySelector(':scope > [name="'+name+'"], :scope > label > [name="'+name+'"]');
    field("id").value = p.ID;
    field("path").value = p.Path || "";
    field("title").value = p.Title || "";
    field("status").value = p.Status || "draft";
    field("template_id").value = p.TemplateID || "";
    field("publish_at").value = isoToDateTimeLocal(p.PublishAt);
    field("unpublish_at").value = isoToDateTimeLocal(p.UnpublishAt);
    const body = p.RenderedBodyHTML ?? p.BodyHTML ?? "";
    setRichEditorHTML(form, body);
    savedPageBodies.set(form, {html:body, blocks:p.BodyBlocks});
    $("#page-preview").hidden = true;
    $("#page-editor-section").hidden = false;
  } catch (e) { fail(e); }
}

function closePageEditor() {
  $("#page-editor-section").hidden = true;
  $("#page-preview").hidden = true;
  $("#page-editor-form").reset();
  savedPageBodies.delete($("#page-editor-form"));
  setRichEditorHTML($("#page-editor-form"), "");
}

function escapeHTML(s) {
  return String(s || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function initRichEditors(root = document) {
  $$("[data-rich-editor]", root).forEach(editor => {
    const surface = $(":scope > [data-editor-surface]", editor);
    const source = $(":scope > [data-editor-source]", editor);
    installEventTool(editor);
    editor.addEventListener("click", ev => {
      const command = ev.target.closest("[data-editor-command]");
      if (command) {
        ev.preventDefault();
        if (!source.hidden) { toast("Switch to visual editing before using formatting tools.", "error"); return; }
        if (editor.dataset.advanced === "true") { toast("Edit advanced markup in HTML source to preserve it.", "error"); return; }
        surface.focus();
        document.execCommand(command.dataset.editorCommand, false, command.dataset.editorValue || null);
        editor.dataset.sourceAuthoritative = "false";
        source.value = editorHTML(surface);
        return;
      }
      const action = ev.target.closest("[data-editor-action]");
      if (!action) return;
      ev.preventDefault();
      if (["link","event"].includes(action.dataset.editorAction) && (!source.hidden || editor.dataset.advanced === "true")) { toast("Switch to visual editing before using this tool.", "error"); return; }
      if (action.dataset.editorAction === "link") {
        const url = prompt("Link URL");
        if (url && safeEditorURL(url)) {
          surface.focus();
          document.execCommand("createLink", false, url);
          editor.dataset.sourceAuthoritative = "false";
          source.value = editorHTML(surface);
        } else if (url) {
          toast("Unsupported link URL", "error");
        }
        return;
      }
      if (action.dataset.editorAction === "source") {
        toggleSourceMode(editor);
      }
      if (action.dataset.editorAction === "event") {
        const tool = $(":scope > [data-event-tool]", editor);
        syncRichEditors(editor);
        const picker = $("[data-event-picker]", tool);
        picker.replaceChildren(new Option("New event", ""), ...[...new DOMParser().parseFromString(source.value, "text/html").querySelectorAll("[data-event]")].map(el => new Option(el.dataset.eventTitle || el.dataset.eventId, el.dataset.eventId)));
        tool.hidden = !tool.hidden;
      }
    });
    surface.addEventListener("input", () => {
      editor.dataset.sourceAuthoritative = "false";
      source.value = editorHTML(surface);
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
    syncRichEditors(editor);
    const el = [...new DOMParser().parseFromString($(":scope > [data-editor-source]", editor).value, "text/html").querySelectorAll("[data-event]")].find(el => el.dataset.eventId === ev.target.value);
    $$("[data-event-field]", tool).forEach(input => {
      input.value = el ? el.dataset["event" + input.dataset.eventField[0].toUpperCase() + input.dataset.eventField.slice(1)] || "" : "";
    });
  };
  apply.onclick = () => {
    const values = Object.fromEntries($$("[data-event-field]", tool).map(input => [input.dataset.eventField, input.value.trim()]));
    try {
      if (!/^[a-z0-9-]+$/.test(values.id) || !values.title || !values.project || !values.venue || !values.city) throw new Error("Complete the event identity, title, project and location.");
      const validDate = validEventDate;
      if (!validDate(values.start) || (values.end && (!validDate(values.end) || Date.parse(values.end) <= Date.parse(values.start)))) throw new Error("Use valid ISO dates with offsets and an end after the start.");
      new Intl.DateTimeFormat("en", { timeZone: values.timezone }).format(new Date());
      if (!["classical", "studio", "metal"].includes(values.persona) || !["confirmed", "postponed", "cancelled"].includes(values.status)) throw new Error("Choose a supported persona and event status.");
      if (!/^https:\/\//.test(values.source) || !safeEditorURL(values.source) || (values.ticket && (!/^https:\/\//.test(values.ticket) || !safeEditorURL(values.ticket))) || !validEventDate(values.verified+"T00:00:00Z")) throw new Error("Use HTTPS source/ticket links and a real verification date.");
      syncRichEditors(editor);
      const source = $(":scope > [data-editor-source]", editor);
      const doc = new DOMParser().parseFromString(source.value, "text/html");
      const selected = $("[data-event-picker]", tool).value;
      const prior = [...doc.querySelectorAll("[data-event]")].find(el => el.dataset.eventId === selected);
      if ([...doc.querySelectorAll("[data-event]")].some(el => el !== prior && el.dataset.eventId === values.id)) throw new Error("This event ID already exists.");
      const article = doc.createElement("article"); article.dataset.event = ""; article.className = "event-card";
      Object.entries(values).forEach(([key, value]) => { article.dataset["event" + key[0].toUpperCase() + key.slice(1)] = value; });
      const title = doc.createElement("h3"); title.textContent = values.title;
      const time = doc.createElement("time"); time.dateTime = values.start; time.textContent = values.start + " · " + values.timezone;
      const location = doc.createElement("p"); location.textContent = values.project + " · " + values.venue + ", " + values.city;
      const link = doc.createElement("a"); link.href = values.ticket || values.source; link.textContent = values.ticket ? "Tickets & details" : "Event source";
      const verified = doc.createElement("p"); verified.className = "small"; verified.textContent = "Verified " + values.verified + " · " + values.status;
      article.append(title, time, location, link, verified);
      if (prior) prior.replaceWith(article); else (doc.querySelector("[data-event-list]") || doc.body).append(article);
      setRichEditorHTML(editor, doc.body.innerHTML); tool.hidden = true;
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
  const safe = sanitizeEditorHTML(source.value);
  if (hasAdvancedMarkup(source.value, safe)) { setSourceNotice(editor, true); return; }
  surface.innerHTML = safe;
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
      surface.innerHTML = sanitizeEditorHTML(source.value);
      setSourceNotice(editor, hasAdvancedMarkup(source.value, surface.innerHTML));
      scopeEditorImages(surface);
      editor.dataset.sourceAuthoritative = "true";
    }
  });
}

function setRichEditorHTML(root, html) {
  richEditors(root).forEach(editor => {
    const surface = $(":scope > [data-editor-surface]", editor);
    const source = $(":scope > [data-editor-source]", editor);
    surface.innerHTML = sanitizeEditorHTML(html || "");
    source.value = html || "";
    scopeEditorImages(surface);
    editor.dataset.sourceAuthoritative = "true";
    const advanced = hasAdvancedMarkup(source.value, sanitizeEditorHTML(source.value));
    setSourceNotice(editor, advanced);
    surface.hidden = advanced;
    source.hidden = !advanced;
  });
}

function hasAdvancedMarkup(raw, safe) {
  const parsed = new DOMParser().parseFromString(raw, "text/html");
  // This surface lives inside the metadata form. Nested forms cannot survive
  // browser HTML parsing there; keep their canonical source explicitly editable.
  return /<!doctype|<html[\s>]|<head[\s>]|<body[\s>]/i.test(raw) || parsed.head.children.length > 0 || !!parsed.body.querySelector("form") || parsed.body.innerHTML !== safe;
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

function sanitizeEditorHTML(value) {
  const doc = new DOMParser().parseFromString(value, "text/html");
  doc.querySelectorAll("script,style,link,meta,base,iframe,object,embed,svg,math,template").forEach(el => el.remove());
  doc.body.querySelectorAll("*").forEach(el => {
    [...el.attributes].forEach(attr => {
      const name = attr.name.toLowerCase();
      if (name.startsWith("on") || ["srcdoc", "style", "action", "formaction", "srcset"].includes(name) ||
          (name === "target" && !["_blank", "_self"].includes(attr.value)) ||
          (["href", "src"].includes(name) && !safeEditorURL(attr.value))) el.removeAttribute(attr.name);
    });
  });
  return doc.body.innerHTML;
}

function renderPreviewDocument(form) {
  return api.preview(currentTenant.ID, pagePayload(form));
}

// Event wiring.
document.addEventListener("DOMContentLoaded", () => {
  initRichEditors();
  $("#schedule-zone").textContent = "Publishing times use your browser timezone: " + Intl.DateTimeFormat().resolvedOptions().timeZone + ". Saved dates use UTC.";
  document.addEventListener("submit", ev => {
    if (ev.target.closest("[data-editor-surface]")) ev.preventDefault();
  });
  refreshTenants();

  $("#new-tenant-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const fd = new FormData(ev.target);
    try {
      await api.createTenant({
        slug: fd.get("slug"),
        label: fd.get("label"),
        theme_id: fd.get("theme_id"),
      });
      ev.target.reset();
      toast("Tenant created", "ok");
      refreshTenants();
    } catch (e) { fail(e); }
  });

  $("#new-domain-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const fd = new FormData(ev.target);
    try {
      await api.createDomain(currentTenant.ID, {
        host: fd.get("host"),
        subsite_label: fd.get("subsite_label"),
        kind: fd.get("kind"),
      });
      ev.target.reset();
      toast("Domain added", "ok");
      refreshDomains();
    } catch (e) { fail(e); }
  });

  $("#new-page-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    try {
      await api.createPage(currentTenant.ID, pagePayload(ev.target));
      ev.target.reset();
      setRichEditorHTML(ev.target, "");
      toast("Page created", "ok");
      refreshPages();
    } catch (e) { fail(e); }
  });

  $("#page-editor-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const fd = new FormData(ev.target);
    try {
      const pid = parseInt(fd.get("id"), 10);
      const payload = pagePayload(ev.target);
      await api.updatePage(currentTenant.ID, pid, payload);
      savedPageBodies.set(ev.target, {html:payload.body_html, blocks:payload.body_blocks});
      toast("Page saved", "ok");
      refreshPages();
    } catch (e) { fail(e); }
  });

  $("#btn-page-preview").addEventListener("click", async () => {
    const form = $("#page-editor-form");
    const frame = $("#page-preview");
    // Keep the authenticated same-origin asset context, while the sandbox still
    // disallows scripts, forms, popups and top navigation. Server CSP agrees.
    frame.setAttribute("sandbox", "allow-same-origin");
    frame.setAttribute("referrerpolicy", "no-referrer");
    try {
      frame.srcdoc = await renderPreviewDocument(form);
      frame.hidden = false;
    } catch (e) { fail(e); }
  });

  $("#btn-page-editor-close").addEventListener("click", closePageEditor);

  $("#btn-back").addEventListener("click", () => {
    $("#tenant-detail-section").hidden = true;
    $("#tenants-section").hidden = false;
    currentTenant = null;
    refreshTenants();
  });

  $("#btn-reload").addEventListener("click", async () => {
    try {
      await api.reload();
      toast("Tenant cache reloaded", "ok");
    } catch (e) { fail(e); }
  });
});
