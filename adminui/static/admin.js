// Multisite admin UI — vanilla JS, no build step.
//
// Pre-auth scaffold per gocodealone-multisite SPEC T15. Once
// workflow-plugin-auth ships the embeddable handler (issue #23) this
// page gets a passkey-enrolment flow + Bearer token persistence.
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
    form.elements.body_html.value = p.BodyHTML || "";
    $("#page-preview").hidden = true;
    $("#page-editor-section").hidden = false;
  } catch (e) { fail(e); }
}

function closePageEditor() {
  $("#page-editor-section").hidden = true;
  $("#page-preview").hidden = true;
  $("#page-editor-form").reset();
}

function escapeHTML(s) {
  return String(s || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// Event wiring.
document.addEventListener("DOMContentLoaded", () => {
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
    const fd = new FormData(ev.target);
    try {
      await api.createPage(currentTenant.ID, {
        path: fd.get("path"),
        title: fd.get("title"),
        status: fd.get("status"),
        body_html: fd.get("body_html"),
      });
      ev.target.reset();
      toast("Page created", "ok");
      refreshPages();
    } catch (e) { fail(e); }
  });

  $("#page-editor-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const fd = new FormData(ev.target);
    try {
      const pid = parseInt(fd.get("id"), 10);
      await api.updatePage(currentTenant.ID, pid, {
        path: fd.get("path"),
        title: fd.get("title"),
        status: fd.get("status"),
        body_html: fd.get("body_html"),
      });
      toast("Page saved", "ok");
      refreshPages();
    } catch (e) { fail(e); }
  });

  $("#btn-page-preview").addEventListener("click", () => {
    const frame = $("#page-preview");
    frame.srcdoc = $("#page-editor-form").elements.body_html.value || "";
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
