// Broken link checker admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge. Text is set with textContent only.
const $ = (sel) => document.querySelector(sel);

const REQUIRED = ["entities.read", "redirects.read", "http.fetch", "http.fetch.any", "db", "kv", "settings", "log"];
const TABS = [
  ["broken", "Broken"],
  ["redirected", "Redirected"],
  ["ok", "OK"],
  ["ignored", "Ignored"],
  ["unknown", "Unknown"],
];

const state = { status: "broken", cursor: "", links: [], counts: {}, pagesTotal: 0, ready: false, permissions: "" };

function el(tag, props = {}, ...kids) {
  const node = Object.assign(document.createElement(tag), props);
  for (const kid of kids) node.append(kid);
  return node;
}

async function api(method, path, query) {
  const res = await hatamoto.route(method, path, null, query);
  let data = {};
  try { data = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) {
    const err = new Error(data.error || "The request failed (" + res.status + ").");
    err.code = res.status;
    throw err;
  }
  return data;
}

function notice(msg, warn) {
  const n = $("#notice");
  n.textContent = msg;
  n.classList.toggle("warn", Boolean(warn));
  n.hidden = !msg;
}

// ago turns a timestamp into "4 min ago".
function ago(iso) {
  const t = Date.parse(iso);
  if (!iso || Number.isNaN(t)) return "";
  const s = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return Math.round(s / 60) + " min ago";
  if (s < 86400) return Math.round(s / 3600) + "h ago";
  return Math.round(s / 86400) + " d ago";
}

function day(iso) {
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? "" : t.toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

function total(c) {
  return (c.broken || 0) + (c.redirected || 0) + (c.ok || 0) + (c.ignored || 0) + (c.unknown || 0) + (c.pending || 0);
}

// resultText is the short word in the Status column.
function resultText(l) {
  switch (l.status) {
    case "redirected": return "redirect";
    case "ignored": return "ignored";
    case "unknown": return l.error || (l.http_status ? String(l.http_status) : "unknown");
    default: return l.http_status ? String(l.http_status) : l.error || l.status;
  }
}

function metaText(l) {
  const parts = [];
  if (l.last_checked) parts.push("checked " + ago(l.last_checked));
  if (l.status === "broken" && l.first_broken) parts.push("broken since " + day(l.first_broken));
  if (l.status === "unknown") parts.push("checked again tomorrow");
  return parts.join(", ");
}

// copyPath selects the path in a readonly input and copies it; when the
// clipboard is blocked the selection stays and the note says what to press.
async function copyPath(box, value) {
  let input = box.querySelector("input");
  if (!input) {
    input = el("input", { type: "text", readOnly: true, value });
    input.setAttribute("aria-label", "Editor path");
    box.append(input);
  }
  box.hidden = false;
  input.focus();
  input.select();
  let done = false;
  try {
    await navigator.clipboard.writeText(value);
    done = true;
  } catch (e) {
    try { done = document.execCommand("copy"); } catch (e2) { done = false; }
  }
  const note = $("#copy-note");
  note.textContent = done ? "Copied." : "Press Ctrl/Cmd+C to copy the selected text.";
  note.hidden = false;
}

function pageBlock(p) {
  const box = el("div", { className: "copy", hidden: true });
  const button = el("button", { type: "button", textContent: "Copy editor path" });
  button.addEventListener("click", () => copyPath(box, "/entities/" + p.entity_id));
  return el("div", { className: "page" },
    el("span", { textContent: p.entity_name || "(untitled)" }),
    el("span", { className: "path", textContent: p.entity_path || "" }),
    el("div", { className: "copy" }, button),
    box,
  );
}

function row(l) {
  const link = el("td", { className: "link" }, el("span", { textContent: l.url }));
  const meta = metaText(l);
  if (meta) link.append(el("span", { className: "meta", textContent: meta }));

  const shown = l.pages || [];
  const pages = el("td", {}, ...shown.map(pageBlock));
  const more = (l.page_count || 0) - shown.length;
  if (more > 0) pages.append(el("div", { className: "meta", textContent: "+" + more + " more pages" }));

  const recheck = el("button", { type: "button", textContent: "Recheck" });
  recheck.addEventListener("click", () => change(recheck, l, "recheck"));
  const ignore = el("button", { type: "button", textContent: l.status === "ignored" ? "Un-ignore" : "Ignore" });
  ignore.addEventListener("click", () => change(ignore, l, "ignore"));

  return el("tr", {},
    el("td", { className: "status " + l.status, textContent: resultText(l) }),
    link,
    pages,
    el("td", { className: "actions" }, recheck, ignore),
  );
}

function renderTabs() {
  const box = $("#tabs");
  box.replaceChildren();
  for (const [key, label] of TABS) {
    const n = state.counts[key] || 0;
    if (key === "unknown" && n === 0 && state.status !== "unknown") continue;
    const b = el("button", { type: "button", textContent: label + " " + n });
    b.setAttribute("aria-pressed", String(state.status === key));
    b.addEventListener("click", () => { state.status = key; load(); });
    box.append(b);
  }
}

function emptyText() {
  if (state.status === "broken") {
    const checked = total(state.counts) - (state.counts.pending || 0);
    return "No broken links found. Checked " + checked + " links on " + state.pagesTotal + " pages.";
  }
  return "No " + state.status + " links.";
}

function render() {
  renderTabs();
  const rows = $("#rows");
  rows.replaceChildren(...state.links.map(row));
  $("#table").hidden = state.links.length === 0;
  const empty = $("#empty");
  empty.textContent = state.links.length === 0 && state.ready ? emptyText() : "";
  empty.hidden = !empty.textContent;
  $("#more").hidden = !state.cursor;
}

function renderHeader(data) {
  state.counts = data.counts || {};
  state.pagesTotal = data.pages_total || 0;
  const scan = data.last_scan ? "Last scan: " + ago(data.last_scan) : "No scan yet";
  $("#scan").textContent = scan + " · Queue: " + (data.queue || 0) + " links waiting";
  const partial = $("#partial");
  partial.textContent = data.scan_partial ? "The last scan could not read every page; some links are not checked yet." : "";
  partial.hidden = !partial.textContent;
  const all = total(state.counts);
  const progress = $("#progress");
  if (all === 0) {
    progress.textContent = "The first scan has not found any links yet. It runs every few minutes.";
  } else if (state.counts.pending > 0) {
    progress.textContent = "First scan still running: checked " + (all - state.counts.pending) + " of " + all + " links so far.";
  } else {
    progress.textContent = "";
  }
  progress.hidden = !progress.textContent;
}

async function load(more) {
  const query = { status: state.status };
  if (more) query.cursor = state.cursor;
  try {
    const data = await api("GET", "/links", query);
    notice(state.permissions, true);
    renderHeader(data);
    state.links = more ? state.links.concat(data.links) : data.links;
    state.cursor = data.next_cursor || "";
    state.ready = true;
    render();
  } catch (e) {
    state.links = [];
    state.cursor = "";
    state.ready = false;
    render();
    if (e.code === 503) {
      notice("The database is not ready. Check that the db permission is granted under Plugins > Permissions, then reload.", true);
    } else {
      notice(e.message, true);
    }
  }
}

async function change(button, l, action) {
  button.disabled = true;
  try {
    await api("POST", "/" + action + "/" + l.id);
    notice(action === "recheck" ? "Queued: the link is checked on the next run." : "");
    await load();
  } catch (e) {
    notice(e.message, true);
    button.disabled = false;
  }
}

async function missingPermissions() {
  try {
    const p = await hatamoto.host("permissions", {});
    const result = p && p.ok !== false && (p.result || p);
    if (!result || !Array.isArray(result.granted)) return [];
    return REQUIRED.filter((name) => !result.granted.includes(name));
  } catch (e) {
    return [];
  }
}

async function start() {
  await hatamoto.ready;
  $("#more").addEventListener("click", () => load(true));
  const missing = await missingPermissions();
  if (missing.length) {
    state.permissions = "Permissions not granted: " + missing.join(", ") + ". Grant them under Plugins > Permissions.";
    notice(state.permissions, true);
  }
  if (missing.includes("db")) {
    renderTabs();
    return;
  }
  await load();
}

start();
