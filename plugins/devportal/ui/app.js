// Developer accounts admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge. All text is set with textContent.
const $ = (sel) => document.querySelector(sel);

let page = 1;
let lastPage = 1;

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
  const res = await hatamoto.route(method, path, body ?? null, query);
  let data = {};
  try { data = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

async function run(fn) {
  try { say(""); await fn(); } catch (e) { say(e.message, true); }
}

const day = (iso) => new Date(iso).toLocaleDateString("en", { day: "numeric", month: "short", timeZone: "UTC" });

// keyCell shows the first four characters of the key id; the full id is the title.
function keyCell(keyId) {
  const td = el("td", { textContent: keyId ? `${keyId.slice(0, 4)}…` : "none" });
  if (keyId) td.setAttribute("title", keyId);
  return td;
}

async function setStatus(row, action) {
  await run(async () => {
    await api("POST", `/admin/accounts/${row.id}/${action}`);
    await load();
  });
}

function accountRow(a) {
  const suspended = a.status === "suspended";
  const action = suspended ? "unsuspend" : "suspend";
  const button = el("button", { type: "button", textContent: suspended ? "Unsuspend" : "Suspend" });
  button.addEventListener("click", () => setStatus(a, action));
  return el("tr", {},
    el("td", { textContent: `@${a.login}` }),
    el("td", { textContent: a.display_name }),
    el("td", { textContent: a.email }),
    keyCell(a.key_id),
    el("td", { textContent: a.plan }),
    el("td", { textContent: day(a.created_at) }),
    el("td", { textContent: suspended ? "Suspended" : "", className: suspended ? "suspended" : "" }),
    el("td", {}, button),
  );
}

function render(data) {
  $("#heading").textContent = `Developers (${data.total})`;
  const list = $("#list");
  list.replaceChildren();
  if (!data.rows.length) {
    list.append(el("p", { className: "muted", textContent: $("#search").value ? "No developer matches." : "No developer accounts yet." }));
  } else {
    const head = ["Login", "Name", "Email", "Key", "Plan", "Joined", "", ""].map((t) => el("th", { textContent: t }));
    list.append(el("table", {}, el("thead", {}, el("tr", {}, ...head)), el("tbody", {}, ...data.rows.map(accountRow))));
  }
  lastPage = Math.max(1, Math.ceil(data.total / data.page_size));
  page = data.page;
  $("#page").textContent = `Page ${page} of ${lastPage}`;
  $("#prev").disabled = page <= 1;
  $("#next").disabled = page >= lastPage;
}

async function load() {
  render(await api("GET", "/admin/accounts", { q: $("#search").value.trim(), page: String(page) }));
}

// ---- Review tab ----

const mb = (n) => `${(n / 1048576).toFixed(1)} MB`;

function line(...kids) {
  return el("p", { className: "line" }, ...kids);
}

// action builds a button. With ask set it first reveals a reason input in place
// and a Confirm button; the reason goes to the route as JSON.
function action(label, path, ask) {
  const box = el("span", { className: "actions" });
  const button = el("button", { type: "button", textContent: label });
  const send = (reason) => run(async () => {
    const out = await api("POST", path, undefined, ask ? { reason } : {});
    await loadReview();
    if (out.published === false) {
      say(out.warning || "Saved, but the developer index could not be published.", true);
      $("#notice").scrollIntoView({ block: "nearest" });
      return;
    }
    say(`${label}: done.`);
  });
  button.addEventListener("click", () => {
    if (!ask) { send(); return; }
    const input = el("input", { type: "text", maxLength: 200, placeholder: "Reason" });
    input.setAttribute("aria-label", `Reason for ${label}`);
    const confirm = el("button", { type: "button", textContent: "Confirm" });
    const cancel = el("button", { type: "button", textContent: "Cancel" });
    confirm.addEventListener("click", () => {
      if (!input.value.trim()) { say("A reason is required.", true); return; }
      send(input.value.trim());
    });
    cancel.addEventListener("click", () => box.replaceChildren(button));
    box.replaceChildren(input, confirm, cancel);
    input.focus();
  });
  box.append(button);
  return box;
}

function checksLine(checks) {
  const failed = (checks || []).filter((c) => !c.ok);
  if (!failed.length) return line("Checks: all passed");
  return line(el("span", { className: "bad", textContent: `Checks failed: ${failed.map((c) => c.text).join("; ")}` }));
}

function keyText(keyId) {
  return keyId ? `key ${keyId.slice(0, 4)}…` : "no key";
}

function head(s) {
  const who = el("span", { className: "muted", textContent: ` ${s.kind}, @${s.login}, ${keyText(s.key_id)}` });
  if (s.key_id) who.setAttribute("title", s.key_id);
  return line(el("strong", { textContent: `${s.name} ${s.version}` }), who);
}

function waitingItem(s) {
  const hosts = (s.allowed_hosts || []).join(", ");
  return el("div", { className: "item" },
    head(s),
    line(el("span", { className: "muted", textContent: `${mb(s.size)}, permissions: ${(s.permissions || []).join(", ") || "none"}${hosts ? `, hosts: ${hosts}` : ""}` })),
    line(el("code", { textContent: s.url }), document.createTextNode(" "), el("code", { textContent: s.source })),
    checksLine(s.checks),
    el("div", { className: "actions" },
      action("Approve", `/admin/submissions/${s.id}/approve`, false),
      action("Reject", `/admin/submissions/${s.id}/reject`, true)),
  );
}

function listedItem(l) {
  const note = l.status === "active" ? "" : ` (${l.status}${l.reason ? `: ${l.reason}` : ""})`;
  const failures = l.check_failures > 0 ? `, ${l.check_failures} failed recheck${l.check_failures === 1 ? "" : "s"}` : "";
  return el("div", { className: "item" },
    head(l),
    line(el("span", { className: "muted", textContent: `${mb(l.size)}, listed ${day(l.listed_at)}${note}${failures}` })),
    el("div", { className: "actions" },
      action("Revoke version", `/admin/listings/${l.id}/revoke`, true),
      action("Revoke developer key", `/admin/accounts/${l.account}/revoke-key`, true)),
  );
}

function revokedItem(v) {
  const what = v.name ? `${v.name} ${v.version || ""}`.trim() : `developer key ${String(v.signer_key || "").slice(0, 8)}…`;
  return el("div", { className: "item" },
    line(el("strong", { textContent: what }), el("span", { className: "muted", textContent: ` ${day(v.created_at)}, ${v.reason}` })),
  );
}

function section(title, rows, build, empty) {
  const box = el("div", {}, el("h3", { textContent: `${title} (${rows.length})` }));
  if (!rows.length) box.append(el("p", { className: "muted", textContent: empty }));
  for (const row of rows) box.append(build(row));
  return box;
}

async function loadReview() {
  const d = await api("GET", "/admin/submissions");
  $("#review").replaceChildren(
    section("Waiting", d.waiting, waitingItem, "Nothing is waiting."),
    section("Listed", d.listed, listedItem, "Nothing is listed."),
    section("Revoked", d.revoked, revokedItem, "Nothing is revoked."),
  );
}

function showTab(name) {
  for (const t of ["accounts", "review"]) {
    $(`#panel-${t}`).hidden = t !== name;
    $(`#tab-${t}`).setAttribute("aria-selected", String(t === name));
  }
  say("");
  run(name === "review" ? loadReview : load);
}

async function main() {
  await hatamoto.ready;
  $("#tab-accounts").addEventListener("click", () => showTab("accounts"));
  $("#tab-review").addEventListener("click", () => showTab("review"));
  let timer;
  $("#search").addEventListener("input", () => {
    clearTimeout(timer);
    timer = setTimeout(() => { page = 1; run(load); }, 250);
  });
  $("#prev").addEventListener("click", () => { page = Math.max(1, page - 1); run(load); });
  $("#next").addEventListener("click", () => { page += 1; run(load); });
  await run(load);
}

main();
