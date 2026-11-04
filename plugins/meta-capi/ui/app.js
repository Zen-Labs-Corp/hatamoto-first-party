// Meta Conversions API admin page. It runs in a sandboxed iframe and reaches
// the plugin only through the hatamoto bridge; all text is set with textContent.
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

async function api(method, path, body) {
  const res = await hatamoto.route(method, path, body ?? null);
  let data = {};
  try { data = JSON.parse(res.body || "{}"); } catch { /* not JSON */ }
  if (res.status >= 400) {
    const err = new Error(data.error || `HTTP ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return data;
}

const SKIP_REASONS = {
  no_contact: "no email or phone",
  consent: "no consent",
  not_configured: "not configured",
  form_filtered: "form not selected",
};

const short = (id) => (id.length > 12 ? `${id.slice(0, 4)}…${id.slice(-4)}` : id);

function badge(r) {
  const kind = r.test ? "test" : r.status;
  const label = { sent: "Sent", failed: "Failed", skipped: "Skipped", test: "Test" }[kind] || kind;
  return el("span", { className: `badge ${kind}`, textContent: label });
}

function detail(r) {
  if (r.status === "skipped") return SKIP_REASONS[r.reason] || r.reason;
  if (r.status === "failed" && r.reason === "interrupted") return "interrupted, outcome unknown";
  const parts = [];
  if (r.meta_code !== null && r.meta_code !== undefined) parts.push(String(r.meta_code));
  if (r.status === "failed" && r.meta_message) parts.push(r.meta_message);
  return parts.join(" ");
}

function fillLog(rows) {
  const body = $("#log tbody");
  body.replaceChildren();
  for (const r of rows) {
    body.append(el("tr", {},
      el("td", { textContent: new Date(r.created_at).toLocaleString() }),
      el("td", { textContent: r.form_slug }),
      el("td", { textContent: short(r.submission_id) }),
      el("td", {}, badge(r)),
      el("td", { textContent: detail(r) })));
  }
  $("#log").hidden = rows.length === 0;
  $("#empty").hidden = rows.length > 0;
}

function fillSummary(d) {
  $("#status").textContent = d.configured
    ? `Sending Lead events to pixel ${d.pixel_id} (Graph ${d.graph_version}).`
    : "Not sending: set Pixel ID and Access token in Settings.";
  $("#forms").textContent = d.forms.length ? d.forms.join(", ") : "all forms";
  $("#testcode").textContent = d.test_event_code || "not set";
  $("#forms-hint").hidden = d.forms_read_granted !== false;
}

async function refresh() {
  const d = await api("GET", "/log");
  fillSummary(d);
  fillLog(d.rows);
}

function result(msg, isError) {
  const n = $("#test-result");
  n.textContent = msg;
  n.className = isError ? "err" : "ok";
}

async function sendTest() {
  const btn = $("#send-test");
  btn.disabled = true;
  result("");
  try {
    await api("POST", "/test");
    result("Sent. Check Events Manager > Test events.", false);
  } catch (e) {
    result(e.status === 502 ? `Meta said: ${e.message}` : e.message, true);
  }
  btn.disabled = false;
  try { await refresh(); } catch (e) { say(e.message, true); }
}

async function main() {
  await hatamoto.ready;
  $("#send-test").onclick = sendTest;
  await refresh();
}

main().catch((e) => say(e.message, true));
