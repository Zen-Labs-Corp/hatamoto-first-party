// Payment links admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge. Text is set with textContent or
// value, never as markup. The frame cannot download or reliably use the
// clipboard, so addresses and code are shown in fields that select on click.
const $ = (sel) => document.querySelector(sel);

const WEBHOOK_NOTE = "Put your site address in front, for example https://example.com/p/payment-links/webhook. In Stripe, send the events checkout.session.completed and checkout.session.async_payment_succeeded to it.";
const EMPTY_NOTE = "No payments yet. Add the webhook in Stripe (see the address above) and make a test payment.";
const CSV_PATH = "/api/v1/p/payment-links/payments.csv";

let page = 1;

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
  if (res.status >= 400) {
    const err = new Error(data.error || `HTTP ${res.status}`);
    err.data = data;
    throw err;
  }
  return data;
}

async function run(fn) {
  try { say(""); await fn(); } catch (e) { say(e.message, true); }
}

// selectOnClick selects a field's text on click and tries to copy it; the
// sandbox may refuse, so the hint says to press Ctrl+C.
function selectOnClick(field) {
  field.addEventListener("click", () => {
    field.select();
    try {
      if (document.execCommand("copy")) say("Copied. If it did not work, press Ctrl+C.");
      else say("Selected. Press Ctrl+C to copy.");
    } catch { say("Selected. Press Ctrl+C to copy."); }
  });
}

function copyField(value, tag = "input") {
  const field = el(tag, { className: "copy", readOnly: true, value });
  selectOnClick(field);
  return field;
}

function table(headers, rows) {
  const head = el("tr", {}, ...headers.map(([text, num]) => el("th", { textContent: text, className: num ? "num" : "" })));
  const body = rows.map((cells) => el("tr", {}, ...cells.map(([text, num, node]) => el("td", { className: num ? "num" : "" }, node || document.createTextNode(text)))));
  return el("table", {}, el("thead", {}, head), el("tbody", {}, ...body));
}

// buttonCode is the template line that renders the button for a link.
function buttonCode(l) {
  return `{{ template "plugin/payment_links/button" (dict "label" ${JSON.stringify(l.label)} "url" ${JSON.stringify(l.url)}) }}`;
}

function renderSetup(s) {
  $("#key-status").textContent = s.key_set ? "set" : "not set (add it in the plugin settings)";
  $("#webhook").value = s.webhook_path;
  $("#webhook-note").textContent = WEBHOOK_NOTE;
  $("#csv").value = CSV_PATH;
  const box = $("#links");
  box.replaceChildren();
  if (!s.links.length) {
    box.append(el("p", { className: "muted", textContent: "No links yet. Add your Stripe Payment Links in the plugin settings." }));
    return;
  }
  box.append(table(
    [["Label"], ["Kind"], ["URL"], ["Button code"]],
    s.links.map((l) => [[l.label], [l.kind], [l.url, false, el("span", { className: "url", textContent: l.url })], ["", false, copyField(buttonCode(l), "textarea")]]),
  ));
}

function renderTotals(totals) {
  const box = $("#totals");
  box.replaceChildren();
  if (!totals.length) {
    box.append(el("p", { className: "muted", textContent: "Nothing yet." }));
    return;
  }
  const months = new Map();
  for (const t of totals) {
    if (!months.has(t.month)) months.set(t.month, []);
    months.get(t.month).push(`${t.display} (${t.count})`);
  }
  box.append(table([["Month"], ["Totals"]], [...months].map(([m, parts]) => [[m], [parts.join("   ")]])));
}

function renderPayments(data) {
  const box = $("#payments");
  box.replaceChildren();
  if (!data.payments.length) {
    box.append(el("p", { className: "muted", textContent: EMPTY_NOTE }));
  } else {
    box.append(table(
      [["Date"], ["Label"], ["Kind"], ["Amount", true], ["Email"]],
      data.payments.map((p) => [[p.created.slice(0, 10)], [p.label], [p.kind], [p.display, true], [p.email]]),
    ));
  }
  $("#older").disabled = !data.has_more;
  $("#newer").disabled = data.page <= 1;
}

async function loadPayments() {
  const data = await api("GET", "/payments", null, { page });
  page = data.page;
  renderPayments(data);
  renderTotals(data.totals);
}

async function load() {
  renderSetup(await api("GET", "/links"));
  await loadPayments();
}

async function turn(delta) {
  const was = page;
  page = Math.max(1, page + delta);
  try { await run(loadPayments); } finally { if ($("#notice").classList.contains("error")) page = was; }
}

async function main() {
  await hatamoto.ready;
  selectOnClick($("#webhook"));
  selectOnClick($("#csv"));
  $("#older").addEventListener("click", () => turn(1));
  $("#newer").addEventListener("click", () => turn(-1));
  await run(load);
}

main();
