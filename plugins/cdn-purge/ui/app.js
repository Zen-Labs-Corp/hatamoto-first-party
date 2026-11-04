// CDN purge admin page. It runs in a sandboxed iframe and reaches the plugin
// only through the hatamoto bridge; all text is set with textContent. The
// sandbox ignores window.confirm, so "Purge everything" asks in the page.
const $ = (sel) => document.querySelector(sel);

const PROVIDERS = { cloudflare: "Cloudflare", bunny: "Bunny", fastly: "Fastly" };
const TARGETS = { cloudflare: "Zone", bunny: "Pull zone", fastly: "Service" };

let state = { provider: "", configured: false };

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

function fillSummary(st) {
  const dl = $("#summary");
  dl.replaceChildren();
  const add = (k, v) => dl.append(el("dt", { textContent: k }), el("dd", { textContent: v }));
  add("Provider", PROVIDERS[st.provider] || "not chosen");
  if (st.provider) add(TARGETS[st.provider] || "Target", st.target || "not set");
  add("API token", st.token_set ? "set" : "not set");
  $("#setup").hidden = st.configured;
  $("#test").disabled = !st.configured;
  $("#purge-all").disabled = !st.configured;
  if (!st.configured) {
    $("#test-result").textContent = "";
    showConfirm(false);
  }
}

function resultCell(r) {
  if (r.error) return el("span", { className: "bad", textContent: r.error });
  return el("span", { className: "ok", textContent: `${r.status} OK` });
}

// A row without URLs is a purge of everything, except a skipped run, which
// purged nothing.
function urlsCell(r) {
  const urls = r.urls;
  if (!urls || urls.length === 0) {
    return el("span", { className: "none", textContent: r.error === "set Site URL" ? "(nothing purged)" : "(everything)" });
  }
  const text = urls.length > 1 ? `${urls[0]} +${urls.length - 1}` : urls[0];
  return el("span", { className: text.length > 60 ? "long" : "", textContent: text });
}

function fillLog(list) {
  const body = $("#log tbody");
  body.replaceChildren();
  for (const r of list) {
    body.append(el("tr", {},
      el("td", { className: "when", textContent: new Date(r.at).toLocaleString() }),
      el("td", { className: "trigger", textContent: r.trigger }),
      el("td", { className: "url" }, urlsCell(r)),
      el("td", { className: "result" }, resultCell(r))));
  }
  $("#log").hidden = list.length === 0;
  $("#empty").hidden = list.length > 0;
}

async function load() {
  const data = await api("GET", "/log");
  state = data;
  fillSummary(data);
  fillLog(data.rows || []);
}

function showConfirm(show) {
  $("#confirm").hidden = !show;
  $("#purge-all").hidden = show;
  if (show) {
    const name = PROVIDERS[state.provider] || "the CDN";
    $("#confirm-text").textContent =
      `Purge everything from ${name}? Every cached page and file is dropped; the CDN refetches from your site.`;
  }
}

$("#test").addEventListener("click", () => run(async () => {
  const out = $("#test-result");
  out.className = "muted";
  out.textContent = "Testing...";
  const r = await api("POST", "/test");
  out.className = r.ok ? "ok" : "bad";
  out.textContent = r.ok ? `OK: ${r.message}` : r.message;
}));

$("#purge-all").addEventListener("click", () => showConfirm(true));
$("#cancel").addEventListener("click", () => showConfirm(false));

$("#confirm-go").addEventListener("click", () => run(async () => {
  showConfirm(false);
  const r = await api("POST", "/purge-all");
  say(r.ok ? "Everything was purged." : `Purge failed: ${r.error}`, !r.ok);
  await load();
}));

(async () => {
  await hatamoto.ready;
  await run(load);
})();
