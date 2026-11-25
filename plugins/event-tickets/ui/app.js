// Event tickets admin page: Events, Attendees and Settings. It runs in a
// sandboxed iframe and reaches the plugin only through the hatamoto bridge.
// Text is set with textContent or value, never as markup. The frame cannot
// download, so the CSV address is shown in a field that selects on click.
const $ = (sel) => document.querySelector(sel);

const BASE = "/api/v1/p/event-tickets";
const MAX_FIELDS = 10;
const KINDS = [["text", "Text"], ["select", "Choice"], ["checkbox", "Checkbox"]];

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

// run calls fn and shows a failure in the notice line.
async function run(fn) {
  try { return await fn(); } catch (e) { say(e.message, true); return undefined; }
}

// csvPath is the address of an event's attendee list as CSV.
function csvPath(eventID) {
  return `${BASE}/admin/events/${encodeURIComponent(eventID)}/attendees.csv`;
}

function day(iso) {
  return iso ? String(iso).slice(0, 10) : "";
}

function cell(...kids) { return el("td", {}, ...kids); }

function button(text, onClick, props = {}) {
  const b = el("button", { type: "button", textContent: text, ...props });
  b.addEventListener("click", onClick);
  return b;
}

const state = { events: [], cursor: "", search: "", editing: null, attendeeEvent: "", page: 1 };

// ---- Events

async function loadEvents(more) {
  const query = {};
  if (state.search) query.search = state.search;
  if (more && state.cursor) query.cursor = state.cursor;
  const data = await run(() => api("GET", "/admin/events", null, query));
  if (!data) return;
  say("");
  state.events = more ? state.events.concat(data.events || []) : data.events || [];
  state.cursor = data.next_cursor || "";
  renderEvents();
  fillEventPicker();
}

function renderEvents() {
  $("#ev-state").textContent = state.events.length ? "" : "No events yet. Publish an event in the calendar first.";
  $("#ev-table").hidden = !state.events.length;
  $("#ev-more").hidden = !state.cursor;
  $("#ev-rows").replaceChildren(...state.events.map((e) => el("tr", {},
    cell(`${e.name}${e.starts_at ? ` (${day(e.starts_at)})` : ""}`),
    cell(e.types ? `${e.taken}/${e.capacity}` : "No tickets"),
    cell(String(e.waiting)),
    el("td", { className: "actions" },
      button("Tickets", () => openEditor(e.id)),
      button("Attendees", () => openAttendees(e.id))))));
}

$("#ev-go").addEventListener("click", () => { state.search = $("#ev-search").value.trim(); loadEvents(false); });
$("#ev-more").addEventListener("click", () => loadEvents(true));

async function openEditor(id) {
  const data = await run(() => api("GET", `/admin/events/${encodeURIComponent(id)}`));
  if (!data) return;
  say("");
  state.editing = { id, name: data.event.name, types: data.types || [], fields: data.fields || [], defaultCapacity: data.default_capacity };
  renderEditor();
}

function typeRow(t) {
  const name = el("input", { type: "text", value: t.name, maxLength: 80 });
  const cap = el("input", { type: "number", value: String(t.capacity), min: "1", max: "100000" });
  const wait = el("input", { type: "checkbox", checked: t.waitlist });
  const sale = el("input", { type: "checkbox", checked: t.on_sale });
  const save = button("Save", () => run(async () => {
    const body = { name: name.value, capacity: Number(cap.value), waitlist: wait.checked, on_sale: sale.checked, position: t.position };
    if (t.id) body.id = t.id;
    const data = await api("POST", `/admin/events/${encodeURIComponent(state.editing.id)}/types`, body);
    state.editing.types = data.types;
    say("Saved.");
    renderEditor();
    loadEvents(false);
  }));
  const kids = [
    el("label", {}, "Name ", name), el("label", {}, "Tickets ", cap),
    el("label", {}, wait, " Waitlist"), el("label", {}, sale, " On sale"), save,
  ];
  if (t.id) {
    kids.push(el("span", { className: "muted", textContent: `${t.taken} taken` }));
    kids.push(button("Delete", () => run(async () => {
      const data = await api("POST", `/admin/types/${encodeURIComponent(t.id)}/delete`);
      say(data.closed ? "This type has bookings, so it was closed for sale instead of deleted." : "Deleted.");
      await openEditor(state.editing.id);
      loadEvents(false);
    })));
  }
  return el("div", { className: "row" }, ...kids);
}

function fieldRow(f, i) {
  const label = el("input", { type: "text", value: f.label, maxLength: 80 });
  const kind = el("select", {}, ...KINDS.map(([value, text]) => el("option", { value, textContent: text })));
  kind.value = f.kind;
  const options = el("input", { type: "text", value: (f.options || []).join(", "), placeholder: "Options, comma separated" });
  const required = el("input", { type: "checkbox", checked: f.required });
  const sync = () => { f.label = label.value; f.kind = kind.value; f.options = options.value.split(",").map((o) => o.trim()).filter(Boolean); f.required = required.checked; options.hidden = kind.value !== "select"; };
  for (const n of [label, kind, options, required]) { n.addEventListener("change", sync); n.addEventListener("input", sync); }
  options.hidden = f.kind !== "select";
  const row = el("div", { className: "row" }, el("label", {}, "Question ", label), kind, options, el("label", {}, required, " Required"),
    button("Remove", () => { state.editing.fields.splice(i, 1); renderEditor(); }));
  return row;
}

function renderEditor() {
  const ed = state.editing;
  $("#ed").hidden = !ed;
  if (!ed) return;
  $("#ed-title").textContent = `Tickets for ${ed.name}`;
  $("#ed-types").replaceChildren(...ed.types.map(typeRow));
  $("#ed-fields").replaceChildren(...ed.fields.map(fieldRow));
}

$("#ed-add").addEventListener("click", () => {
  const ed = state.editing;
  ed.types.push({ id: 0, name: "", capacity: ed.defaultCapacity || 50, taken: 0, waitlist: true, on_sale: true, position: ed.types.length });
  renderEditor();
});
$("#ed-add-field").addEventListener("click", () => {
  const ed = state.editing;
  if (ed.fields.length >= MAX_FIELDS) { say(`At most ${MAX_FIELDS} questions.`, true); return; }
  ed.fields.push({ key: "", label: "", kind: "text", options: [], required: false });
  renderEditor();
});
$("#ed-save-fields").addEventListener("click", () => run(async () => {
  const ed = state.editing;
  const data = await api("POST", `/admin/events/${encodeURIComponent(ed.id)}/fields`, { fields: ed.fields });
  ed.fields = data.fields;
  say("Saved.");
  renderEditor();
}));
$("#ed-close").addEventListener("click", () => { state.editing = null; renderEditor(); });

// ---- Attendees

function fillEventPicker() {
  const sel = $("#at-event");
  sel.replaceChildren(...state.events.map((e) => el("option", { value: e.id, textContent: e.name })));
  if (state.attendeeEvent) sel.value = state.attendeeEvent;
}

function openAttendees(id) {
  state.attendeeEvent = id;
  $("#at-event").value = id;
  showTab("attendees");
}

async function loadAttendees(page = 1) {
  const id = $("#at-event").value || state.attendeeEvent;
  if (!id) {
    $("#at-state").textContent = "Pick an event on the Events tab first.";
    return;
  }
  state.attendeeEvent = id;
  state.page = page;
  $("#at-csv").value = csvPath(id);
  const data = await run(() => api("GET", `/admin/events/${encodeURIComponent(id)}/attendees`, null, { page }));
  if (!data) return;
  say("");
  const list = data.attendees || [];
  const waiting = data.waitlist || [];
  $("#at-state").textContent = list.length ? "" : "No tickets booked yet.";
  $("#at-table").hidden = !list.length;
  $("#at-rows").replaceChildren(...list.map(attendeeRow));
  $("#wl-state").textContent = waiting.length ? "" : "Nobody is waiting.";
  $("#wl-table").hidden = !waiting.length;
  $("#wl-rows").replaceChildren(...waiting.map((w) => el("tr", {},
    cell(w.name), cell(w.email || ""), cell(w.type_name), cell(w.status), cell(day(w.created_at)))));
  $("#at-more").hidden = !data.has_more;
}

function attendeeRow(a) {
  const active = a.status === "active";
  return el("tr", {},
    cell(a.name), cell(a.email || ""), cell(a.type_name), cell(a.status), cell(day(a.created_at)),
    el("td", { className: "actions" },
      button("Resend", () => resendAttendee(a.id), { disabled: !active }),
      cancelButton(a.id, active)));
}

// resendAttendee mails the order's tickets again; the route refuses a resend
// that comes too soon and says so.
async function resendAttendee(id) {
  const data = await run(() => api("POST", `/admin/attendees/${encodeURIComponent(id)}/resend`));
  if (data) say(data.queued ? "The email is queued and will be sent shortly." : "The email was sent.");
}

// The page runs in a sandboxed iframe without allow-modals, where the browser
// refuses modal dialogs, so cancelling is confirmed in the page: the first
// click arms the button, a second click within a few seconds cancels.
const CANCEL_LABEL = "Cancel";
const CONFIRM_LABEL = "Confirm cancel";
const CONFIRM_MS = 4000;

function cancelButton(id, active) {
  const b = button(CANCEL_LABEL, () => cancelAttendee(id, b), { disabled: !active });
  return b;
}

// cancelAttendee frees the seat only on the second click of the button.
async function cancelAttendee(id, btn) {
  if (btn.textContent !== CONFIRM_LABEL) {
    btn.textContent = CONFIRM_LABEL;
    say("Click Confirm cancel to free the seat and offer it to the waitlist. The attendee is not emailed.");
    setTimeout(() => { btn.textContent = CANCEL_LABEL; }, CONFIRM_MS);
    return;
  }
  btn.textContent = CANCEL_LABEL;
  const data = await run(() => api("POST", `/admin/attendees/${encodeURIComponent(id)}/cancel`));
  if (!data) return;
  say(data.promoted ? "Cancelled. The seat was offered to the waitlist." : "Cancelled.");
  await loadAttendees(state.page);
  loadEvents(false);
}

$("#at-event").addEventListener("change", () => loadAttendees(1));
$("#at-more").addEventListener("click", () => loadAttendees(state.page + 1));
$("#at-csv").addEventListener("click", () => {
  const f = $("#at-csv");
  f.select();
  try {
    say(document.execCommand("copy") ? "Copied. If it did not work, press Ctrl+C." : "Selected. Press Ctrl+C to copy.");
  } catch { say("Selected. Press Ctrl+C to copy."); }
});

// ---- Settings

async function loadSettings() {
  const s = await run(() => api("GET", "/admin/settings"));
  if (!s) return;
  const rows = [
    ["Currency", s.currency],
    ["Sender name", s.sender_name || "(the site name)"],
    ["Confirmation message", s.confirmation_text_set ? "Set" : "Not set"],
    ["Default number of tickets", String(s.default_capacity)],
    ["Keep attendee details for", `${s.retention_months} months`],
    ["Site address", s.site_url || "(not set)"],
    ["Event content type", s.schema_slug || "(calendar events)"],
  ];
  $("#st-rows").replaceChildren(...rows.map(([k, v]) => el("tr", {}, el("th", { textContent: k }), cell(v))));
}

// ---- Tabs

const TABS = { events: () => loadEvents(false), attendees: () => loadAttendees(1), settings: loadSettings };
const loaded = new Set();

function showTab(name) {
  for (const tab of Object.keys(TABS)) {
    $(`#panel-${tab}`).hidden = tab !== name;
    $(`#tab-${tab}`).classList.toggle("active", tab === name);
  }
  say("");
  if (name === "attendees" || !loaded.has(name)) {
    loaded.add(name);
    TABS[name]();
  }
}

for (const tab of Object.keys(TABS)) $(`#tab-${tab}`).addEventListener("click", () => showTab(tab));

hatamoto.ready.then(() => showTab("events"));

if (typeof module !== "undefined") module.exports = { csvPath, resendAttendee, cancelAttendee };
