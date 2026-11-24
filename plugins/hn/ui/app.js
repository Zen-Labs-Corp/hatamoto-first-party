"use strict";
// News moderation admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge. Every value is set with textContent.

const TABS = [
  ["queue", "Queue"],
  ["users", "Users"],
  ["domains", "Domains"],
  ["nets", "Networks"],
  ["jobs", "Jobs"],
  ["log", "Log"],
];

const $ = (sel) => document.querySelector(sel);

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

async function api(method, path, query, body) {
  const res = await hatamoto.route(method, path, body ? JSON.stringify(body) : null, query);
  let data = {};
  try { data = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

// run shows the failure of fn as a notice; it returns true when fn succeeded.
async function run(fn) {
  try { say(""); await fn(); return true; } catch (e) { say(e.message, true); return false; }
}

const stamp = (iso) => {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.toLocaleDateString("en", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" })} ${d.toLocaleTimeString("en", { hour: "2-digit", minute: "2-digit", hour12: false, timeZone: "UTC" })}`;
};

function button(label, onClick) {
  const b = el("button", { type: "button", textContent: label });
  b.addEventListener("click", onClick);
  return b;
}

function table(headers, rows, empty) {
  if (!rows.length) return el("p", { className: "muted", textContent: empty });
  const head = headers.map((t) => el("th", { textContent: t }));
  return el("table", {}, el("thead", {}, el("tr", {}, ...head)), el("tbody", {}, ...rows));
}

const cell = (text, className) => el("td", { textContent: text, className: className || "" });

let current = "queue";

// act runs one change, then reloads the open tab.
async function act(done, fn) {
  if (await run(fn)) {
    await load(current);
    say(done);
  }
}

// ---- Queue -----------------------------------------------------------------

function queueRow(it) {
  const label = it.kind === "story" ? `#${it.item_no}` : "comment";
  const buttons = [
    button("kill", () => act("Killed.", () => api("POST", "/admin/items/kill", null, { kind: it.kind, id: it.id }))),
    button("restore", () => act("Restored.", () => api("POST", "/admin/items/restore", null, { kind: it.kind, id: it.id }))),
  ];
  if (it.by) {
    buttons.push(button("ban user", () => act("Banned.", () => api("POST", `/admin/users/${encodeURIComponent(it.by)}/ban`, null, { mode: "dead", reason: "flagged" }))));
  }
  return el("tr", {},
    cell(label),
    cell(it.title),
    cell(it.by),
    cell(`${it.flags} flags`),
    cell(it.dead ? `dead (${it.cause})` : "", it.dead ? "dead" : ""),
    el("td", { className: "actions" }, ...buttons));
}

function renderQueue(data) {
  return table(["Item", "Title", "By", "Flags", "State", ""], (data.items || []).map(queueRow), "Nothing is flagged.");
}

// ---- Users -----------------------------------------------------------------

function userRow(u) {
  return el("tr", {},
    cell(u.name),
    cell(u.mode),
    cell(u.reason),
    cell(stamp(u.created_at)),
    el("td", { className: "actions" }, button("unban", () => act("Unbanned.", () => api("POST", `/admin/users/${encodeURIComponent(u.name)}/unban`, null, {})))));
}

function banForm() {
  const name = el("input", { type: "text", placeholder: "username", maxLength: 40 });
  const dead = el("input", { type: "radio", name: "mode", value: "dead", checked: true });
  const blocked = el("input", { type: "radio", name: "mode", value: "blocked" });
  const reason = el("input", { type: "text", placeholder: "reason", maxLength: 200 });
  const go = button("ban", () => {
    const who = name.value.trim();
    if (!who) return say("Enter a username.", true);
    return act("Banned.", () => api("POST", `/admin/users/${encodeURIComponent(who)}/ban`, null, { mode: blocked.checked ? "blocked" : "dead", reason: reason.value }));
  });
  return el("div", { className: "form" }, name,
    el("label", {}, dead, "dead"), el("label", {}, blocked, "blocked"), reason, go);
}

function renderUsers(data) {
  return el("div", {}, banForm(), table(["Member", "Mode", "Reason", "Since", ""], (data.users || []).map(userRow), "No member is banned."));
}

// ---- Domains ---------------------------------------------------------------

function domainRow(d) {
  return el("tr", {},
    cell(d.domain),
    cell(d.reason),
    cell(stamp(d.created_at)),
    el("td", { className: "actions" }, button("remove", () => act("Removed.", () => api("POST", "/admin/domains/remove", null, { domain: d.domain })))));
}

function domainForm() {
  const domain = el("input", { type: "text", placeholder: "example.com", maxLength: 253 });
  const reason = el("input", { type: "text", placeholder: "reason", maxLength: 200 });
  const go = button("block", () => {
    if (!domain.value.trim()) return say("Enter a domain.", true);
    return act("Blocked.", () => api("POST", "/admin/domains", null, { domain: domain.value, reason: reason.value }));
  });
  return el("div", { className: "form" }, domain, reason, go);
}

function renderDomains(data) {
  return el("div", {}, domainForm(), table(["Domain", "Reason", "Since", ""], (data.domains || []).map(domainRow), "No site is blocked."));
}

// ---- Networks --------------------------------------------------------------

function blockNet(key, reason, days) {
  return act("Blocked.", () => api("POST", "/admin/nets/ban", null, { net_key: key, reason, days }));
}

function netBanRow(b) {
  return el("tr", {},
    el("td", {}, el("code", { textContent: b.net_key })),
    cell(b.reason),
    cell(b.expires_at ? stamp(b.expires_at) : "never"),
    el("td", { className: "actions" }, button("unblock", () => act("Unblocked.", () => api("POST", "/admin/nets/unban", null, { net_key: b.net_key })))));
}

function noisyRow(n) {
  return el("tr", {},
    el("td", {}, el("code", { textContent: n.net_key })),
    cell(String(n.events)),
    el("td", { className: "actions" }, button("block", () => blockNet(n.net_key, "noisy", 7))));
}

function netForm() {
  const key = el("input", { type: "text", placeholder: "network key", maxLength: 128 });
  const reason = el("input", { type: "text", placeholder: "reason", maxLength: 200 });
  const days = el("input", { type: "number", placeholder: "days (0 = no end)", min: 0, max: 365, value: "7" });
  const go = button("block", () => {
    if (!key.value.trim()) return say("Enter a network key.", true);
    return blockNet(key.value.trim(), reason.value, Number.parseInt(days.value, 10) || 0);
  });
  return el("div", { className: "form" }, key, reason, days, go);
}

function renderNets(data) {
  return el("div", {}, netForm(),
    el("h3", { textContent: "Blocked networks" }),
    table(["Key", "Reason", "Until", ""], (data.bans || []).map(netBanRow), "No network is blocked."),
    el("h3", { textContent: "Busiest in the last 24 hours" }),
    table(["Key", "Events", ""], (data.noisy || []).map(noisyRow), "No traffic recorded."));
}

// ---- Jobs ------------------------------------------------------------------

function renderJobs() {
  const title = el("input", { type: "text", placeholder: "title", maxLength: 200 });
  const url = el("input", { type: "text", placeholder: "link (optional)", maxLength: 2000 });
  const text = el("textarea", { placeholder: "text (optional)" });
  const by = el("input", { type: "text", placeholder: "post as member (optional)", maxLength: 40 });
  const go = button("post job", async () => {
    await run(async () => {
      const data = await api("POST", "/admin/jobs", null, { title: title.value, url: url.value, text: text.value, by: by.value });
      say(`Posted: ${data.path}`);
    });
  });
  return el("div", { className: "form stack" }, title, url, text, by, go);
}

// ---- Log -------------------------------------------------------------------

function logRow(l) {
  return el("tr", {}, cell(stamp(l.at)), cell(l.actor), cell(l.action), cell(l.target), cell(l.detail));
}

function renderLog(data) {
  return table(["When (UTC)", "By", "Action", "Target", "Detail"], (data.log || []).map(logRow), "Nothing logged yet.");
}

// ---- Tabs ------------------------------------------------------------------

const LOADERS = {
  queue: async () => renderQueue(await api("GET", "/admin/queue", { view: "queue" })),
  users: async () => renderUsers(await api("GET", "/admin/queue", { view: "users" })),
  domains: async () => renderDomains(await api("GET", "/admin/queue", { view: "domains" })),
  nets: async () => renderNets(await api("GET", "/admin/queue", { view: "nets" })),
  jobs: async () => renderJobs(),
  log: async () => renderLog(await api("GET", "/admin/log")),
};

async function load(name) {
  await run(async () => {
    const node = await LOADERS[name]();
    $(`#body-${name}`).replaceChildren(node);
  });
}

async function show(name) {
  current = name;
  for (const [id] of TABS) {
    $(`#tab-${id}`).setAttribute("aria-selected", String(id === name));
    $(`#panel-${id}`).hidden = id !== name;
  }
  say("");
  await load(name);
}

function init() {
  for (const [id] of TABS) $(`#tab-${id}`).addEventListener("click", () => show(id));
  return show("queue");
}

if (typeof globalThis.__hnTest === "object") {
  globalThis.__hnTest.render = { renderQueue, renderUsers, renderDomains, renderNets, renderJobs, renderLog };
}
if (typeof hatamoto === "object") hatamoto.ready.then(init);
