// CRM sync admin page. It runs in a sandboxed iframe and reaches the plugin
// only through the hatamoto bridge; all text is set with textContent.
// Credentials are never sent to this page.
const $ = (sel) => document.querySelector(sel);

const PROVIDERS = { hubspot: "HubSpot", pipedrive: "Pipedrive", salesforce: "Salesforce" };
const LINK_HOSTS = [".hubspot.com", ".pipedrive.com", ".salesforce.com", ".force.com"];

let formFields = []; // [{name, label}] of the watched forms
let crmFields = [];  // [{name, label, required}]; empty when not loaded
let mapRows = [];    // [{form_field, crm_field}] shown in the table

function el(tag, props = {}, ...kids) {
  const node = Object.assign(document.createElement(tag), props);
  for (const kid of kids) node.append(kid);
  return node;
}

function say(msg, isError) {
  const n = $("#notice");
  n.textContent = msg;
  n.classList.toggle("error", Boolean(isError));
  n.hidden = !msg;
}

async function api(method, path, body, query) {
  const res = await hatamoto.route(method, path, body ?? null, query);
  let data = {};
  try { data = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

async function run(fn) {
  try { say(""); await fn(); } catch (e) { say(e.message, true); }
}

function fillStatus(data) {
  const dl = $("#status");
  dl.replaceChildren();
  $("#setup").hidden = data.configured;
  if (!data.configured) return;
  const add = (k, v) => dl.append(el("dt", { textContent: k }), el("dd", { textContent: v }));
  let provider = PROVIDERS[data.provider] || data.provider;
  if (data.provider === "salesforce") provider += data.object === "contact" ? " (Contact)" : " (Lead)";
  add("Provider", provider);
  add("Forms", data.forms.length ? data.forms.join(", ") : "none chosen, nothing is sent");
}

// recordLink is a link only for an https address on the CRM's own hosts.
function recordLink(url) {
  let u;
  try { u = new URL(url); } catch { return null; }
  const host = u.hostname.toLowerCase();
  if (u.protocol !== "https:" || !LINK_HOSTS.some((s) => host.endsWith(s))) return null;
  return el("a", { href: u.href, target: "_blank", rel: "noopener noreferrer", textContent: "Open" });
}

function fillLog(rows) {
  const body = $("#log tbody");
  body.replaceChildren();
  for (const r of rows) {
    const link = r.record_url ? recordLink(r.record_url) : null;
    body.append(el("tr", {},
      el("td", { textContent: new Date(r.created_at).toLocaleString() }),
      el("td", { textContent: r.form_slug }),
      el("td", { textContent: r.status, className: `status-${r.status}` }),
      el("td", { textContent: r.code ? String(r.code) : "", className: "num" }),
      el("td", { textContent: r.message || "" }),
      el("td", {}, ...(link ? [link] : []))));
  }
  $("#empty").hidden = rows.length > 0;
  $("#log").hidden = rows.length === 0;
}

// choice is a dropdown of [value, text] options; the current value is kept
// even when it is not among them.
function choice(options, current) {
  const sel = el("select");
  const list = [["", ""], ...options];
  if (current && !list.some(([v]) => v === current)) list.push([current, current]);
  for (const [value, text] of list) sel.append(el("option", { value, textContent: text }));
  sel.value = current;
  return sel;
}

function crmLabel(f) {
  return `${f.label || f.name} (${f.name})${f.required ? " *" : ""}`;
}

function fillMap() {
  const body = $("#map tbody");
  body.replaceChildren();
  const haveCRM = crmFields.length > 0;
  $("#map-note").hidden = haveCRM;
  mapRows.forEach((row, i) => {
    const form = choice(formFields.map((f) => [f.name, f.name]), row.form_field);
    form.className = "form-field";
    const crm = haveCRM
      ? choice(crmFields.map((f) => [f.name, crmLabel(f)]), row.crm_field)
      : el("input", { type: "text", value: row.crm_field, maxLength: 100 });
    crm.className = "crm-field";
    const remove = el("button", { type: "button", textContent: "✕", title: "Remove row" });
    remove.addEventListener("click", () => {
      readMap();
      mapRows.splice(i, 1);
      fillMap();
    });
    body.append(el("tr", {}, el("td", {}, form), el("td", {}, crm), el("td", {}, remove)));
  });
}

// readMap copies what is typed or chosen in the table back into mapRows.
function readMap() {
  mapRows = [...document.querySelectorAll("#map tbody tr")].map((tr) => ({
    form_field: tr.querySelector(".form-field").value.trim(),
    crm_field: tr.querySelector(".crm-field").value.trim(),
  }));
}

async function loadFields() {
  const data = await api("GET", "/fields");
  formFields = data.form_fields || [];
  crmFields = data.crm_fields || [];
  fillMap();
}

async function loadMap() {
  const data = await api("GET", "/map");
  mapRows = data.rows || [];
  $("#map-default").hidden = data.saved;
}

async function load() {
  const data = await api("GET", "/log");
  fillStatus(data);
  fillLog(data.rows || []);
  if (!data.configured) {
    mapRows = [];
    fillMap();
    return;
  }
  await loadMap();
  await loadFields();
}

async function saveMapping() {
  readMap();
  await api("PUT", "/map", { rows: mapRows });
  await loadMap();
  fillMap();
  say("Saved");
}

async function testConnection() {
  const out = $("#test-result");
  out.className = "";
  out.textContent = "Checking...";
  const data = await api("POST", "/test", {});
  out.className = data.ok ? "ok" : "bad";
  out.textContent = data.ok ? `OK: connected to "${data.account}"` : `Failed: ${data.error}`;
  if (data.ok) {
    readMap();
    await loadFields();
  }
}

(async () => {
  await hatamoto.ready;
  $("#test").addEventListener("click", () => run(testConnection));
  $("#save").addEventListener("click", () => run(saveMapping));
  $("#add").addEventListener("click", () => {
    readMap();
    mapRows.push({ form_field: "", crm_field: "" });
    fillMap();
  });
  run(load);
})();
