// Runs assets/plugin.js against a minimal DOM stub. No dependencies:
//   node assets_test.js
// Lives outside assets/ because everything under assets/ is served publicly.
'use strict';
var fs = require('fs');
var path = require('path');
var vm = require('vm');

var src = fs.readFileSync(path.join(__dirname, 'assets', 'plugin.js'), 'utf8');
var failures = 0;

function check(name, cond) {
  if (!cond) { failures++; console.error('FAIL: ' + name); } else console.log('ok: ' + name);
}

// ---- fake DOM -------------------------------------------------------------

function El(tag, cls, attrs, kids) {
  this.tagName = tag;
  this.className = cls || '';
  this.attrs = attrs || {};
  this.children = [];
  this.parentNode = null;
  this.textContent = '';
  this.elements = [];
  this.submitted = 0;
  this.prevented = 0;
  var self = this;
  (kids || []).forEach(function (k) { self.appendChild(k); });
}
El.prototype.getAttribute = function (n) { return n in this.attrs ? this.attrs[n] : null; };
El.prototype.appendChild = function (k) {
  if (k.parentNode) k.parentNode.removeChild(k);
  k.parentNode = this;
  this.children.push(k);
  return k;
};
El.prototype.removeChild = function (k) {
  var i = this.children.indexOf(k);
  if (i < 0) throw new Error('not a child');
  this.children.splice(i, 1);
  k.parentNode = null;
  return k;
};
El.prototype.submit = function () { this.submitted++; };
El.prototype.matches = function (sel) {
  var m = /^([a-z]*)(?:\.([\w-]+))?(?:\[([\w-]+)(?:([\^*]?)="([^"]*)")?\])?$/.exec(sel);
  if (!m || !(m[1] || m[2] || m[3])) throw new Error('unexpected selector: ' + sel);
  if (m[1] && this.tagName !== m[1]) return false;
  if (m[2] && (' ' + this.className + ' ').indexOf(' ' + m[2] + ' ') < 0) return false;
  if (m[3]) {
    var v = this.getAttribute(m[3]);
    if (v === null) return false;
    if (m[4] === '^' && v.indexOf(m[5]) !== 0) return false;
    if (m[4] === '*' && v.indexOf(m[5]) < 0) return false;
    if (m[4] === '' && v !== m[5]) return false;
  }
  return true;
};
El.prototype.querySelectorAll = function (sel) {
  var out = [];
  (function walk(n) {
    n.children.forEach(function (c) { if (c.matches(sel)) out.push(c); walk(c); });
  })(this);
  return out;
};
El.prototype.querySelector = function (sel) { return this.querySelectorAll(sel)[0] || null; };

function el(tag, cls, attrs, kids) { return new El(tag, cls, attrs, kids); }

function form(cls, action, fields) {
  var f = el('form', cls, { action: action });
  f.elements = fields.map(function (p) { return { name: p[0], value: p[1], type: p[2] || 'hidden' }; });
  f.elements.push({ name: '', value: 'x', type: 'submit' });
  return f;
}

function mkDoc(root) {
  var doc = el('#document');
  doc.appendChild(root);
  doc.listeners = [];
  doc.readyState = 'complete';
  doc.addEventListener = function (type, fn, cap) { doc.listeners.push({ type: type, fn: fn, cap: cap }); };
  doc.fire = function (type, target) {
    var ev = { target: target, preventDefault: function () { target.prevented++; } };
    doc.listeners.forEach(function (l) { if (l.type === type) l.fn(ev); });
  };
  return doc;
}

// fetch fake: reply(call) returns a Promise or throws.
function mkFetch(reply) {
  var f = function (url, init) { f.calls.push({ url: url, init: init }); return reply(url, init); };
  f.calls = [];
  return f;
}
function ok(json) { return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve(json); } }); }
function status(n) { return Promise.resolve({ ok: false, status: n, json: function () { return Promise.resolve({ error: 'x' }); } }); }

function flush() { return new Promise(function (r) { setTimeout(r, 5); }); }

// boot runs the script (twice by default) in a fresh context.
function boot(root, fetchFn, times) {
  var doc = mkDoc(root);
  var win = { fetch: fetchFn };
  if (fetchFn === undefined) delete win.fetch;
  win.document = doc;
  var ctx = vm.createContext({ window: win, document: doc, encodeURIComponent: encodeURIComponent, Promise: Promise });
  for (var i = 0; i < (times || 2); i++) vm.runInContext(src, ctx);
  return { doc: doc, win: win };
}

// ---- fixtures -------------------------------------------------------------

function storyRow() {
  var vf = form('hn-vote-form', '/p/hn/vote/story/7', [['how', 'up'], ['next', '/news']]);
  var cell = el('span', 'hn-vote-cell', {}, [vf]);
  var score = el('span', 'hn-score');
  score.textContent = '98 points';
  var row = el('li', 'hn-story', {}, [cell, el('div', 'hn-sub', {}, [score])]);
  return { row: row, form: vf, cell: cell, score: score, root: el('main', '', {}, [el('ol', 'hn-stories', {}, [row])]) };
}

function commentCell() {
  var up = form('hn-vote-form', '/p/hn/vote/comment/c1', [['how', 'up']]);
  var down = form('hn-vote-form', '/p/hn/vote/comment/c1', [['how', 'down']]);
  var cell = el('span', 'hn-vote', { 'data-hn-comment': 'c1' }, [up, down]);
  return { up: up, down: down, cell: cell, root: el('main', '', {}, [el('div', 'hn-threads', {}, [cell])]) };
}

// ---- cases ----------------------------------------------------------------

async function main() {
  var s, c, b, call;

  // 1. story vote success
  s = storyRow();
  b = boot(s.root, mkFetch(function () { return ok({ points: 99, voted: true }); }));
  b.doc.fire('submit', s.form);
  await flush();
  call = b.win.fetch.calls[0];
  check('story: one fetch', b.win.fetch.calls.length === 1);
  check('story: url, method, credentials', call.url === '/p/hn/vote/story/7' && call.init.method === 'POST' && call.init.credentials === 'same-origin');
  check('story: Accept json', call.init.headers.Accept === 'application/json');
  check('story: body', call.init.body === 'how=up&next=%2Fnews');
  check('story: preventDefault', s.form.prevented === 1);
  check('story: score updated', s.score.textContent === '99 points');
  check('story: form removed', s.cell.children.length === 0 && s.form.parentNode === null);
  check('story: cell marked voted', s.cell.className.indexOf('hn-voted') >= 0);
  check('story: no submit()', s.form.submitted === 0);

  // 2. singular
  s = storyRow();
  b = boot(s.root, mkFetch(function () { return ok({ points: 1, voted: true }); }));
  b.doc.fire('submit', s.form);
  await flush();
  check('story: 1 point', s.score.textContent === '1 point');

  // 2b. no points number: text untouched
  s = storyRow();
  b = boot(s.root, mkFetch(function () { return ok({ points: '5', voted: true }); }));
  b.doc.fire('submit', s.form);
  await flush();
  check('story: non-numeric points keep the text', s.score.textContent === '98 points' && s.cell.children.length === 0);

  // 3. comment vote success
  c = commentCell();
  b = boot(c.root, mkFetch(function () { return ok({ voted: true }); }));
  b.doc.fire('submit', c.up);
  await flush();
  check('comment: both forms removed', c.cell.children.length === 0);
  check('comment: no submit()', c.up.submitted === 0 && c.down.submitted === 0);

  // 4-8. failures fall back to a plain submit and keep the form
  var bad = [
    ['error body', function () { return ok({ error: 'no' }); }],
    ['missing voted', function () { return ok({}); }],
    ['non-boolean voted', function () { return ok({ voted: 'yes' }); }],
    ['403', function () { return status(403); }],
    ['network error', function () { return Promise.reject(new Error('net')); }],
    ['non-JSON', function () { return Promise.resolve({ ok: true, json: function () { return Promise.reject(new Error('json')); } }); }],
    ['null body', function () { return ok(null); }],
    ['sync throw', function () { throw new Error('boom'); }]
  ];
  for (var i = 0; i < bad.length; i++) {
    s = storyRow();
    b = boot(s.root, mkFetch(bad[i][1]));
    var threw = false;
    try { b.doc.fire('submit', s.form); } catch (e) { threw = true; }
    await flush();
    check('fallback ' + bad[i][0] + ': submit() once, form kept, no throw',
      !threw && s.form.submitted === 1 && s.form.parentNode === s.cell && s.score.textContent === '98 points');
  }

  // 10. other forms are ignored
  s = storyRow();
  var act = form('hn-act', '/p/hn/hide/7', [['next', '/']]);
  s.row.appendChild(act);
  b = boot(s.root, mkFetch(function () { return ok({ voted: true }); }));
  b.doc.fire('submit', act);
  await flush();
  check('other form: no fetch, no preventDefault', b.win.fetch.calls.length === 0 && act.prevented === 0);

  // 11. double submit in flight
  s = storyRow();
  b = boot(s.root, mkFetch(function () { return ok({ points: 2, voted: true }); }));
  b.doc.fire('submit', s.form);
  b.doc.fire('submit', s.form);
  await flush();
  check('double submit: one fetch', b.win.fetch.calls.length === 1);

  // 12. idempotence (boot runs the script twice)
  check('idempotent: one submit listener', b.doc.listeners.filter(function (l) { return l.type === 'submit'; }).length === 1);

  // 13. no fetch: nothing bound
  s = storyRow();
  b = boot(s.root, undefined);
  check('no fetch: no listener', b.doc.listeners.length === 0);

  // 14. userbar
  var ub = el('div', 'hn-ub'), slot = el('span', 'hn-userbar');
  var root = el('main', '', {}, [el('header', '', {}, [slot]), ub]);
  b = boot(root, mkFetch(function () { return ok({}); }));
  check('userbar: moved once into the slot', ub.parentNode === slot && slot.children.length === 1);
  ub = el('div', 'hn-ub');
  root = el('main', '', {}, [ub]);
  b = boot(root, mkFetch(function () { return ok({}); }));
  check('userbar: no slot, unchanged', ub.parentNode === root);
  slot = el('span', 'hn-userbar');
  root = el('main', '', {}, [slot]);
  b = boot(root, mkFetch(function () { return ok({}); }));
  check('userbar: no bar, unchanged', slot.children.length === 0);

  // 15. static checks on the source
  ['eval', 'Function(', 'innerHTML', 'cookie', 'localStorage', 'sessionStorage', 'http:', 'https:', '=>', 'let ', 'const ', 'document.write']
    .forEach(function (bannedText) { check('source has no ' + bannedText, src.indexOf(bannedText) < 0); });
  check('source has no inline handler strings', !/\bon[a-z]+\s*=/.test(src));
  check('source < 6144 bytes (' + Buffer.byteLength(src) + ')', Buffer.byteLength(src) < 6144);
  check('assets holds only plugin.js', fs.readdirSync(path.join(__dirname, 'assets')).join() === 'plugin.js');
}

main().then(function () {
  if (failures) { console.error(failures + ' check(s) failed'); process.exit(1); }
}, function (e) { console.error(e); process.exit(1); });
