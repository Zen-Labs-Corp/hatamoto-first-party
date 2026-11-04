// Subscriber sync admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge; all text is set with textContent.
// The API key is never sent to this page.
const $ = (sel) => document.querySelector(sel);

const PROVIDERS = { mailchimp: "Mailchimp", brevo: "Brevo", mailerlite: "MailerLite", kit: "Kit" };

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
  add("Provider", PROVIDERS[data.provider] || data.provider);
  add("List", data.list_id);
  add("Forms", data.forms.length ? data.forms.join(", ") : "none chosen, nothing is sent");
}

function fillLog(rows) {
  const body = $("#log tbody");
  body.replaceChildren();
  for (const r of rows) {
    body.append(el("tr", {},
      el("td", { textContent: new Date(r.created_at).toLocaleString() }),
      el("td", { textContent: r.form_slug }),
      el("td", { textContent: r.status, className: `status-${r.status}` }),
      el("td", { textContent: r.code ? String(r.code) : "", className: "num" }),
      el("td", { textContent: r.reason || "" })));
  }
  $("#empty").hidden = rows.length > 0;
  $("#log").hidden = rows.length === 0;
}

async function load() {
  const data = await api("GET", "/log");
  fillStatus(data);
  fillLog(data.rows || []);
}

async function testConnection() {
  const out = $("#test-result");
  out.className = "";
  out.textContent = "Checking...";
  const data = await api("POST", "/test", {});
  out.className = data.ok ? "ok" : "bad";
  out.textContent = data.ok ? `OK: found list "${data.list_name}"` : `Failed: ${data.error}`;
}

(async () => {
  await hatamoto.ready;
  $("#test").addEventListener("click", () => run(testConnection));
  run(load);
})();
