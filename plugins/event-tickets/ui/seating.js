"use strict";
// Seating tab of the admin page: maps, grid sections, GA zones and blocked
// seats. The page script hands in its helpers with seating.init; this file
// builds nodes with textContent and value only and reaches the plugin through
// the page's api() (the hatamoto bridge), never the network.

const seating = (() => {
  let h = null; // { el, api, say, confirmButton } from app.js
  const state = { maps: [], map: null, seats: [], block: false, form: null };
  const q = (sel) => document.querySelector(sel);

  // nextLabel is the row label after one of the same kind ("A" -> "B", "AZ" -> "BA", "9" -> "10"); "" at the end.
  function nextLabel(s) {
    if (/^[0-9]+$/.test(s)) return String(Number(s) + 1);
    const b = s.split("");
    for (let i = b.length - 1; i >= 0; i--) {
      if (b[i] < "Z") { b[i] = String.fromCharCode(b[i].charCodeAt(0) + 1); return b.join(""); }
      b[i] = "A";
    }
    return "";
  }

  // rowsText turns row labels into what the owner types: ["A".."J"] -> "A-J".
  function rowsText(rows) {
    const out = [];
    for (let i = 0; i < rows.length;) {
      let j = i;
      while (j + 1 < rows.length && nextLabel(rows[j]) === rows[j + 1]) j++;
      out.push(j > i ? `${rows[i]}-${rows[j]}` : rows[i]);
      i = j + 1;
    }
    return out.join(",");
  }

  // sectionRequest is the body of POST /admin/maps/{id}/sections from the form
  // values, or { error } for a value the server would refuse.
  function sectionRequest(f) {
    const name = String(f.name || "").trim();
    if (!name) return { error: "Give the section a name." };
    const body = { id: f.id || 0, kind: f.kind, name, tier: String(f.tier || "").trim(), rows: "", seats_per_row: 0, capacity: 0, position: f.position || 0 };
    if (f.kind === "zone") {
      const cap = Number(f.capacity);
      if (!Number.isInteger(cap) || cap < 1 || cap > 100000) return { error: "Capacity is a whole number from 1 to 100000." };
      body.capacity = cap;
      return { body };
    }
    const rows = String(f.rows || "").trim();
    const per = Number(f.seatsPerRow);
    if (!rows) return { error: "Name the rows, for example A-J." };
    if (!Number.isInteger(per) || per < 1 || per > 100) return { error: "Seats per row is a whole number from 1 to 100." };
    body.rows = rows;
    body.seats_per_row = per;
    return { body };
  }

  // sectionText is the one line the owner reads for a section.
  function sectionText(s) {
    const tier = s.tier ? `  tier ${s.tier}` : "";
    if (s.kind === "zone") return `GA zone ${s.name}  capacity ${s.capacity}${tier}`;
    return `Section ${s.name}  rows ${rowsText(s.rows || [])} x ${s.seats_per_row} seats  price tier ${s.tier || "-"}`;
  }

  async function run(fn) {
    try { return await fn(); } catch (e) { h.say(e.message, true); return undefined; }
  }

  const btn = (text, onClick, props = {}) => {
    const b = h.el("button", { type: "button", textContent: text, ...props });
    b.addEventListener("click", onClick);
    return b;
  };

  async function open() {
    await run(async () => {
      state.maps = (await h.api("GET", "/admin/maps")).maps || [];
      renderMaps();
      if (state.map) await edit(state.map.id);
      else renderEditor();
    });
  }

  function renderMaps() {
    q("#st-maps").replaceChildren(...state.maps.map((m) => h.el("div", { className: "row" },
      h.el("strong", { textContent: m.name }),
      h.el("span", { className: "muted", textContent: `${m.seats} seats, ${m.sections.length} sections` }),
      btn("Edit", () => run(() => edit(m.id))),
      h.confirmButton("Delete", "Confirm delete", "Click Confirm delete to remove this map. A map used by ticket types cannot be deleted.", () => run(async () => {
        await h.api("POST", `/admin/maps/${encodeURIComponent(m.id)}/delete`);
        if (state.map && state.map.id === m.id) { state.map = null; state.form = null; }
        h.say("Deleted.");
        await open();
      })))));
  }

  async function edit(id) {
    const data = await h.api("GET", `/admin/maps/${encodeURIComponent(id)}`);
    state.map = data.map;
    state.seats = data.seats || [];
    // the list line follows the map as sections change
    const at = state.maps.findIndex((x) => x.id === data.map.id);
    if (at >= 0) { state.maps[at] = data.map; renderMaps(); }
    renderEditor();
  }

  function renderEditor() {
    const m = state.map;
    q("#st-ed").hidden = !m;
    if (!m) return;
    q("#st-name").value = m.name;
    q("#st-block").textContent = state.block ? "Done blocking" : "Block seat";
    q("#st-sections").replaceChildren(...m.sections.map((s) => h.el("div", { className: "row" },
      h.el("span", { textContent: sectionText(s) }),
      btn("Edit", () => { state.form = formOf(s); renderForm(); }),
      h.confirmButton("Delete", "Confirm delete", "Click Confirm delete to remove this section. A section used by a ticket type cannot be deleted.", () => run(async () => {
        await h.api("POST", `/admin/sections/${encodeURIComponent(s.id)}/delete`);
        h.say("Deleted.");
        await edit(m.id);
      })))));
    renderForm();
    renderGrid();
  }

  const formOf = (s) => ({ id: s.id, kind: s.kind, name: s.name, tier: s.tier, rows: rowsText(s.rows || []), seatsPerRow: s.seats_per_row || 20, capacity: s.capacity || 80, position: s.position });

  function renderForm() {
    const f = state.form;
    const box = q("#st-form");
    box.hidden = !f;
    if (!f) return;
    const field = (label, key, props) => {
      const input = h.el("input", { value: String(f[key]), ...props });
      input.addEventListener("input", () => { f[key] = input.value; });
      return h.el("label", {}, `${label} `, input);
    };
    const grid = f.kind !== "zone";
    box.replaceChildren(
      h.el("h2", { textContent: grid ? (f.id ? "Edit section" : "New section") : (f.id ? "Edit GA zone" : "New GA zone") }),
      h.el("div", { className: "row" },
        field("Name", "name", { type: "text", maxLength: 80 }),
        ...(grid ? [field("Rows", "rows", { type: "text", maxLength: 120, placeholder: "A-J" }), field("Seats per row", "seatsPerRow", { type: "number", min: "1", max: "100" })]
          : [field("Capacity", "capacity", { type: "number", min: "1", max: "100000" })]),
        field("Price tier", "tier", { type: "text", maxLength: 40 })),
      h.el("div", { className: "row" },
        btn("Save section", () => run(async () => {
          const req = sectionRequest(f);
          if (req.error) throw new Error(req.error);
          if (!f.id) req.body.position = state.map.sections.length;
          await h.api("POST", `/admin/maps/${encodeURIComponent(state.map.id)}/sections`, req.body);
          state.form = null;
          h.say("Saved.");
          await edit(state.map.id);
        })),
        btn("Cancel", () => { state.form = null; renderForm(); })));
  }

  // renderGrid draws the seats of every grid section; in block mode a click on
  // a seat blocks it (or lets it go again).
  function renderGrid() {
    const parts = [];
    state.map.sections.forEach((s, i) => {
      if (s.kind !== "grid") return;
      const rows = new Map();
      for (const [id, row, label, num, blocked] of state.seats[i] || []) {
        if (!rows.has(row)) rows.set(row, { label, cells: [] });
        rows.get(row).cells.push({ id, num, blocked });
      }
      parts.push(h.el("h3", { textContent: s.name }));
      for (const r of rows.values()) {
        parts.push(h.el("div", { className: "st-row" }, h.el("span", { className: "st-label", textContent: r.label }), ...r.cells.map((c) => {
          const b = btn(c.blocked ? "b" : "o", () => run(async () => {
            await h.api("POST", `/admin/sections/${encodeURIComponent(s.id)}/seats`, { seats: [c.id], blocked: !c.blocked });
            await edit(state.map.id);
          }), { className: c.blocked ? "st-seat st-blocked" : "st-seat", disabled: !state.block });
          b.setAttribute("aria-label", `Row ${r.label} seat ${c.num}, ${c.blocked ? "blocked" : "free"}`);
          return b;
        })));
      }
    });
    q("#st-grid").replaceChildren(...parts);
  }

  function init(helpers) {
    h = helpers;
    q("#st-add-map").addEventListener("click", () => run(async () => {
      const name = q("#st-new-name").value.trim();
      if (!name) throw new Error("Give the map a name.");
      const data = await h.api("POST", "/admin/maps", { name });
      q("#st-new-name").value = "";
      state.form = null;
      await edit(data.id);
      state.maps = (await h.api("GET", "/admin/maps")).maps || [];
      renderMaps();
    }));
    q("#st-rename").addEventListener("click", () => run(async () => {
      await h.api("POST", "/admin/maps", { id: state.map.id, name: q("#st-name").value.trim() });
      h.say("Saved.");
      await open();
    }));
    q("#st-add-section").addEventListener("click", () => { state.form = { id: 0, kind: "grid", name: "", tier: "", rows: "", seatsPerRow: 20, capacity: 80, position: 0 }; renderForm(); });
    q("#st-add-zone").addEventListener("click", () => { state.form = { id: 0, kind: "zone", name: "", tier: "", rows: "", seatsPerRow: 20, capacity: 80, position: 0 }; renderForm(); });
    q("#st-block").addEventListener("click", () => {
      state.block = !state.block;
      h.say(state.block ? "Click a seat to block it or to let it go again." : "");
      renderEditor();
    });
  }

  return { init, open, sectionRequest, rowsText, sectionText, state };
})();

if (typeof module !== "undefined") module.exports = seating;
