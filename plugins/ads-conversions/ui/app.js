// Server-side conversions admin page. It runs in a sandboxed iframe and reaches
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
    err.data = data;
    throw err;
  }
  return data;
}

const PLATFORMS = ["google", "tiktok", "linkedin"];
const LABELS = { google: "Google Ads", tiktok: "TikTok", linkedin: "LinkedIn" };
const LOG_NAMES = { google: "Google", tiktok: "TikTok", linkedin: "LinkedIn" };
const REASONS = {
  no_identifier: "no identifier",
  no_contact: "no email, phone or click ID",
  consent: "no consent",
  expired: "expired after 24 hours",
};
const STATUS_LABELS = { sent: "Sent", failed: "Failed", retrying: "Retrying", skipped: "Skipped", test: "Test" };

let state = { platforms: {}, rows: [], forms: [] };
const sessionTests = {};

const pad = (n) => String(n).padStart(2, "0");
function when(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
const day = (iso) => (iso ? iso.slice(0, 10) : "");

function short(id) {
  if (id.startsWith("test-")) return `${id.slice(0, 9)}…`;
  return id.length > 12 ? `${id.slice(0, 4)}…${id.slice(-4)}` : id;
}

function customerID(id) {
  return /^\d{10}$/.test(id) ? `${id.slice(0, 3)}-${id.slice(3, 6)}-${id.slice(6)}` : id;
}

function badge(status) {
  return el("span", { className: `badge ${status}`, textContent: STATUS_LABELS[status] || status });
}

function detail(r) {
  if (r.status === "skipped") return REASONS[r.reason] || r.reason || "";
  if (r.status === "failed" && r.reason === "expired") return REASONS.expired;
  if (r.status === "test") return r.message || r.code || "";
  return [r.code, r.status === "failed" || r.status === "retrying" ? r.message : ""].filter(Boolean).join(" ");
}

function fillLog() {
  const platform = $("#filter").value;
  const rows = state.rows.filter((r) => !platform || r.platform === platform);
  const body = $("#log tbody");
  body.replaceChildren();
  for (const r of rows) {
    body.append(el("tr", {},
      el("td", { textContent: when(r.created_at) }),
      el("td", { textContent: LOG_NAMES[r.platform] || "—" }),
      el("td", { textContent: r.form_slug || "" }),
      el("td", { textContent: short(r.submission_id || "") }),
      el("td", {}, badge(r.status)),
      el("td", { textContent: detail(r) })));
  }
  $("#log").hidden = rows.length === 0;
  $("#empty").hidden = rows.length > 0;
}

// stateLine is the card's connection line.
function stateLine(p, c) {
  if (p === "tiktok") {
    const pixel = c.pixel_code ? `Pixel ${c.pixel_code}` : "No pixel code";
    if (c.needs_reconnect) return `${pixel} · Reconnect needed: TikTok refused the token, enter a new one in Settings and press Test`;
    return `${pixel} · ${c.token_set ? "token set" : "no token"}`;
  }
  const expires = c.token_expires ? new Date(c.token_expires) : null;
  if (c.needs_reconnect) {
    return expires && expires < new Date() ? `Reconnect needed · token expired ${day(c.token_expires)}` : "Reconnect needed";
  }
  if (!c.connected) return "Not connected";
  if (p === "google" || c.has_refresh_token) return "Connected · token renews automatically";
  return expires ? `Connected · token expires ${day(c.token_expires)}` : "Connected";
}

function idLines(p, c) {
  if (p === "google") {
    return [
      c.customer_id && `Account ${customerID(c.customer_id)}`,
      c.login_customer_id && `Manager ${customerID(c.login_customer_id)}`,
      c.conversion_action_id && `Conversion action ${c.conversion_action_id}`,
    ];
  }
  if (p === "tiktok") return [c.event && `Event ${c.event}`, c.test_event_code && `Test code ${c.test_event_code}`];
  return [[c.rule_id && `Rule ${c.rule_id}`, c.ad_account_id && `account ${c.ad_account_id}`].filter(Boolean).join(" · ")];
}

function lastTest(p) {
  if (sessionTests[p]) return sessionTests[p];
  const row = state.rows.find((r) => r.platform === p && r.status === "test");
  if (!row) return null;
  return { ok: row.reason === "ok", message: row.message || "" };
}

function card(p) {
  const c = state.platforms[p] || {};
  const node = el("article", { className: "card" });
  node.append(el("header", {}, el("span", { textContent: LABELS[p] }),
    el("span", { className: `badge ${c.enabled ? "on" : "off"}`, textContent: c.enabled ? "on" : "off" })));
  node.append(el("p", { textContent: stateLine(p, c) }));
  for (const line of idLines(p, c)) if (line) node.append(el("p", { className: "muted", textContent: line }));
  if (c.missing && c.missing.length) node.append(el("p", { className: "err", textContent: `Set: ${c.missing.join(", ")}` }));

  const buttons = el("div", { className: "buttons" });
  const oauth = p !== "tiktok";
  if (oauth) {
    const connected = c.connected && !c.needs_reconnect;
    const label = c.needs_reconnect ? "Reconnect" : connected ? "" : "Connect";
    if (label) buttons.append(el("button", { className: "primary", textContent: label, onclick: () => connect(p) }));
    if (c.connected) buttons.append(el("button", { textContent: "Disconnect", onclick: () => disconnect(p) }));
  }
  const testable = oauth ? c.connected && !c.needs_reconnect : c.configured;
  if (testable) buttons.append(el("button", { textContent: "Test", onclick: (e) => test(p, e.target) }));
  node.append(buttons);

  const t = lastTest(p);
  if (t) node.append(el("p", { className: `result ${t.ok ? "ok" : "err"}`, textContent: `${t.ok ? "Test OK" : "Test failed"}: ${t.message}` }));
  return node;
}

function fillCards() {
  $("#cards").replaceChildren(...PLATFORMS.map(card));
  $("#forms").textContent = state.forms.length
    ? `Sending for forms: ${state.forms.join(", ")}`
    : "No forms selected in Settings: nothing is sent.";
}

async function refresh() {
  state = await api("GET", "/log");
  fillCards();
  fillLog();
}

async function guarded(fn) {
  try { say(""); await fn(); } catch (e) {
    const miss = e.data && e.data.missing && e.data.missing.length ? ` Set: ${e.data.missing.join(", ")}.` : "";
    say(e.message + miss, true);
  }
}

// connect asks for the consent address. A sandboxed page cannot open another
// site, so the owner opens the address in a new tab and comes back.
function connect(p) {
  return guarded(async () => {
    const r = await api("GET", `/connect/${p}`);
    const help = $("#connect-help");
    help.replaceChildren(
      `Open this address in a new browser tab to connect ${LABELS[p]}, approve access, then come back and reload this page (valid for 10 minutes): `,
      el("code", { textContent: r.authorization_url }));
    help.hidden = false;
  });
}

function disconnect(p) {
  return guarded(async () => {
    if (!confirm(`Forget the ${LABELS[p]} connection and its tokens?`)) return;
    await api("POST", `/disconnect/${p}`);
    delete sessionTests[p];
    $("#connect-help").hidden = true;
    await refresh();
  });
}

function test(p, btn) {
  return guarded(async () => {
    btn.disabled = true;
    try {
      const r = await api("POST", "/test", { platform: p });
      sessionTests[p] = { ok: Boolean(r.ok), message: r.message || "" };
    } catch (e) {
      sessionTests[p] = { ok: false, message: e.message };
    }
    await refresh();
  });
}

async function main() {
  await hatamoto.ready;
  $("#filter").onchange = fillLog;
  await refresh();
}

main().catch((e) => say(e.message, true));
