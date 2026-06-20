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
  reload() { return this.req("POST", "/reload"); },
};

let currentTenant = null;

function pagePayload(form) {
  syncRichEditors(form);
  const fd = new FormData(form);
  const payload = {
    path: fd.get("path"),
    title: fd.get("title"),
    status: fd.get("status"),
    body_html: fd.get("body_html"),
    template_id: fd.get("template_id") || "",
  };
  const publishAt = dateTimeLocalToISO(fd.get("publish_at"));
  if (publishAt) payload.publish_at = publishAt;
  const unpublishAt = dateTimeLocalToISO(fd.get("unpublish_at"));
  if (unpublishAt) payload.unpublish_at = unpublishAt;
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
    tbody.addEventListener("click", onTenantOpen, { once: true });
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
    tbody.addEventListener("click", async (ev) => {
      const btn = ev.target.closest("button[data-domain]");
      if (!btn) return;
      if (!confirm("Delete this domain?")) return;
      try {
        await api.deleteDomain(currentTenant.ID, parseInt(btn.dataset.domain, 10));
        refreshDomains();
      } catch (e) { fail(e); }
    }, { once: true });
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
    tbody.addEventListener("click", async (ev) => {
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
    }, { once: true });
  } catch (e) { fail(e); }
}

async function openPageEditor(pid) {
  try {
    const p = await api.getPage(currentTenant.ID, pid);
    const form = $("#page-editor-form");
    form.elements.id.value = p.ID;
    form.elements.path.value = p.Path || "";
    form.elements.title.value = p.Title || "";
    form.elements.status.value = p.Status || "draft";
    form.elements.template_id.value = p.TemplateID || "";
    form.elements.publish_at.value = isoToDateTimeLocal(p.PublishAt);
    form.elements.unpublish_at.value = isoToDateTimeLocal(p.UnpublishAt);
    setRichEditorHTML(form, p.BodyHTML || "");
    $("#page-preview").hidden = true;
    $("#page-editor-section").hidden = false;
  } catch (e) { fail(e); }
}

function closePageEditor() {
  $("#page-editor-section").hidden = true;
  $("#page-preview").hidden = true;
  $("#page-editor-form").reset();
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
    const surface = $("[data-editor-surface]", editor);
    const source = $("[data-editor-source]", editor);
    editor.addEventListener("click", ev => {
      const command = ev.target.closest("[data-editor-command]");
      if (command) {
        ev.preventDefault();
        surface.focus();
        document.execCommand(command.dataset.editorCommand, false, command.dataset.editorValue || null);
        source.value = surface.innerHTML;
        return;
      }
      const action = ev.target.closest("[data-editor-action]");
      if (!action) return;
      ev.preventDefault();
      if (action.dataset.editorAction === "link") {
        const url = prompt("Link URL");
        if (url) {
          surface.focus();
          document.execCommand("createLink", false, url);
          source.value = surface.innerHTML;
        }
        return;
      }
      if (action.dataset.editorAction === "source") {
        toggleSourceMode(editor);
      }
    });
    surface.addEventListener("input", () => {
      source.value = surface.innerHTML;
    });
  });
}

function toggleSourceMode(editor) {
  const surface = $("[data-editor-surface]", editor);
  const source = $("[data-editor-source]", editor);
  if (source.hidden) {
    source.value = surface.innerHTML;
    surface.hidden = true;
    source.hidden = false;
    source.focus();
    return;
  }
  setEditorSurfaceHTML(surface, source.value);
  source.value = surface.innerHTML;
  source.hidden = true;
  surface.hidden = false;
  surface.focus();
}

function syncRichEditors(root) {
  $$("[data-rich-editor]", root).forEach(editor => {
    const surface = $("[data-editor-surface]", editor);
    const source = $("[data-editor-source]", editor);
    if (source.hidden) {
      source.value = surface.innerHTML;
    } else {
      setEditorSurfaceHTML(surface, source.value);
      source.value = surface.innerHTML;
    }
  });
}

function setRichEditorHTML(root, html) {
  $$("[data-rich-editor]", root).forEach(editor => {
    const surface = $("[data-editor-surface]", editor);
    const source = $("[data-editor-source]", editor);
    setEditorSurfaceHTML(surface, html || "");
    surface.hidden = false;
    source.value = surface.innerHTML;
    source.hidden = true;
  });
}

function setEditorSurfaceHTML(surface, html) {
  surface.replaceChildren(sanitizedFragmentFromHTML(html || ""));
}

function sanitizedFragmentFromHTML(html) {
  const doc = new DOMParser().parseFromString(html, "text/html");
  const frag = document.createDocumentFragment();
  doc.body.childNodes.forEach(node => {
    const clean = sanitizeNode(node);
    if (clean) frag.appendChild(clean);
  });
  return frag;
}

function sanitizeNode(node) {
  if (node.nodeType === Node.TEXT_NODE) {
    return document.createTextNode(node.textContent || "");
  }
  if (node.nodeType !== Node.ELEMENT_NODE) {
    return null;
  }
  const tag = node.tagName.toLowerCase();
  const allowed = new Set([
    "a", "article", "blockquote", "br", "code", "div", "em", "h1", "h2", "h3",
    "h4", "h5", "h6", "hr", "i", "img", "li", "main", "ol", "p", "pre",
    "section", "span", "strong", "u", "ul",
  ]);
  const container = allowed.has(tag) ? document.createElement(tag) : document.createDocumentFragment();
  if (container.nodeType === Node.ELEMENT_NODE) {
    copySafeAttrs(node, container);
  }
  node.childNodes.forEach(child => {
    const clean = sanitizeNode(child);
    if (clean) container.appendChild(clean);
  });
  return container;
}

function copySafeAttrs(source, target) {
  for (const attr of source.attributes) {
    const name = attr.name.toLowerCase();
    const value = attr.value || "";
    if (name === "class" || name === "id" || name === "title" || name === "alt") {
      target.setAttribute(name, value);
      continue;
    }
    if (name.startsWith("data-")) {
      target.setAttribute(name, value);
      continue;
    }
    if (target.tagName === "A" && name === "href" && safeURL(value)) {
      target.setAttribute("href", value);
      continue;
    }
    if (target.tagName === "A" && name === "target" && value === "_blank") {
      target.setAttribute("target", "_blank");
      target.setAttribute("rel", "noopener noreferrer");
      continue;
    }
    if (target.tagName === "IMG" && name === "src" && safeURL(value)) {
      target.setAttribute("src", value);
    }
  }
}

function safeURL(value) {
  const trimmed = String(value || "").trim();
  return trimmed.startsWith("/") ||
    trimmed.startsWith("#") ||
    trimmed.startsWith("https://") ||
    trimmed.startsWith("http://") ||
    trimmed.startsWith("mailto:") ||
    trimmed.startsWith("tel:");
}

function renderPreviewDocument(form) {
  syncRichEditors(form);
  const title = escapeHTML(form.elements.title.value || "Untitled");
  const body = form.elements.body_html.value || "";
  return "<!doctype html><html><head><meta charset=\"utf-8\"><style>" +
    "body{font:16px/1.5 system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;margin:2rem;color:#111827}" +
    "main{max-width:760px;margin:auto} img{max-width:100%;height:auto} blockquote{border-left:4px solid #d1d5db;margin-left:0;padding-left:1rem;color:#4b5563}" +
    "</style><title>" + title + "</title></head><body><main>" + body + "</main></body></html>";
}

// Event wiring.
document.addEventListener("DOMContentLoaded", () => {
  initRichEditors();
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
      await api.updatePage(currentTenant.ID, pid, pagePayload(ev.target));
      toast("Page saved", "ok");
      refreshPages();
    } catch (e) { fail(e); }
  });

  $("#btn-page-preview").addEventListener("click", () => {
    const form = $("#page-editor-form");
    const frame = $("#page-preview");
    frame.srcdoc = renderPreviewDocument(form);
    frame.hidden = false;
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
