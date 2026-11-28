// Event calendar admin page: the Events workspace (Upcoming, Past, Drafts with a
// month view), Submissions, Imports and Repeating events. It runs in a sandboxed
// iframe and reaches the plugin only through the hatamoto bridge. Text is set with textContent. The
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

// note sets the text of a status line and hides the line when it is empty.
function note(selector, msg) {
  const n = $(selector);
  n.textContent = msg;
  n.hidden = !msg;
}

// A role the manifest does not allow gets a 403 from core with a body that is
// not JSON; it reads the same as the plugin's own 403.
async function api(method, path, query) {
  const res = await hatamoto.route(method, path, null, query);
  let data = {};
  let json = true;
  try { data = JSON.parse(res.body || "{}"); } catch { json = false; }
  if (res.status === 403 && !json) throw new Error("not allowed");
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

const REP_PER_PAGE = 20;
const rep = { q: "", page: 1 };
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
    if (inputs.after) say("#hint", `This screen cannot fill in the run. On the plugin's Runs tab choose "${RUN}" and type action = ${inputs.action}, name = ${inputs.name}, after = ${inputs.after}. Then press Start.`);
    else say("#hint", `This screen cannot fill in the run. Open the plugin's Runs tab and use the same button there, then press Start.`);
  }
}

const RUN_EDIT = "edit_dates";

// startRun starts the Edit dates run from the click: Hatamoto asks the owner to
// confirm, then starts it. It must be called straight from the click handler,
// with nothing awaited before. An older core throws on the named form; the form
// is filled in instead, as fillRun does.
function startRun(inputs, summary) {
  const values = Object.fromEntries(Object.entries(inputs).filter(([, v]) => v !== ""));
  let started;
  try {
    started = hatamoto.runInputs(RUN_EDIT, values);
  } catch {
    return fillRun(inputs, summary);
  }
  return Promise.resolve(started).then((r) => {
    if (r && r.ok) say("#hint", "Started. Review the change in Changes when the run ends.");
    else if (r && r.error === "cancelled") say("#hint", "");
    else say("#hint", (r && r.error) || "The run could not start.", true);
  }, () => fillRun(inputs, summary));
}

function editDate(ev, d, action, extra = {}) {
  return startRun({ event: ev.slug, action, date: d.day, start_at: "", end_at: "", shift_days: "", ...extra });
}

function button(text, onClick, canAct, className = "btn sm") {
  const b = el("button", { type: "button", className, textContent: text });
  if (canAct) b.addEventListener("click", onClick);
  else {
    b.disabled = true;
    b.title = "Ask the site owner";
  }
  return b;
}

const PER_PAGE = 50;

// showCount writes a tab badge. warn colours it (pending work).
function showCount(name, n, warn) {
  const badge = $(`#cnt-${name}`);
  badge.textContent = n === undefined || n === null ? "" : String(n);
  badge.classList.toggle("q", !(warn && n > 0));
  badge.classList.toggle("warn", Boolean(warn && n > 0));
}

// pager draws "‹ 1 … 4 5 6 … 12 ›" for a page of a list and calls onPage with
// the page a button names.
function pager(node, page, perPage, total, onPage) {
  node.replaceChildren();
  const pages = Math.max(1, Math.ceil(total / perPage));
  if (pages < 2) return;
  const go = (label, target, on) => {
    const b = el("button", { type: "button", textContent: label, className: on ? "on" : "" });
    if (target < 1 || target > pages) b.disabled = true;
    else if (!on) b.addEventListener("click", () => onPage(target));
    return b;
  };
  node.append(go("\u2039", page - 1, false));
  let last = 0;
  for (let p = 1; p <= pages; p++) {
    if (p !== 1 && p !== pages && Math.abs(p - page) > 1) continue;
    if (p - last > 1) node.append(el("span", { className: "muted gap", textContent: "\u2026" }));
    node.append(go(String(p), p, p === page));
    last = p;
  }
  node.append(go("\u203a", page + 1, false));
}

// "1-50 of 232" for the page of a list.
function rangeText(page, perPage, total, capped) {
  if (!total) return "";
  const to = Math.min(total, page * perPage);
  return `${(page - 1) * perPage + 1}-${to} of ${total}${capped ? "+" : ""}`;
}

function plural(n, word) {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
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

// The first six dates of a repeating event: struck through when skipped, amber
// when the time was changed.
function dateChips(ev) {
  const cls = { skipped: "sk", changed: "ch" };
  return el("div", { className: "dates" }, ...ev.dates.slice(0, 6).map((d) => el("span", { className: cls[d.state] || "", textContent: dayText(d.date) })));
}

function eventRows(ev, canAct) {
  const open = state.open.has(ev.id);
  const actions = el("td", { className: "actions" });
  if (canAct) {
    const edit = el("button", { type: "button", className: "btn sm", textContent: "Edit dates" });
    edit.addEventListener("click", () => {
      if (open) state.open.delete(ev.id); else state.open.add(ev.id);
      render();
    });
    const dup = el("button", { type: "button", className: "btn sm ghost", textContent: "Duplicate" });
    dup.addEventListener("click", () => {
      $("#duplicate").hidden = false;
      $("#dup-event").value = ev.slug;
    });
    actions.append(edit, dup);
  } else {
    actions.append(el("span", { className: "muted", textContent: "Owner edits dates" }));
  }
  const rows = [el("tr", {},
    el("td", {}, el("span", { className: "t", textContent: ev.name })),
    el("td", { className: "muted", textContent: ev.rule || "-" }),
    el("td", {}, dateChips(ev)),
    actions)];
  if (open && canAct) {
    const cell = el("td", { colSpan: 4 });
    if (!ev.owned) cell.append(el("p", { className: "muted", textContent: NOT_OWNED }));
    if (!ev.dates.length) cell.append(el("p", { className: "muted", textContent: "No dates to show." }));
    const list = el("ul", { className: "dates-edit" });
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
  $("#dup-event").replaceChildren(...state.events.map((ev) => el("option", { value: ev.slug, textContent: ev.name })));
  $("#dup-go").disabled = !canAct;
  $("#dup-go").title = canAct ? "" : "Ask the site owner";
}

async function loadRepeating() {
  try {
    say("#notice", "");
    const query = { page: String(rep.page) };
    if (rep.q) query.q = rep.q;
    const data = await api("GET", "/admin/repeating", query);
    state.role = data.role || "";
    state.events = (data.items || data.events || []).map((e) => ({ ...e, dates: e.dates || [] }));
    note("#state", "");
    if (!data.ready) note("#state", "The event list is still being built. Check again in a few minutes.");
    else if (!state.events.length) note("#state", rep.q ? "No repeating events match this search." : "No repeating events yet. Set Repeats on an event to see it here.");
    $("#duplicate").hidden = true;
    render();
    const page = data.page || 1;
    const perPage = data.per_page || REP_PER_PAGE;
    $("#rep-range").textContent = rangeText(page, perPage, data.total || 0);
    pager($("#rep-pager"), page, perPage, data.total || 0, (p) => { rep.page = p; loadRepeating(); });
  } catch (e) {
    say("#notice", e.message, true);
  }
}

function searchRepeating() {
  const q = $("#rep-q").value.trim();
  if (q === rep.q) return;
  rep.q = q;
  rep.page = 1;
  loadRepeating();
}
$("#rep-q").addEventListener("keydown", (e) => { if (e.key === "Enter") searchRepeating(); });
$("#rep-q").addEventListener("change", searchRepeating);

$("#dup-go").addEventListener("click", () => {
  const n = Math.trunc(Number($("#dup-shift").value));
  fillRun({ event: $("#dup-event").value, action: "duplicate", date: "", start_at: "", end_at: "", shift_days: Number.isFinite(n) ? String(n) : "0" });
});

// ---- Submissions

const STATUS_BUTTONS = [["pending", "Pending"], ["approved", "Approved"], ["rejected", "Rejected"], ["all", "All"]];
const sub = { role: "", status: "pending", q: "", page: 1, counts: {} };
const STATUS_PILL = { pending: ["pend", "Pending"], approved: ["pub", "Approved"], published: ["pub", "Approved"], rejected: ["rej", "Rejected"] };

// "Fri 20 Feb 8:00 pm" from "2027-02-20T20:00:00+01:00": the clock the
// submitter typed, so no zone conversion.
function submittedWhen(text) {
  const [date, rest] = String(text || "").split("T");
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date)) return text || "";
  return rest ? `${dayText(date)} ${clockText(rest.slice(0, 5))}` : dayText(date);
}

// The submissions the Approved filter lists include the older published ones.
function statusCount(counts, status) {
  if (status === "approved") return (counts.approved || 0) + (counts.published || 0);
  return counts[status] || 0;
}

function renderStatusButtons() {
  for (const [status, label] of STATUS_BUTTONS) {
    const b = $(`#sub-${status}`);
    const n = statusCount(sub.counts, status);
    const warn = status === "pending" && n > 0;
    b.replaceChildren(label, el("span", { className: warn ? "count" : "count q", textContent: String(n) }));
    b.classList.toggle("on", status === sub.status);
  }
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
    cell.append(button("Reject", () => submissionAction(item, "reject"), true, "btn sm danger"));
  }
  if (owner) cell.append(button("Delete", () => submissionAction(item, "delete"), true, "btn sm ghost"));
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

function submissionRow(item) {
  const [cls, label] = STATUS_PILL[item.status] || ["draft", item.status];
  const sent = /^\d{4}-\d{2}-\d{2}/.test(item.created_at || "") ? el("div", { className: "muted", textContent: `Sent ${dayText(item.created_at.slice(0, 10))}` }) : "";
  return el("tr", {},
    el("td", {}, el("span", { className: "t", textContent: item.title }), sent),
    el("td", { textContent: submittedWhen(item.start_at) }),
    el("td", { textContent: item.venue || "-" }),
    el("td", { className: "muted", textContent: item.email }),
    el("td", {}, el("span", { className: `st ${cls}`, textContent: label })),
    submissionActions(item));
}

// Reject all pending asks for a second click: the rows are not listed again.
// It is the owner's, and only on the Pending filter.
let rejectAllAsked = false;
function syncRejectAll() {
  const b = $("#sub-reject-all");
  rejectAllAsked = false;
  b.textContent = "Reject all pending";
  b.disabled = sub.role !== "owner" || sub.status !== "pending";
  b.title = sub.role === "owner" ? "" : "Ask the site owner";
}

function renderSubmissions(data) {
  sub.role = data.role || "";
  sub.counts = data.counts || {};
  $("#sub-off").hidden = data.enabled !== false;
  renderStatusButtons();
  syncRejectAll();
  const rows = $("#sub-rows");
  rows.replaceChildren();
  const items = data.items || [];
  for (const item of items) rows.append(submissionRow(item));
  $("#sub-table").hidden = !items.length;
  note("#sub-state", items.length ? "" : "No submissions here.");
  showCount("submissions", sub.counts.pending || 0, true);
  const page = data.page || 1;
  const perPage = data.per_page || PER_PAGE;
  $("#sub-range").textContent = rangeText(page, perPage, data.total || 0);
  pager($("#sub-pager"), page, perPage, data.total || 0, (p) => { sub.page = p; loadSubmissions(); });
}

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
    sub.page = 1;
    await loadSubmissions();
    say("#hint", `${r.rejected || 0} pending submissions rejected.`);
  } catch (e) {
    say("#notice", e.message, true);
  }
});

async function loadSubmissions() {
  try {
    say("#notice", "");
    const query = { status: sub.status, page: String(sub.page) };
    if (sub.q) query.q = sub.q;
    renderSubmissions(await api("GET", "/admin/submissions", query));
  } catch (e) {
    say("#notice", e.message, true);
  }
}

for (const [status] of STATUS_BUTTONS) {
  $(`#sub-${status}`).addEventListener("click", () => {
    sub.status = status;
    sub.page = 1;
    loadSubmissions();
  });
}

function searchSubmissions() {
  const q = $("#sub-q").value.trim();
  if (q === sub.q) return;
  sub.q = q;
  sub.page = 1;
  loadSubmissions();
}
$("#sub-q").addEventListener("keydown", (e) => { if (e.key === "Enter") searchSubmissions(); });
$("#sub-q").addEventListener("change", searchSubmissions);

// ---- Imports

const KINDS = { google: "Google", outlook: "Outlook", meetup: "Meetup", ical: "iCal", eventbrite: "Eventbrite" };
const imp = { role: "", sources: [], batch: 200, seq: 0 };
const COUNT_AGE_MS = 3600 * 1000;

// "3 hours ago" from a timestamp; "never" when there is none.
function agoText(stamp) {
  const t = Date.parse(stamp);
  if (!stamp || Number.isNaN(t)) return "never";
  const hours = Math.floor((Date.now() - t) / 3600000);
  if (hours < 1) return "less than 1 hour ago";
  if (hours < 48) return `${hours} hour${hours === 1 ? "" : "s"} ago`;
  return `${Math.floor(hours / 24)} days ago`;
}

// "Daily", "Daily (Run now only)" for an address a scheduled run cannot read, or "Off".
function scheduleText(src) {
  if (src.schedule !== "daily") return "Off";
  return src.scheduled_ok ? "Daily" : "Daily (Run now only)";
}

// "200 of 214 imported" once the feed was counted, "200 imported" before. A
// capped count (a first page only) is a floor: it never reads "200 of 50+".
function progressText(src) {
  if (src.found === null || src.found === undefined) return `${src.imported} imported`;
  if (src.found_capped && src.imported >= src.found) return `${src.imported} imported, more may remain`;
  return `${src.imported} of ${src.found}${src.found_capped ? "+" : ""} imported`;
}

// More is waiting: the count says so, or a full batch is on the site and the
// count is not known yet.
function hasMore(src) {
  if (!src.cursor) return false;
  if (src.found_capped) return true;
  if (src.remaining !== null && src.remaining !== undefined) return src.remaining > 0;
  return src.imported >= imp.batch;
}

function lastImportCell(src) {
  if (!src.last_new && !src.imported) return el("td", { textContent: "Never" });
  const when = src.last_new ? `${agoText(src.last_new)} \u00b7 ` : "";
  const progress = src.imported || src.found ? progressText(src) : "";
  const cell = el("td", {}, when);
  cell.append(hasMore(src) ? el("span", { className: "st warn", textContent: progress }) : progress);
  return cell;
}

const SETTINGS_HINT = "Change or add sources on the plugin's Settings tab.";

// Run now and the next batch fill the run form with the source's name: a
// page-started run reaches only the first allowed host, so the run reads the
// address from the settings. The next batch adds where the last one stopped.
function nextBatch(src) {
  return fillRun({ action: "import_source", name: src.name, after: src.cursor });
}

function importRow(src, owner) {
  const run = () => fillRun({ action: "import_source", name: src.name });
  const edit = el("button", { type: "button", className: "btn sm ghost", textContent: "Edit" });
  edit.addEventListener("click", () => {
    const parts = [`Name: ${src.name}`, `Kind: ${KINDS[src.kind] || src.kind}`, `Host: ${src.host || "-"}`, `Category: ${src.category || "-"}`];
    say("#imp-detail", `${parts.join(", ")}. ${SETTINGS_HINT}`);
  });
  const actions = el("td", { className: "actions" }, button("Run now", run, owner && Boolean(src.name)));
  if (hasMore(src)) actions.append(button(`Import the next ${imp.batch}`, () => nextBatch(src), owner));
  actions.append(edit);
  return el("tr", {},
    el("td", {}, el("span", { className: "t", textContent: src.name })),
    el("td", { className: "muted", textContent: KINDS[src.kind] || src.kind }),
    el("td", { className: "muted", textContent: scheduleText(src) }),
    lastImportCell(src),
    actions);
}

// The callout names the first source that has more to import.
function renderNext() {
  const src = imp.sources.find(hasMore);
  $("#imp-next").hidden = !src;
  if (!src) return;
  const unknown = src.found === null || src.found === undefined || src.found_capped && src.imported >= src.found;
  const total = unknown ? "more events" : `${src.found}${src.found_capped ? "+" : ""} events`;
  const b = el("button", { type: "button", className: "link", textContent: `Import the next ${imp.batch}` });
  b.addEventListener("click", () => nextBatch(src));
  $("#imp-next").replaceChildren(`${src.name} has ${total} and the last runs brought in ${src.imported}. `, b);
}

function renderRows() {
  const rows = $("#imp-rows");
  rows.replaceChildren(...imp.sources.map((src) => importRow(src, imp.role === "owner")));
  $("#imp-table").hidden = !imp.sources.length;
  renderNext();
}

// An editor has no Imports tab: core refuses the route, and so does the plugin.
function showOwnerOnly() {
  $("#imp-owner-only").hidden = false;
  for (const id of ["#imp-table", "#imp-next", "#imp-state"]) $(id).hidden = true;
  for (const id of ["#imp-go", "#imp-url", "#imp-add"]) $(id).disabled = true;
}

function renderImports(data) {
  imp.role = data.role || "";
  if (imp.role !== "owner") return showOwnerOnly();
  imp.sources = data.sources || [];
  imp.batch = data.batch_cap || 200;
  $("#imp-owner-only").hidden = true;
  renderRows();
  note("#imp-state", imp.sources.length ? "" : "No sources yet. Add them in import_sources on the plugin's Settings tab.");
  for (const id of ["#imp-go", "#imp-url", "#imp-add"]) $(id).disabled = false;
  countSources(++imp.seq);
}

// A source whose feed was not counted in the last hour is counted now, one at a
// time. A count that fails leaves the row as it was; the reason is the row's
// tooltip.
function needsCount(src) {
  if (!src.enabled || !src.imported) return false;
  const t = Date.parse(src.counted_at);
  return Number.isNaN(t) || Date.now() - t > COUNT_AGE_MS;
}

async function countSources(seq) {
  for (const name of imp.sources.filter(needsCount).map((s) => s.name)) {
    try {
      const data = await api("GET", "/admin/imports", { count: name });
      if (seq !== imp.seq || !data.source) return;
      const i = imp.sources.findIndex((s) => s.name === name);
      if (i < 0) continue;
      imp.sources[i] = data.source;
      renderRows();
      if (data.count_error) $("#imp-rows").children[i].title = data.count_error;
    } catch { /* the row keeps its last numbers */ }
  }
}

$("#imp-add").addEventListener("click", () => say("#imp-detail", SETTINGS_HINT));

async function loadImports() {
  try {
    say("#notice", "");
    renderImports(await api("GET", "/admin/imports"));
  } catch (e) {
    if (e.message === "not allowed") showOwnerOnly();
    else say("#notice", e.message, true);
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

// ---- Events

const WEEK = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const MONTH_NAMES = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
const EVENT_TABS = ["upcoming", "past", "drafts"];
const HINT_EDIT = (name) => `Open Content, then the Calendar event list, in the admin menu and open ${name} there.`;
const HINT_NEW = "Open Content, then the Calendar event list, in the admin menu and press New.";

const ev = {
  tab: "upcoming", view: "list", q: "", venue: "", category: "", novenue: false, day: "", month: "",
  page: 1, role: "", counts: {}, facetsLoaded: false, seq: 0,
};

// "2026-02-13" for a local date.
function ymd(d) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function currentMonth() {
  return ymd(new Date()).slice(0, 7);
}

function renderCounts(counts) {
  Object.assign(ev.counts, counts);
  const c = ev.counts;
  showCount("upcoming", c.upcoming);
  showCount("past", c.past);
  if (c.drafts !== undefined) showCount("drafts", c.drafts_capped ? `${c.drafts}+` : c.drafts);
  showCount("submissions", c.submissions_pending, true);
  showCount("repeating", c.repeating);
}

// The imports tab is the owner's; core refuses an editor anyway.
function applyRole(role) {
  if (!role) return;
  ev.role = role;
  const owner = role === "owner";
  $("#tab-imports").disabled = !owner;
  $("#tab-imports").title = owner ? "" : "Owner only";
}

function setOptions(select, all, values) {
  const keep = select.value;
  select.replaceChildren(el("option", { value: "", textContent: all }), ...values.map((v) => el("option", { value: v, textContent: v })));
  select.value = keep;
}

function renderFacets(f) {
  setOptions($("#venue"), "All venues", f.venues || []);
  setOptions($("#cat"), "All categories", f.categories || []);
  $("#venue").value = ev.venue;
  $("#cat").value = ev.category;
  ev.facetsLoaded = true;
}

function chipList() {
  const chips = [];
  if (ev.q) chips.push([`Search: ${ev.q}`, () => { ev.q = ""; $("#q").value = ""; }]);
  if (ev.venue) chips.push([`Venue: ${ev.venue}`, () => { ev.venue = ""; $("#venue").value = ""; }]);
  if (ev.category) chips.push([`Category: ${ev.category}`, () => { ev.category = ""; $("#cat").value = ""; }]);
  if (ev.novenue) chips.push(["No venue set", () => { ev.novenue = false; $("#novenue").checked = false; }]);
  if (ev.day) {
    const [, m, d] = ev.day.split("-").map(Number);
    chips.push([`Day: ${d} ${MONTHS[m - 1]}`, () => { ev.day = ""; }]);
  }
  return chips;
}

function renderChips() {
  const chips = chipList();
  $("#chips").hidden = !chips.length;
  $("#chips").replaceChildren(...chips.map(([label, clear]) => {
    const b = el("button", { type: "button", className: "chip", textContent: `${label} \u00d7` });
    b.title = "Remove";
    b.addEventListener("click", () => { clear(); ev.page = 1; loadEvents(); });
    return b;
  }));
}

function eventRow(item) {
  const title = el("td", {}, el("span", { className: "t", textContent: item.name }));
  if (item.repeating) title.append(el("span", { className: "tag", textContent: "Repeats" }));
  if (item.imported) title.append(el("span", { className: "tag", textContent: "Imported" }));
  const venue = el("td", {});
  if (item.no_venue) venue.append(el("span", { className: "st warn", textContent: "No venue" }));
  else venue.append(item.venue);
  if (item.city) venue.append(el("div", { className: "muted", textContent: item.city }));
  const category = [(item.categories || []).join(", "), item.price].filter(Boolean).join(" \u00b7 ");
  const draft = item.status === "draft";
  const edit = el("button", { type: "button", className: "btn sm ghost", textContent: "Edit" });
  edit.addEventListener("click", () => say("#hint", HINT_EDIT(item.name)));
  return el("tr", {},
    el("td", { className: "time", textContent: item.all_day ? "All day" : item.time || "" }),
    title, venue,
    el("td", { className: "muted", textContent: category }),
    el("td", {}, el("span", { className: draft ? "st draft" : "st pub", textContent: draft ? "Draft" : "Published" })),
    el("td", { className: "actions" }, edit));
}

function dayHeader(day, counts, inPage) {
  const today = ymd(new Date());
  const label = day ? `${dayText(day)}${day === today ? " \u00b7 Today" : ""}` : "No date";
  const n = counts[day] || inPage;
  return el("tr", { className: "day" }, el("td", { colSpan: 6 }, label, el("span", { textContent: plural(n, "event") })));
}

function renderList(data) {
  const items = data.items || [];
  const rows = $("#ev-rows");
  rows.replaceChildren();
  let current;
  for (const item of items) {
    if (item.day !== current) {
      current = item.day;
      rows.append(dayHeader(current, data.day_counts || {}, items.filter((i) => i.day === current).length));
    }
    rows.append(eventRow(item));
  }
  $("#list").hidden = !items.length;
  if (!data.ready) note("#ev-state", "The event list is being prepared; it is ready after the next hourly check.");
  else note("#ev-state", items.length ? "" : "No events match these filters.");
  const total = data.total || 0;
  $("#ev-range").textContent = rangeText(data.page || 1, data.per_page || PER_PAGE, total, data.total_capped);
  pager($("#ev-pager"), data.page || 1, data.per_page || PER_PAGE, total, (p) => { ev.page = p; loadEvents(); });
}

// ---- Month grid

function monthCell(key, inMonth, items, count, today) {
  const cell = el("div", { className: `c${inMonth ? "" : " out"}${key === today ? " today" : ""}` },
    el("span", { className: "n", textContent: String(Number(key.slice(8))) }));
  const shown = items.slice(0, 3);
  for (const item of shown) {
    const b = el("button", { type: "button", className: "e", textContent: item.all_day || !item.time ? item.name : `${item.time} ${item.name}` });
    b.title = item.name;
    b.addEventListener("click", () => say("#hint", HINT_EDIT(item.name)));
    cell.append(b);
  }
  if (count > shown.length) {
    const more = el("button", { type: "button", className: "more", textContent: `+${count - shown.length} more` });
    more.addEventListener("click", () => showDay(key));
    cell.append(more);
  }
  return cell;
}

// showDay lists one day of the month. A day before today is among the past
// events, so the list switches to Past for it.
function showDay(day) {
  ev.view = "list";
  ev.day = day;
  ev.page = 1;
  ev.tab = day < ymd(new Date()) ? "past" : "upcoming";
  syncTabs();
  loadEvents();
}

function renderMonth(data) {
  const month = data.month || ev.month;
  ev.month = month;
  const [y, m] = month.split("-").map(Number);
  $("#m-label").textContent = `${MONTH_NAMES[m - 1]} ${y}`;
  const total = data.total || 0;
  $("#m-total").textContent = `${plural(total, "event")} this month${data.truncated ? ` (first ${data.per_page || 1500} shown)` : ""}`;
  const byDay = {};
  for (const item of data.items || []) (byDay[item.day] = byDay[item.day] || []).push(item);
  const first = Date.UTC(y, m - 1, 1);
  const lead = (new Date(first).getUTCDay() + 6) % 7;
  const today = ymd(new Date());
  const cells = WEEK.map((d) => el("div", { className: "h", textContent: d }));
  for (let i = 0; i < 42; i++) {
    const d = new Date(first + (i - lead) * 86400000);
    const key = d.toISOString().slice(0, 10);
    cells.push(monthCell(key, d.getUTCMonth() === m - 1, byDay[key] || [], (data.day_counts || {})[key] || 0, today));
  }
  $("#m-grid").replaceChildren(...cells);
}

function shiftMonth(by) {
  const [y, m] = ev.month.split("-").map(Number);
  const d = new Date(Date.UTC(y, m - 1 + by, 1));
  ev.month = d.toISOString().slice(0, 7);
  loadEvents();
}

// ---- Loading

function eventsQuery() {
  const q = { tab: ev.tab };
  if (ev.view === "month") q.month = ev.month || currentMonth();
  else q.page = String(ev.page);
  if (ev.q) q.q = ev.q;
  if (ev.venue) q.venue = ev.venue;
  if (ev.category) q.category = ev.category;
  if (ev.novenue) q.novenue = "1";
  if (ev.day && ev.view === "list") q.day = ev.day;
  if (!ev.facetsLoaded) q.facets = "1";
  if (ev.counts.drafts === undefined) q.with_drafts = "1";
  return q;
}

// syncTabs shows the active tab and what the Drafts tab cannot do: it is a list
// only, and venue and category do not filter it.
function syncTabs() {
  for (const name of TABS) $(`#tab-${name}`).classList.toggle("on", name === ev.tab);
  const drafts = ev.tab === "drafts";
  if (drafts) ev.view = "list";
  $("#venue").disabled = drafts;
  $("#cat").disabled = drafts;
  $("#view-month").disabled = drafts;
  $("#view-list").classList.toggle("on", ev.view === "list");
  $("#view-month").classList.toggle("on", ev.view === "month");
  $("#list").hidden = ev.view === "month";
  $("#ev-foot").hidden = ev.view === "month";
  $("#month").hidden = ev.view !== "month";
  if (ev.view === "month") note("#ev-state", "");
}

async function loadEvents() {
  const seq = ++ev.seq;
  syncTabs();
  renderChips();
  try {
    say("#notice", "");
    const data = await api("GET", "/admin/events", eventsQuery());
    if (seq !== ev.seq) return;
    applyRole(data.role);
    renderCounts(data.counts || {});
    if (data.facets) renderFacets(data.facets);
    if (ev.view === "month") renderMonth(data);
    else renderList(data);
  } catch (e) {
    if (seq === ev.seq) say("#notice", e.message, true);
  }
}

// ---- Tabs

const TABS = ["upcoming", "past", "drafts", "submissions", "imports", "repeating"];
const PANEL = { upcoming: "events", past: "events", drafts: "events", submissions: "submissions", imports: "imports", repeating: "repeating" };
const LOADERS = { submissions: loadSubmissions, imports: loadImports, repeating: loadRepeating };
const HELP = {
  upcoming: "Published events from today on, grouped by day, 50 per page.",
  past: "Published events that have ended, newest first.",
  drafts: "Imported, approved and duplicated events stay here until someone publishes them. The first 500 are listed.",
  submissions: "Owners and editors can reject a submission. Approving and deleting are for the site owner.",
  imports: "Owner only. When a source has more events than one run brings in, the page says so and offers the next batch.",
  repeating: "20 per page. Only the site owner changes dates. Skip, Change time and Restore start the Edit dates run after you confirm; review the change in Changes. On an older Hatamoto core they fill in the run form instead.",
};
const loaded = new Set();

function showTab(name) {
  const panel = PANEL[name];
  for (const p of new Set(Object.values(PANEL))) $(`#panel-${p}`).hidden = p !== panel;
  for (const tab of TABS) $(`#tab-${tab}`).classList.toggle("on", tab === name);
  $("#help").textContent = HELP[name];
  say("#hint", "");
  if (panel === "events") {
    ev.tab = name;
    ev.page = 1;
    ev.day = "";
    loadEvents();
  } else if (!loaded.has(name)) {
    loaded.add(name);
    LOADERS[name]();
  }
}

for (const tab of TABS) $(`#tab-${tab}`).addEventListener("click", () => showTab(tab));

function filterChanged() {
  ev.page = 1;
  loadEvents();
}

// Search runs on Enter and when the field loses focus with a new value.
function applySearch() {
  const q = $("#q").value.trim();
  if (q === ev.q) return;
  ev.q = q;
  filterChanged();
}
$("#q").addEventListener("keydown", (e) => { if (e.key === "Enter") applySearch(); });
$("#q").addEventListener("change", applySearch);
$("#venue").addEventListener("change", () => { ev.venue = $("#venue").value; filterChanged(); });
$("#cat").addEventListener("change", () => { ev.category = $("#cat").value; filterChanged(); });
$("#novenue").addEventListener("change", () => { ev.novenue = $("#novenue").checked; filterChanged(); });

$("#view-list").addEventListener("click", () => {
  if (ev.view === "list") return;
  ev.view = "list";
  filterChanged();
});
$("#view-month").addEventListener("click", () => {
  if (ev.view === "month") return;
  ev.view = "month";
  ev.day = "";
  ev.month = ev.month || currentMonth();
  loadEvents();
});
$("#m-prev").addEventListener("click", () => shiftMonth(-1));
$("#m-next").addEventListener("click", () => shiftMonth(1));
$("#m-today").addEventListener("click", () => { ev.month = currentMonth(); loadEvents(); });

$("#ical").addEventListener("click", () => { $("#feed-row").hidden = !$("#feed-row").hidden; });
$("#new-event").addEventListener("click", () => say("#hint", HINT_NEW));

hatamoto.ready.then(() => showTab("upcoming"));
