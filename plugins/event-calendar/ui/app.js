// Repeating events admin page. It runs in a sandboxed iframe and reaches the
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

async function api(method, path) {
  const res = await hatamoto.route(method, path, null);
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
// the admin refuses them; then the owner is told what to enter in the Runs tab.
async function fillRun(inputs) {
  const entered = Object.entries(inputs).filter(([, v]) => v !== "").map(([k, v]) => `${k}=${v}`).join(", ");
  try {
    await hatamoto.runInputs(inputs);
    say("#hint", `The run "${RUN}" is filled in (${entered}). Press Start, then review the change in Changes.`);
  } catch {
    say("#hint", `Open the Runs tab, pick "${RUN}" and enter: ${entered}`);
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

async function load() {
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

hatamoto.ready.then(load);
