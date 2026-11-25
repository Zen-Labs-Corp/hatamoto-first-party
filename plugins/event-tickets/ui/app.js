// Event tickets admin page: Events, Attendees, Orders and Settings. It runs in a
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

const MAX_PRICE_CENTS = 99999999;
const MAX_BUNDLES = 10;
const STATUSES = [["", "Any status"], ["paid", "Paid"], ["pending", "Waiting for payment"], ["confirmed", "Confirmed (free)"],
  ["refunded", "Refunded"], ["cancelled", "Cancelled"], ["expired", "Expired"]];
const PROVIDERS = { stripe: "Stripe", paypal: "PayPal" };

const state = {
  events: [], cursor: "", search: "", editing: null, attendeeEvent: "", page: 1,
  settings: {}, orders: [], orderCursor: "", orderFilter: { status: "", event: "", q: "" }, refunding: null,
  coupons: [], coupon: { id: 0, type_id: 0 },
};

// ---- Money. Amounts are whole minor units (cents) everywhere; the decimals of a
// currency come from the server (currency_exponent). No floating point is used.

function exponent() {
  const e = state.settings.currency_exponent;
  return Number.isInteger(e) && e >= 0 && e <= 3 ? e : 2;
}

// centsText writes minor units as a plain decimal: 1250 is "12.50" (exp 2).
function centsText(cents, exp = exponent()) {
  let s = String(cents);
  if (exp === 0) return s;
  s = s.padStart(exp + 1, "0");
  return `${s.slice(0, -exp)}.${s.slice(-exp)}`;
}

// formatMoney is "Free" or "12.50 USD".
function formatMoney(cents, cur, exp = exponent()) {
  return cents > 0 ? `${centsText(cents, exp)} ${cur || ""}`.trim() : "Free";
}

// parseMoney reads what the owner typed as minor units; null when it is not a
// plain amount with at most exp decimals, or above the provider cap.
function parseMoney(text, exp = exponent()) {
  const m = /^(\d{1,9})(?:\.(\d{1,3}))?$/.exec(String(text).trim());
  if (!m || (m[2] || "").length > exp) return null;
  const cents = Number(m[1] + (m[2] || "").padEnd(exp, "0"));
  return cents <= MAX_PRICE_CENTS ? cents : null;
}

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
  state.editing = {
    id, name: data.event.name, types: data.types || [], fields: data.fields || [], defaultCapacity: data.default_capacity,
    bundles: (data.bundles || []).map(bundleRule),
  };
  renderEditor();
}

function typeRow(t) {
  const name = el("input", { type: "text", value: t.name, maxLength: 80 });
  const cap = el("input", { type: "number", value: String(t.capacity), min: "1", max: "100000" });
  const price = el("input", { type: "text", inputMode: "decimal", value: t.price_cents ? centsText(t.price_cents) : "", placeholder: "Free", maxLength: 12, size: 8 });
  const wait = el("input", { type: "checkbox", checked: t.waitlist });
  const sale = el("input", { type: "checkbox", checked: t.on_sale });
  const save = button("Save", () => run(async () => {
    const cents = price.value.trim() === "" ? 0 : parseMoney(price.value);
    if (cents === null) throw new Error("Enter the price as a number, for example 12.50, or leave it empty for free.");
    // A priced ticket has no waitlist: nobody can pay for a seat that frees up later.
    const body = { name: name.value, capacity: Number(cap.value), waitlist: wait.checked && cents === 0, on_sale: sale.checked, position: t.position, price_cents: cents };
    if (t.id) body.id = t.id;
    const data = await api("POST", `/admin/events/${encodeURIComponent(state.editing.id)}/types`, body);
    state.editing.types = data.types;
    say("Saved.");
    renderEditor();
    loadEvents(false);
  }));
  const kids = [
    el("label", {}, "Name ", name), el("label", {}, "Tickets ", cap), el("label", {}, `Price (${state.settings.currency || ""}) `, price),
    el("label", {}, wait, " Waitlist (free tickets only)"), el("label", {}, sale, " On sale"), save,
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

// bundleRule is a rule as the editor holds it: value in the unit of its kind,
// valueText what the field shows.
function bundleRule(b) {
  return {
    type_ids: b.type_ids || [], all_types: Boolean(b.all_types), min_qty: b.min_qty || 2, kind: b.kind || "percent",
    valueText: b.kind === "fixed" ? centsText(b.value) : String(b.value || ""),
  };
}

// bundlesBody is the body of the bundles route, or an error text. A percent is
// a whole number 1 to 100; a fixed discount is an amount in the currency.
function bundlesBody(rules, exp = exponent()) {
  const out = [];
  for (const r of rules) {
    const value = r.kind === "percent" ? (/^\d{1,3}$/.test(r.valueText.trim()) ? Number(r.valueText) : null) : parseMoney(r.valueText, exp);
    if (!value || (r.kind === "percent" && value > 100)) return { error: "Give every bundle rule a discount: a percent from 1 to 100, or an amount." };
    if (r.all_types && !r.type_ids.length) return { error: "Pick the ticket types of a combination." };
    out.push({ type_ids: r.type_ids, all_types: r.all_types, min_qty: r.min_qty, kind: r.kind, value });
  }
  return { body: { rules: out } };
}

function bundleRow(rule, i) {
  const ed = state.editing;
  const boxes = ed.types.filter((t) => t.id).map((t) => {
    const cb = el("input", { type: "checkbox", checked: rule.type_ids.includes(t.id) });
    cb.addEventListener("change", () => {
      rule.type_ids = cb.checked ? rule.type_ids.concat(t.id) : rule.type_ids.filter((x) => x !== t.id);
    });
    return el("label", {}, cb, ` ${t.name}`);
  });
  const qty = el("input", { type: "number", value: String(rule.min_qty), min: "1", max: "10" });
  qty.addEventListener("input", () => { rule.min_qty = Number(qty.value); });
  const kind = el("select", {}, el("option", { value: "percent", textContent: "% off" }), el("option", { value: "fixed", textContent: "amount off" }));
  kind.value = rule.kind;
  kind.addEventListener("change", () => { rule.kind = kind.value; });
  const value = el("input", { type: "text", value: rule.valueText, maxLength: 12, size: 8 });
  value.addEventListener("input", () => { rule.valueText = value.value; });
  const all = el("input", { type: "checkbox", checked: rule.all_types });
  all.addEventListener("change", () => { rule.all_types = all.checked; });
  return el("div", { className: "row" }, el("span", { textContent: "Buy at least" }), qty, el("span", { textContent: "of" }),
    ...(boxes.length ? boxes : [el("span", { className: "muted", textContent: "any ticket (save the ticket types first to pick some)" })]),
    el("label", {}, all, " one of each picked type"), kind, value,
    button("Remove", () => { ed.bundles.splice(i, 1); renderEditor(); }));
}

// isOwner: coupons and bundles are changed by owners only (an editor would get a 403).
function isOwner() { return Boolean(state.settings.can_refund); }

function renderEditor() {
  const ed = state.editing;
  $("#ed").hidden = !ed;
  if (!ed) return;
  $("#ed-title").textContent = `Tickets for ${ed.name}`;
  $("#ed-bundle-box").hidden = !isOwner(); // the server answers 403 to an editor
  $("#ed-types").replaceChildren(...ed.types.map(typeRow));
  $("#ed-bundles").replaceChildren(...ed.bundles.map(bundleRow));
  $("#ed-fields").replaceChildren(...ed.fields.map(fieldRow));
}

$("#ed-add").addEventListener("click", () => {
  const ed = state.editing;
  ed.types.push({ id: 0, name: "", capacity: ed.defaultCapacity || 50, taken: 0, waitlist: true, on_sale: true, position: ed.types.length, price_cents: 0 });
  renderEditor();
});
$("#ed-add-bundle").addEventListener("click", () => {
  const ed = state.editing;
  if (ed.bundles.length >= MAX_BUNDLES) { say(`At most ${MAX_BUNDLES} bundle rules.`, true); return; }
  ed.bundles.push(bundleRule({ min_qty: 2, kind: "percent", value: 10 }));
  renderEditor();
});
$("#ed-save-bundles").addEventListener("click", () => run(async () => {
  const ed = state.editing;
  const req = bundlesBody(ed.bundles);
  if (req.error) throw new Error(req.error);
  const data = await api("POST", `/admin/events/${encodeURIComponent(ed.id)}/bundles`, req.body);
  ed.bundles = (data.bundles || []).map(bundleRule);
  say("Saved.");
  renderEditor();
}));
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
  const events = [el("option", { value: "", textContent: "Any event" })].concat(state.events.map((e) => el("option", { value: e.id, textContent: e.name })));
  const keep = $("#or-event").value;
  $("#or-event").replaceChildren(...events);
  $("#or-event").value = keep;
  $("#cp-event").replaceChildren(...events.map((o) => el("option", { value: o.value, textContent: o.textContent })));
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
// A second click this soon after arming is a double-click, not a confirmation.
const ARM_GUARD_MS = 400;

function cancelButton(id, active) {
  const b = button(CANCEL_LABEL, () => cancelAttendee(id, b), { disabled: !active });
  return b;
}

// cancelAttendee frees the seat only on the second click of the button.
async function cancelAttendee(id, btn) {
  if (btn.textContent !== CONFIRM_LABEL) {
    btn.textContent = CONFIRM_LABEL;
    btn.armedAt = Date.now();
    say("Click Confirm cancel to free the seat and offer it to the waitlist. The attendee is not emailed. This does not refund a payment: use Orders, Refund.");
    setTimeout(() => { btn.textContent = CANCEL_LABEL; }, CONFIRM_MS);
    return;
  }
  if (Date.now() - btn.armedAt < ARM_GUARD_MS) return;
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

// ---- Orders

// confirmButton arms on the first click and acts on the second, like the
// attendee cancel: the sandboxed page has no modal dialogs.
function confirmButton(label, armedLabel, hint, act) {
  const b = button(label, async () => {
    if (b.textContent !== armedLabel) {
      b.textContent = armedLabel;
      b.armedAt = Date.now();
      say(hint);
      setTimeout(() => { b.textContent = label; }, CONFIRM_MS);
      return;
    }
    if (Date.now() - b.armedAt < ARM_GUARD_MS) return;
    b.textContent = label;
    await act();
  });
  return b;
}

// orderTickets is "2 x GA, 1 x VIP": from the lines of a paid order, or from
// the tickets of a free one.
function orderTickets(o) {
  const lines = (o.lines || []).map((l) => `${l.qty} x ${l.name}`);
  if (lines.length) return lines.join(", ");
  const counts = new Map();
  for (const t of o.tickets || []) counts.set(t.type_name, (counts.get(t.type_name) || 0) + 1);
  return [...counts].map(([name, n]) => `${n} x ${name}`).join(", ");
}

function orderStatus(o) {
  const names = { paid: "Paid", pending: "Waiting for payment", confirmed: "Confirmed", refunded: "Refunded", cancelled: "Cancelled", expired: "Expired" };
  let text = names[o.status] || o.status;
  if ((o.status === "paid" || o.status === "cancelled") && o.refunded_cents > 0) text += ` (${formatMoney(o.refunded_cents, o.currency)} refunded)`;
  if (o.problem === "amount_mismatch") text += ". The amount paid did not match: check it at the provider.";
  if (o.problem === "refund_failed") text += ". A refund failed: refund it at the provider.";
  return text;
}

function orderRow(o) {
  const owner = isOwner();
  const left = o.total_cents - o.refunded_cents;
  // a cancelled order that was paid still holds money the owner can give back
  const canRefund = owner && (o.status === "paid" || (o.status === "cancelled" && Boolean(o.paid_at))) && PROVIDERS[o.provider] && left > 0;
  const canCancel = owner && ["paid", "pending", "confirmed"].includes(o.status);
  const actions = el("td", { className: "actions" });
  if (canRefund) actions.append(button("Refund", () => openRefund(o)));
  if (canCancel) {
    actions.append(confirmButton("Cancel", "Confirm cancel", "Click Confirm cancel to cancel the tickets of this order. This does not refund: use Refund for the money.",
      () => cancelOrder(o.id)));
  }
  return el("tr", {},
    cell(`#${o.id}`),
    cell(o.name, el("br"), el("span", { className: "muted", textContent: o.email || "" })),
    cell(orderTickets(o)),
    cell(formatMoney(o.total_cents, o.currency)),
    cell(PROVIDERS[o.provider] || "-"),
    cell(orderStatus(o)),
    actions);
}

async function loadOrders(more) {
  const f = state.orderFilter;
  const query = {};
  if (f.status) query.status = f.status;
  if (f.event) query.event = f.event;
  if (f.q) query.q = f.q;
  if (more && state.orderCursor) query.cursor = state.orderCursor;
  const data = await run(() => api("GET", "/admin/orders", null, query));
  if (!data) return;
  say("");
  state.orders = more ? state.orders.concat(data.orders || []) : data.orders || [];
  state.orderCursor = data.next_cursor || "";
  $("#or-state").textContent = state.orders.length ? "" : "No orders yet.";
  $("#or-table").hidden = !state.orders.length;
  $("#or-more").hidden = !state.orderCursor;
  $("#or-rows").replaceChildren(...state.orders.map(orderRow));
}

$("#or-status").replaceChildren(...STATUSES.map(([value, text]) => el("option", { value, textContent: text })));
$("#or-go").addEventListener("click", () => {
  state.orderFilter = { status: $("#or-status").value, event: $("#or-event").value, q: $("#or-search").value.trim() };
  loadOrders(false);
});
$("#or-more").addEventListener("click", () => loadOrders(true));

async function cancelOrder(id) {
  const data = await run(() => api("POST", `/admin/orders/${encodeURIComponent(id)}/cancel`));
  if (!data) return;
  say("Order cancelled. No money was returned.");
  loadOrders(false);
  loadEvents(false);
}

// refundRequest checks the amount and picks the tickets: the body of the
// refund route, or an error text.
function refundRequest(order, amountText, ticketIDs, exp = exponent()) {
  const cents = parseMoney(amountText, exp);
  const left = order.total_cents - order.refunded_cents;
  if (!cents || cents > left) return { error: `Enter an amount from ${centsText(1, exp)} to ${centsText(left, exp)}.` };
  return { body: { amount_cents: cents, attendees: ticketIDs } };
}

function openRefund(o) {
  state.refunding = { order: o, picked: new Set() };
  const left = o.total_cents - o.refunded_cents;
  $("#or-refund").hidden = false;
  $("#or-refund-title").textContent = `Refund order #${o.id} (${o.name})`;
  $("#or-refund-amount").value = centsText(left);
  $("#or-refund-cur").textContent = `${o.currency}, up to ${centsText(left)}`;
  $("#or-refund-tickets").replaceChildren(...(o.tickets || []).filter((t) => t.status === "active").map((t) => {
    const cb = el("input", { type: "checkbox" });
    cb.addEventListener("change", () => { if (cb.checked) state.refunding.picked.add(t.id); else state.refunding.picked.delete(t.id); });
    return el("div", {}, el("label", {}, cb, ` ${t.type_name} (ticket ${t.id})`));
  }));
}

function closeRefund() {
  state.refunding = null;
  $("#or-refund").hidden = true;
}

// submitRefund asks the route to refund; the route calls the provider first.
async function submitRefund() {
  const r = state.refunding;
  if (!r) return;
  const req = refundRequest(r.order, $("#or-refund-amount").value, [...r.picked]);
  if (req.error) { say(req.error, true); return; }
  const data = await run(() => api("POST", `/admin/orders/${encodeURIComponent(r.order.id)}/refund`, req.body));
  if (!data) return;
  say(`Refunded ${formatMoney(req.body.amount_cents, r.order.currency)}. The buyer is emailed.`);
  closeRefund();
  loadOrders(false);
  loadEvents(false);
}

$("#or-refund-act").append(confirmButton("Refund", "Confirm refund", "Click Confirm refund to send the money back to the buyer. This cannot be undone.", submitRefund));
$("#or-refund-close").addEventListener("click", closeRefund);

// ---- Coupons

function couponValue(c) {
  return c.kind === "percent" ? `${c.value}%` : formatMoney(c.value, state.settings.currency);
}

function couponScope(c) {
  const ev = state.events.find((e) => e.id === c.event_id);
  const where = c.event_id ? (ev ? ev.name : c.event_id) : "Any event";
  return c.type_id ? `${where}, one ticket type` : where;
}

// bundlesText is the bundle rules in words, one per rule, "none" without any.
function bundlesText(rules) {
  if (!rules.length) return "none";
  return rules.map((b) => {
    const what = b.all_types ? "tickets" : `${(b.type_names || []).join(" / ") || "selected"} tickets`;
    const off = b.kind === "percent" ? `${b.value}%` : formatMoney(b.value, state.settings.currency);
    return `${b.min_qty}+ ${what} -> ${off} off`;
  }).join("; ");
}

function couponRow(c) {
  const uses = c.max_uses ? `${c.used}/${c.max_uses}` : String(c.used);
  return el("tr", {},
    cell(c.code + (c.active ? "" : " (off)")), cell(couponValue(c)), cell(uses), cell(day(c.expires_at)), cell(couponScope(c)),
    el("td", { className: "actions" }, ...(isOwner() ? [button("Edit", () => editCoupon(c)), button("Delete", () => deleteCoupon(c))] : [])));
}

async function loadCoupons() {
  const data = await run(() => api("GET", "/admin/coupons"));
  if (!data) return;
  state.coupons = data.coupons || [];
  $("#or-bundles").textContent = `Bundles: ${bundlesText(data.bundles || [])}. Change them in each event's editor.`;
  $("#cp-state").textContent = state.coupons.length ? "" : "No coupons yet.";
  $("#cp-table").hidden = !state.coupons.length;
  $("#cp-rows").replaceChildren(...state.coupons.map(couponRow));
}

function editCoupon(c) {
  state.coupon = { id: c ? c.id : 0, type_id: c ? c.type_id : 0 };
  $("#cp-title").textContent = c ? `Edit coupon ${c.code}` : "New coupon";
  $("#cp-code").value = c ? c.code : "";
  $("#cp-kind").value = c ? c.kind : "percent";
  $("#cp-value").value = c ? (c.kind === "percent" ? String(c.value) : centsText(c.value)) : "";
  $("#cp-max").value = c && c.max_uses ? String(c.max_uses) : "";
  $("#cp-until").value = c ? day(c.expires_at) : "";
  $("#cp-event").value = c ? c.event_id : "";
  $("#cp-active").checked = c ? c.active : true;
}

// couponRequest is the body of the coupon route, or an error text.
function couponRequest(f, exp = exponent()) {
  const value = f.kind === "percent" ? (/^\d{1,3}$/.test(f.value.trim()) ? Number(f.value) : null) : parseMoney(f.value, exp);
  if (!/^[A-Za-z0-9_-]{3,32}$/.test(f.code.trim())) return { error: "A code has 3 to 32 letters, digits, dashes or underscores." };
  if (!value || (f.kind === "percent" && value > 100)) return { error: "Give a discount: a percent from 1 to 100, or an amount." };
  const body = { code: f.code.trim(), kind: f.kind, value, active: f.active };
  if (f.id) body.id = f.id;
  if (f.event_id) body.event_id = f.event_id;
  if (f.type_id) body.type_id = f.type_id;
  if (f.max) body.max_uses = Number(f.max);
  if (f.until) body.expires_at = f.until;
  return { body };
}

async function saveCoupon() {
  const req = couponRequest({
    id: state.coupon.id, type_id: state.coupon.type_id, code: $("#cp-code").value, kind: $("#cp-kind").value, value: $("#cp-value").value,
    max: $("#cp-max").value, until: $("#cp-until").value, event_id: $("#cp-event").value, active: $("#cp-active").checked,
  });
  if (req.error) { say(req.error, true); return; }
  const data = await run(() => api("POST", "/admin/coupons", req.body));
  if (!data) return;
  say("Saved.");
  editCoupon(null);
  loadCoupons();
}

async function deleteCoupon(c) {
  const data = await run(() => api("POST", `/admin/coupons/${encodeURIComponent(c.id)}/delete`));
  if (!data) return;
  say(data.deleted ? "Deleted." : "This coupon was used, so it was switched off instead of deleted.");
  loadCoupons();
}

$("#cp-kind").replaceChildren(el("option", { value: "percent", textContent: "Percent" }), el("option", { value: "fixed", textContent: "Fixed amount" }));
$("#cp-save").addEventListener("click", saveCoupon);
$("#cp-reset").addEventListener("click", () => editCoupon(null));

// feesText is the fee rule of the settings, in words ("none" without one).
function feesText(s) {
  const parts = [];
  if (s.fee_fixed_cents > 0) parts.push(`${formatMoney(s.fee_fixed_cents, s.currency)} per ${s.fee_per === "ticket" ? "paid ticket" : "order"}`);
  if (s.fee_percent_bp > 0) parts.push(`${s.fee_percent_bp / 100}%`);
  if (!parts.length) return "none";
  return `${parts.join(" plus ")}, ${s.fee_payer === "organizer" ? "paid by you" : "passed to the buyer"}`;
}

function openOrders() {
  $("#or-fees").textContent = `Fees: ${feesText(state.settings)}. Change them in the plugin settings form.`;
  $("#cp-form").hidden = !isOwner();
  loadOrders(false);
  loadCoupons();
}

// ---- Settings

async function fetchSettings() {
  const s = await run(() => api("GET", "/admin/settings"));
  if (s) state.settings = s;
  return s;
}

async function loadSettings() {
  const s = await fetchSettings();
  if (!s) return;
  const rows = [
    ["Currency", s.currency],
    ["Sender name", s.sender_name || "(the site name)"],
    ["Confirmation message", s.confirmation_text_set ? "Set" : "Not set"],
    ["Default number of tickets", String(s.default_capacity)],
    ["Keep attendee details for", `${s.retention_months} months`],
    ["Site address", s.site_url || "(not set)"],
    ["Event content type", s.schema_slug || "(calendar events)"],
    ["Stripe", s.stripe_configured ? "Connected" : "Not set up"],
    ["PayPal", s.paypal_configured ? `Connected${s.paypal_sandbox ? " (sandbox)" : ""}${s.paypal_webhook_set ? "" : ", webhook id missing"}` : "Not set up"],
    ["Fees", feesText(s)],
  ];
  $("#st-rows").replaceChildren(...rows.map(([k, v]) => el("tr", {}, el("th", { textContent: k }), cell(v))));
}

// ---- Tabs

const TABS = { events: () => loadEvents(false), attendees: () => loadAttendees(1), orders: openOrders, settings: loadSettings };
const loaded = new Set();

function showTab(name) {
  for (const tab of Object.keys(TABS)) {
    $(`#panel-${tab}`).hidden = tab !== name;
    $(`#tab-${tab}`).classList.toggle("active", tab === name);
  }
  say("");
  if (name === "attendees" || name === "orders" || !loaded.has(name)) {
    loaded.add(name);
    TABS[name]();
  }
}

for (const tab of Object.keys(TABS)) $(`#tab-${tab}`).addEventListener("click", () => showTab(tab));

hatamoto.ready.then(async () => {
  await fetchSettings();
  showTab("events");
});

if (typeof module !== "undefined") {
  module.exports = {
    csvPath, resendAttendee, cancelAttendee, parseMoney, centsText, formatMoney, orderTickets, orderStatus, refundRequest, bundlesBody, bundlesText, couponRequest,
    feesText, submitRefund, openRefund, state,
  };
}
