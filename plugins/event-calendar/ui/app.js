// Event calendar admin page: Submissions, Imports and Repeating events. It runs in a sandboxed iframe and reaches the
// plugin only through the hatamoto bridge. Text is set with textContent. The
// buttons fill in the inputs of a run through the bridge; the owner then
// starts the run, and the change is reviewed in Changes before it is live.
const $ = (sel) => document.querySelector(sel);

const NOT_OWNED = "Hand this event back in the editor (Who owns this page) to change single dates here, or edit the dates in the event itself.";
const RUN = "Edit repeat dates or duplicate";
const DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

function el(tag, props = {}, ...kids) {
  const node = Object.assign(document.createElement(tag), props);
  for (const kid of kids) node.append(kid);
  return node;
}

function say(selector, msg, isError) {
  const n = $(selector);
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

// "Tue 17 Feb" from "2026-02-17".
function dayText(date) {
  const [y, m, d] = date.split("-").map(Number);
  const wd = new Date(Date.UTC(y, m - 1, d)).getUTCDay();
  return `${DAYS[wd]} ${String(d).padStart(2, "0")} ${MONTHS[m - 1]}`;
}

// "6:00 pm" from "18:00".
function clockText(hhmm) {
  const [h, m] = hhmm.split(":").map(Number);
  return `${h % 12 || 12}:${String(m).padStart(2, "0")} ${h < 12 ? "am" : "pm"}`;
}

function whenText(d) {
  return d.start ? `${dayText(d.date)}  ${clockText(d.start)}` : dayText(d.date);
}

const state = { role: "", events: [], open: new Set(), editing: "" };

// fillRun puts the inputs into the run form. Where the page has no run form
// the admin refuses them (the Settings screen); then the owner is told to use
// the same button on the Runs tab, whose form cannot be typed into.
// summary names what was filled in when the values themselves are too long to
// list.
async function fillRun(inputs, summary) {
  const entered = Object.entries(inputs).filter(([, v]) => v !== "").map(([k, v]) => `${k}=${v}`).join(", ");
  try {
    await hatamoto.runInputs(inputs);
    say("#hint", `The run "${RUN}" is filled in (${summary || entered}). Press Start, then review the change in Changes.`);
  } catch {
    say("#hint", `This screen cannot fill in the run. Open the plugin's Runs tab and use the same button there, then press Start.`);
  }
}

function editDate(ev, d, action, extra = {}) {
  return fillRun({ event: ev.slug, action, date: d.day, start_at: "", end_at: "", shift_days: "", ...extra });
}

function button(text, onClick, canAct) {
  const b = el("button", { type: "button", textContent: text });
  if (canAct) b.addEventListener("click", onClick);
  else {
    b.disabled = true;
    b.title = "Ask the site owner";
  }
  return b;
}

function dateRow(ev, d, canAct) {
  const key = `${ev.id}|${d.day}`;
  const li = el("li", {}, el("span", { className: "when", textContent: whenText(d) }), el("span", { className: `state ${d.state}`, textContent: d.state }));
  if (!ev.owned) return [li];
  if (d.state !== "skipped") {
    if (d.state === "normal") li.append(button("Skip", () => editDate(ev, d, "skip"), canAct));
    li.append(button("Change time", () => { state.editing = state.editing === key ? "" : key; render(); }, canAct));
  }
  if (d.state !== "normal") li.append(button("Restore", () => editDate(ev, d, "restore"), canAct));
  const rows = [li];
  if (state.editing === key && canAct) {
    const start = el("input", { type: "time", value: d.start || "" });
    const end = el("input", { type: "time", value: d.end || "" });
    const use = el("button", { type: "button", textContent: "Use these times" });
    use.addEventListener("click", () => editDate(ev, d, "change", { start_at: start.value, end_at: end.value }));
    rows.push(el("li", { className: "editor" }, "From", start, "to", end, use));
  }
  return rows;
}

function eventRows(ev, canAct) {
  const open = state.open.has(ev.id);
  const next = ev.dates.find((d) => d.state !== "skipped");
  const toggle = el("button", { type: "button", className: "link", textContent: `${open ? "^" : "v"} dates` });
  toggle.addEventListener("click", () => {
    if (open) state.open.delete(ev.id); else state.open.add(ev.id);
    render();
  });
  const first = el("tr", {},
    el("td", {}, el("div", { textContent: ev.name }), toggle),
    el("td", { textContent: ev.rule || "-" }),
    el("td", { textContent: next ? whenText(next) : "-" }));
  const rows = [first];
  if (open) {
    const cell = el("td", { colSpan: 3 });
    if (!ev.owned) cell.append(el("p", { className: "muted", textContent: NOT_OWNED }));
    if (!ev.dates.length) cell.append(el("p", { className: "muted", textContent: "No dates to show." }));
    const list = el("ul", { className: "dates" });
    for (const d of ev.dates) list.append(...dateRow(ev, d, canAct));
    cell.append(list);
    rows.push(el("tr", {}, cell));
  }
  return rows;
}

function render() {
  const canAct = state.role === "owner";
  const tbody = $("#rows");
  tbody.replaceChildren();
  for (const ev of state.events) tbody.append(...eventRows(ev, canAct));
  $("#events").hidden = !state.events.length;
  $("#duplicate").hidden = !state.events.length;
  const pick = $("#dup-event");
  pick.replaceChildren(...state.events.map((ev) => el("option", { value: ev.slug, textContent: ev.name })));
  $("#dup-go").disabled = !canAct;
  $("#dup-go").title = canAct ? "" : "Ask the site owner";
  if (!canAct && state.events.length) $("#state").textContent = "Ask the site owner to skip, change or restore dates.";
}

async function loadRepeating() {
  try {
    say("#notice", "");
    const data = await api("GET", "/admin/repeating");
    state.role = data.role || "";
    state.events = (data.events || []).map((e) => ({ ...e, dates: e.dates || [] }));
    $("#state").textContent = "";
    if (!data.ready) $("#state").textContent = "The event list is still being built. Check again in a few minutes.";
    else if (!state.events.length) $("#state").textContent = "No repeating events yet. Set Repeats on an event to see it here.";
    render();
  } catch (e) {
    say("#notice", e.message, true);
  }
}

$("#dup-go").addEventListener("click", () => {
  const n = Math.trunc(Number($("#dup-shift").value));
  fillRun({ event: $("#dup-event").value, action: "duplicate", date: "", start_at: "", end_at: "", shift_days: Number.isFinite(n) ? String(n) : "0" });
});

// ---- Submissions

const STATUSES = [["pending", "Pending"], ["approved", "Approved"], ["published", "Published"], ["rejected", "Rejected"], ["all", "All"]];
const sub = { role: "", status: "pending" };

// "Fri 20 Feb 8:00 pm" from "2027-02-20T20:00:00+01:00": the clock the
// submitter typed, so no zone conversion.
function submittedWhen(text) {
  const [date, rest] = String(text || "").split("T");
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return text || "";
  return rest ? `${dayText(date)} ${clockText(rest.slice(0, 5))}` : dayText(date);
}

function submissionActions(item) {
  const owner = sub.role === "owner";
  const cell = el("td", { className: "actions" });
  if (item.status === "pending") {
    cell.append(button("Approve", () => fillRun({
      action: "approve", title: item.title, start_at: item.start_at, end_at: item.end_at,
      venue_name: item.venue, description: item.description, submission: String(item.id),
    }, `submission ${item.id}`), owner));
  }
  if (item.status === "pending" || item.status === "approved") {
    cell.append(button("Reject", () => submissionAction(item, "reject"), true));
  }
  cell.append(button("Delete", () => submissionAction(item, "delete"), owner));
  return cell;
}

async function submissionAction(item, what) {
  try {
    say("#notice", "");
    await api("POST", `/admin/submissions/${item.id}/${what}`);
    await loadSubmissions();
  } catch (e) {
    say("#notice", e.message, true);
  }
}

function renderSubmissions(data) {
  sub.role = data.role || "";
  $("#sub-off").hidden = data.enabled !== false;
  $("#sub-reject-all").disabled = sub.role !== "owner";
  const rows = $("#sub-rows");
  rows.replaceChildren();
  for (const item of data.items || []) {
    rows.append(el("tr", {},
      el("td", { textContent: item.title }),
      el("td", { textContent: submittedWhen(item.start_at) }),
      el("td", { textContent: item.venue || "-" }),
      el("td", { textContent: item.email }),
      submissionActions(item)));
  }
  const n = (data.items || []).length;
  $("#sub-table").hidden = !n;
  $("#sub-state").textContent = n ? "" : "No submissions here.";
  const pending = (data.counts && data.counts.pending) || 0;
  $("#tab-submissions").textContent = pending ? `Submissions (${pending})` : "Submissions";
  if (sub.role !== "owner" && n) $("#sub-state").textContent = "Ask the site owner to approve or delete a submission. You can reject one.";
}

// Reject all pending asks for a second click: the rows are not listed again.
let rejectAllAsked = false;
$("#sub-reject-all").addEventListener("click", async () => {
  const b = $("#sub-reject-all");
  if (!rejectAllAsked) {
    rejectAllAsked = true;
    b.textContent = "Click again to reject all pending";
    return;
  }
  rejectAllAsked = false;
  b.textContent = "Reject all pending";
  try {
    say("#notice", "");
    const r = await api("POST", "/admin/submissions/reject-all");
    await loadSubmissions();
    say("#hint", `${r.rejected || 0} pending submissions rejected.`);
  } catch (e) {
    say("#notice", e.message, true);
  }
});

async function loadSubmissions() {
  try {
    say("#notice", "");
    renderSubmissions(await api("GET", "/admin/submissions", { status: sub.status }));
  } catch (e) {
    say("#notice", e.message, true);
  }
}

// ---- Imports

const KINDS = { google: "Google", outlook: "Outlook", meetup: "Meetup", ical: "iCal", eventbrite: "Eventbrite" };
const imp = { role: "", sources: [] };

// "3 hours ago" from a timestamp; "never" when there is none.
function agoText(stamp) {
  const t = Date.parse(stamp);
  if (!stamp || Number.isNaN(t)) return "never";
  const hours = Math.floor((Date.now() - t) / 3600000);
  if (hours < 1) return "less than 1 hour ago";
  if (hours < 48) return `${hours} hour${hours === 1 ? "" : "s"} ago`;
  return `${Math.floor(hours / 24)} days ago`;
}

function importRow(src, owner) {
  const run = () => fillRun({ action: "import_url", url: src.url || "", kind: src.kind, name: src.name });
  const edit = el("button", { type: "button", textContent: "Edit" });
  edit.addEventListener("click", () => {
    const parts = [`Name: ${src.name}`, `Kind: ${KINDS[src.kind] || src.kind}`, `Host: ${src.host || "-"}`, `Category: ${src.category || "-"}`];
    say("#imp-detail", `${parts.join(", ")}. Change sources on the plugin's Settings tab.`);
  });
  return el("tr", {},
    el("td", { textContent: src.name }),
    el("td", { textContent: KINDS[src.kind] || src.kind }),
    el("td", { textContent: src.enabled ? "manual" : "off" }),
    el("td", { textContent: agoText(src.last_imported) }),
    el("td", { className: "actions" }, button("Run now", run, owner && Boolean(src.url)), edit));
}

function renderImports(data) {
  imp.role = data.role || "";
  imp.sources = data.sources || [];
  const rows = $("#imp-rows");
  rows.replaceChildren();
  for (const src of imp.sources) rows.append(importRow(src, imp.role === "owner"));
  $("#imp-table").hidden = !imp.sources.length;
  $("#imp-state").textContent = imp.sources.length ? "" : "No sources yet. Add them in import_sources on the plugin's Settings tab.";
  const owner = imp.role === "owner";
  $("#imp-go").disabled = !owner;
  $("#imp-go").title = owner ? "" : "Ask the site owner";
  $("#imp-url").disabled = !owner;
}

async function loadImports() {
  try {
    say("#notice", "");
    renderImports(await api("GET", "/admin/imports"));
  } catch (e) {
    say("#notice", e.message, true);
  }
}

// The host of an address as typed, "" when it is not a web or webcal address.
function hostOfAddress(text) {
  const m = /^(?:https?|webcals?):\/\/([^/?#@\s]+)/i.exec(String(text || "").trim());
  return m ? m[1].toLowerCase() : "";
}

$("#imp-go").addEventListener("click", () => {
  const url = $("#imp-url").value.trim();
  const host = hostOfAddress(url);
  if (!host) {
    say("#imp-confirm", "Enter the calendar address; it starts with https or webcal.", true);
    return;
  }
  say("#imp-confirm", `Importing from ${host}. Tick the any-address host (*) on the Runs tab before Start.`);
  // Core's run form accepts only http(s) addresses.
  return fillRun({ action: "import_url", url: url.replace(/^webcals?:\/\//i, "https://"), kind: "ical", name: "" });
});

// ---- Tabs

const TABS = { submissions: loadSubmissions, imports: loadImports, repeating: loadRepeating };
const loaded = new Set();

function showTab(name) {
  for (const tab of Object.keys(TABS)) {
    $(`#panel-${tab}`).hidden = tab !== name;
    $(`#tab-${tab}`).classList.toggle("active", tab === name);
  }
  say("#hint", "");
  if (!loaded.has(name)) {
    loaded.add(name);
    TABS[name]();
  }
}

for (const tab of Object.keys(TABS)) $(`#tab-${tab}`).addEventListener("click", () => showTab(tab));

$("#sub-status").replaceChildren(...STATUSES.map(([value, text]) => el("option", { value, textContent: text })));
$("#sub-status").addEventListener("change", () => {
  sub.status = $("#sub-status").value;
  loadSubmissions();
});

hatamoto.ready.then(() => showTab("submissions"));
