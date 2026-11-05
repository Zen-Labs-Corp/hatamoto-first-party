// Form alerts admin page. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge; all text is set with textContent.
// Webhook URLs and tokens are never sent to this page.
const $ = (sel) => document.querySelector(sel);

const CHANNELS = [
  ["slack", "Slack", "Webhook URL"],
  ["teams", "Teams", "Workflow URL"],
  ["gchat", "Google Chat", "Webhook URL"],
  ["telegram", "Telegram", "Bot token"],
];

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

async function sendTest(channel, out) {
  out.className = "";
  out.textContent = "Sending...";
  const data = await api("POST", "/test", { channel });
  if (data.error) {
    out.className = "bad";
    out.textContent = data.error;
    return;
  }
  const answer = data.answer ? ` ${data.answer}` : "";
  out.className = data.ok ? "ok" : "bad";
  out.textContent = data.ok ? `OK ${data.status}${answer}` : `${data.status || "Failed"}${answer}`;
}

function fillChannels(channels) {
  const box = $("#channels");
  box.replaceChildren();
  for (const [key, name, what] of CHANNELS) {
    const c = channels[key] || {};
    let detail = `${what}: ${c.set ? "set" : "not set"}`;
    if (key === "telegram") detail += `  Chat: ${c.chat_id || "not set"}`;
    const out = el("span", { role: "status" });
    const button = el("button", { type: "button", textContent: "Send test message" });
    button.addEventListener("click", () => run(() => sendTest(key, out)));
    box.append(el("div", { className: "channel" },
      el("strong", { textContent: name }),
      el("span", { textContent: c.enabled ? "on" : "off", className: c.enabled ? "ok" : "muted" }),
      el("span", { textContent: detail }),
      el("div", { className: "row" }, button, out)));
  }
}

// unknownNames returns the configured names that are not in known, trimmed
// and without blanks or repeats. Names match exactly, as the plugin does.
function unknownNames(configured, known) {
  const out = [];
  for (const raw of configured) {
    const name = raw.trim();
    if (name && !known.has(name) && !out.includes(name)) out.push(name);
  }
  return out;
}

// settingWarnings lists the form slugs and field keys in the settings that
// match no form or field, which would silently send nothing or less. It says
// nothing when the form list could not be read.
function settingWarnings(s, forms) {
  if (!forms.length) return [];
  const slugs = new Set(forms.map((f) => f.slug));
  const chosen = forms.filter((f) => !s.forms.length || s.forms.some((n) => n.trim() === f.slug));
  const keys = new Set(chosen.flatMap((f) => f.fields || []));
  return [
    ...unknownNames(s.forms, slugs).map((n) => `Unknown form "${n}": no form has this slug, so it sends no alerts.`),
    ...unknownNames(s.fields, keys).map((n) => `Unknown field "${n}": no chosen form has this field, so it is left out of messages.`),
  ];
}

function fillWarnings(s, forms) {
  const box = $("#warnings");
  box.replaceChildren();
  const list = settingWarnings(s, forms);
  for (const w of list) box.append(el("li", { textContent: w }));
  box.hidden = list.length === 0;
}

function fillSettings(s) {
  const dl = $("#settings");
  dl.replaceChildren();
  const add = (k, v) => dl.append(el("dt", { textContent: k }), el("dd", { textContent: v }));
  add("Forms", s.forms.length ? s.forms.join(", ") : "all forms (none chosen)");
  add("Fields", s.fields.length ? s.fields.join(", ") : "all except password, token or card");
  add("Include values", s.include_values ? "on" : "off");
  add("Max length", String(s.max_value_length));
  add("Admin address", s.admin_url || "not set");
  const hint = $("#suggest");
  hint.hidden = true;
  if (s.admin_url) return;
  try {
    const origin = new URL(location.href).origin;
    if (!origin || origin === "null") return;
    hint.textContent = `Admin address is not set. A guess is ${origin}/admin, but check your admin address; it may differ. Paste the right one in Settings to get a link in each message.`;
    hint.hidden = false;
  } catch { /* the address is not readable here */ }
}

function fillPreview(p) {
  const box = $("#preview");
  box.replaceChildren();
  if (!p) {
    box.append(el("p", { className: "muted", textContent: "No submission to preview yet." }));
    return;
  }
  box.append(el("p", { className: "muted", textContent: `Latest submission on ${p.form}` }));
  box.append(el("strong", { textContent: p.title }));
  for (const pair of p.pairs || []) {
    box.append(el("div", {}, el("span", { className: "muted", textContent: `${pair.key}: ` }), pair.value));
  }
  box.append(el("div", { textContent: p.link ? `Open in admin -> ${p.link}` : "Submission id only (no admin address set)" }));
}

function fillLog(rows) {
  const body = $("#log tbody");
  body.replaceChildren();
  for (const r of rows) {
    body.append(el("tr", {},
      el("td", { textContent: new Date(r.created_at).toLocaleString() }),
      el("td", { textContent: r.form_slug }),
      el("td", { textContent: r.channel }),
      el("td", { textContent: r.status, className: `status-${r.status}` }),
      el("td", { textContent: r.code ? String(r.code) : "", className: "num" })));
  }
  $("#empty").hidden = rows.length > 0;
  $("#log").hidden = rows.length === 0;
}

async function load() {
  const data = await api("GET", "/log");
  fillChannels(data.channels || {});
  fillSettings(data.settings);
  fillWarnings(data.settings, data.forms || []);
  fillPreview(data.preview);
  fillLog(data.rows || []);
}

(async () => {
  await hatamoto.ready;
  run(load);
})();
