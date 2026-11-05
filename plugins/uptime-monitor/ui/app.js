// Uptime monitor admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge. Text is set with textContent only.
const $ = (sel) => document.querySelector(sel);

const BADGES = { up: "UP", slow: "SLOW", down: "DOWN", config: "CONFIG", pending: "PENDING" };
const SSL_WARN_DAYS = 21;
const SSL_CRIT_DAYS = 7;
const DATA_SVG = "data:image/svg+xml";

// editing is null (form closed), 0 (adding) or the id of the monitor being edited.
const state = { data: null, editing: null, confirmDelete: false };

function el(tag, props = {}, ...kids) {
  const node = Object.assign(document.createElement(tag), props);
  for (const kid of kids) node.append(kid);
  return node;
}

async function api(method, path, body) {
  const res = await hatamoto.route(method, path, body ?? null);
  let data = {};
  try { data = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) {
    const err = new Error(data.error || "The request failed (" + res.status + ").");
    err.code = res.status;
    throw err;
  }
  return data;
}

function show(node, msg) {
  node.textContent = msg;
  node.hidden = !msg;
}

// ago turns a timestamp into "4 min ago".
function ago(iso) {
  const t = Date.parse(iso);
  if (!iso || Number.isNaN(t)) return "";
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (s < 60) return s + " s ago";
  if (s < 3600) return Math.round(s / 60) + " min ago";
  if (s < 86400) return Math.round(s / 3600) + " h ago";
  return Math.round(s / 86400) + " d ago";
}

function when(iso) {
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? "" : t.toLocaleString(undefined, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}

// span renders a length of time between two timestamps as "12 min", "3 h 5 min".
function span(fromIso, toIso) {
  const mins = Math.max(1, Math.round((Date.parse(toIso || new Date().toISOString()) - Date.parse(fromIso)) / 60000));
  if (mins < 60) return mins + " min";
  if (mins < 1440) return Math.floor(mins / 60) + " h " + (mins % 60) + " min";
  return Math.floor(mins / 1440) + " d " + Math.floor((mins % 1440) / 60) + " h";
}

function pct(v) {
  return v === null || v === undefined ? "-" : v + "%";
}

function avg(ms) {
  if (ms === null || ms === undefined) return "-";
  return ms < 1000 ? ms + " ms" : (ms / 1000).toFixed(1) + " s";
}

function cell(label, className, ...kids) {
  const td = el("td", { className }, ...kids);
  td.setAttribute("data-label", label);
  return td;
}

function sslCell(days) {
  if (days === null || days === undefined) return cell("SSL", "ssl", "-");
  const text = days < 0 ? "expired" : days + (days === 1 ? " day" : " days");
  const cls = days <= SSL_CRIT_DAYS ? "ssl ssl-crit" : days <= SSL_WARN_DAYS ? "ssl ssl-warn" : "ssl";
  return cell("SSL", cls, text);
}

// barImage is the 90-day bar. Only an SVG data URI the server built is used.
function barImage(src) {
  if (typeof src !== "string" || !src.startsWith(DATA_SVG)) return null;
  const img = el("img", { className: "bar-img", alt: "Uptime over the last 90 days" });
  img.setAttribute("src", src);
  return img;
}

function causeLine(m) {
  if (m.status === "down") {
    const since = m.since ? " since " + when(m.since) + " (" + span(m.since) + ")" : "";
    return el("span", { className: "cause down", textContent: (m.cause || "down") + since });
  }
  if (m.status === "config") return el("span", { className: "cause", textContent: m.cause || "Cannot be checked as configured." });
  return null;
}

function nameCell(m) {
  const td = cell("Monitor", "name", el("span", { className: "nm", textContent: m.name }));
  if (m.is_site) td.append(el("span", { className: "site", textContent: "This site" }));
  td.append(el("span", { className: "url", textContent: m.url }));
  const bar = barImage(m.bar);
  if (bar) td.append(bar);
  const cause = causeLine(m);
  if (cause) td.append(cause);
  return td;
}

function row(m) {
  const check = el("button", { type: "button", textContent: "Check now" });
  check.addEventListener("click", () => checkNow(check, m));
  const edit = el("button", { type: "button", textContent: "Edit" });
  edit.addEventListener("click", () => openForm(m));
  const status = BADGES[m.status] ? m.status : "pending";
  return el("tr", {},
    nameCell(m),
    cell("Status", "status", el("span", { className: "badge " + status, textContent: BADGES[status] })),
    cell("24h", "", pct(m.uptime_24h)),
    cell("30d", "", pct(m.uptime_30d)),
    cell("Avg", "", avg(m.avg_ms)),
    cell("Last check", "", m.last_check ? ago(m.last_check) : "-"),
    sslCell(m.cert_days_left),
    el("td", { className: "actions" }, check, edit),
  );
}

function incidentItem(i) {
  const end = i.ended_at ? when(i.ended_at) + " (" + span(i.started_at, i.ended_at) + ")" : "ongoing";
  return el("li", {},
    el("strong", { textContent: i.name || "(deleted monitor)" }),
    " " + when(i.started_at) + " -> " + end,
    el("span", { className: "cause", textContent: i.cause || "" }),
  );
}

function alertsText(s) {
  const mails = s.alert_emails === 0 ? "no alert emails" : s.alert_emails + (s.alert_emails === 1 ? " email" : " emails");
  const days = (s.ssl_warn_days || []).slice().sort((a, b) => b - a).join("/");
  return "Alerts: " + mails + ", threshold " + s.fail_threshold + " failures, SSL warnings at " + (days || "no") + " days.";
}

function notices(d) {
  const msgs = [];
  if (d.missing.length) msgs.push("Permissions not granted: " + d.missing.join(", ") + ". Grant them under Plugins > Permissions.");
  if (!d.db_ready && !d.missing.includes("db")) msgs.push("The database is not ready. Reload in a minute.");
  if (d.settings.invalid_emails.length) msgs.push("Not alert addresses, skipped (use plain addresses like name@example.com): " + d.settings.invalid_emails.join(", ") + ".");
  if (!d.settings.mail_configured) msgs.push("Mail is not set up on this site: alert emails stay queued until it is.");
  show($("#notice"), msgs.join(" "));
  const setup = d.db_ready && (d.monitors.length === 0 || d.settings.alert_emails === 0);
  show($("#setup"), setup ? "Setup: add your site as the first monitor and at least one alert email (under Settings above)." : "");
}

function render() {
  const d = state.data;
  if (!d) return;
  notices(d);
  $("#rows").replaceChildren(...d.monitors.map(row));
  $("#table").hidden = d.monitors.length === 0;
  $("#incidents").replaceChildren(...d.incidents.map(incidentItem));
  $("#no-incidents").hidden = d.incidents.length > 0;
  $("#alerts").textContent = alertsText(d.settings);
  $("#heartbeat").textContent = d.settings.heartbeat
    ? "Heartbeat: set (pinged after each good check of This site)"
    : "Heartbeat: not set";
  $("#test-alert").disabled = d.settings.alert_emails === 0;
  $("#add").disabled = !d.db_ready;
}

async function load() {
  try {
    state.data = await api("GET", "/overview");
    if (state.editing === null && state.data.db_ready && state.data.monitors.length === 0) openForm(null);
    render();
  } catch (e) {
    show($("#notice"), e.message);
  }
}

async function checkNow(button, m) {
  button.disabled = true;
  button.textContent = "Checking...";
  try {
    await api("POST", "/check-now", { id: m.id });
    await load();
  } catch (e) {
    show($("#notice"), e.message);
    button.disabled = false;
    button.textContent = "Check now";
  }
}

// openForm shows the form for adding (m is null) or editing a monitor.
function openForm(m) {
  state.editing = m ? m.id : 0;
  state.confirmDelete = false;
  $("#form-title").textContent = m ? "Edit monitor" : "Add monitor";
  $("#f-name").value = m ? m.name : "";
  $("#f-url").value = m ? m.url : "";
  $("#f-interval").value = m ? m.interval : "5m";
  $("#f-keyword").value = m ? m.keyword : "";
  $("#f-max").value = m && m.max_ms ? String(m.max_ms) : "";
  $("#f-site").checked = m ? m.is_site : false;
  $("#f-delete").hidden = !m;
  $("#f-delete").textContent = "Delete";
  show($("#form-error"), "");
  $("#form").hidden = false;
  $("#f-name").focus();
}

function closeForm() {
  state.editing = null;
  $("#form").hidden = true;
}

function formMonitor() {
  return {
    name: $("#f-name").value,
    url: $("#f-url").value,
    interval: $("#f-interval").value,
    keyword: $("#f-keyword").value,
    max_ms: parseInt($("#f-max").value, 10) || 0,
    is_site: $("#f-site").checked,
  };
}

async function save(e) {
  e.preventDefault();
  const body = state.editing
    ? { action: "update", id: state.editing, monitor: formMonitor() }
    : { action: "create", monitor: formMonitor() };
  $("#f-save").disabled = true;
  try {
    await api("POST", "/monitors", body);
    closeForm();
    await load();
  } catch (err) {
    show($("#form-error"), err.message);
  }
  $("#f-save").disabled = false;
}

// remove asks twice: the first click only changes the button text.
async function remove() {
  const button = $("#f-delete");
  if (!state.confirmDelete) {
    state.confirmDelete = true;
    button.textContent = "Confirm delete";
    return;
  }
  try {
    await api("POST", "/monitors", { action: "delete", id: state.editing });
    closeForm();
    await load();
  } catch (err) {
    show($("#form-error"), err.message);
  }
}

async function testAlert() {
  const button = $("#test-alert");
  const out = $("#test-result");
  button.disabled = true;
  out.textContent = "Sending...";
  try {
    const r = await api("POST", "/test-alert");
    out.textContent = r.sent > 0 ? "Test alert sent to " + r.sent + (r.sent === 1 ? " address." : " addresses.") : r.reason || "Nothing was sent.";
  } catch (e) {
    out.textContent = e.message;
  }
  button.disabled = !state.data || state.data.settings.alert_emails === 0;
}

async function start() {
  await hatamoto.ready;
  $("#add").addEventListener("click", () => openForm(null));
  $("#form").addEventListener("submit", save);
  $("#f-cancel").addEventListener("click", closeForm);
  $("#f-delete").addEventListener("click", remove);
  $("#test-alert").addEventListener("click", testAlert);
  await load();
}

start();
