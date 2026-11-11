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

async function api(method, path, query) {
  const res = await hatamoto.route(method, path, null, query);
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

async function main() {
  await hatamoto.ready;
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
