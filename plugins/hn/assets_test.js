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

function Text(v) { this.nodeType = 3; this.nodeValue = v; this.parentNode = null; this.children = []; }
Text.prototype.cloneNode = function () { return new Text(this.nodeValue); };
Text.prototype.matches = function () { return false; };

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
  if (this.tagName === 'form' && (k.tagName === 'input' || k.tagName === 'button')) this.elements.push(k);
  return k;
};
El.prototype.insertBefore = function (k, ref) {
  if (!ref) return this.appendChild(k);
  if (k.parentNode) k.parentNode.removeChild(k);
  var i = this.children.indexOf(ref);
  if (i < 0) throw new Error('not a child');
  k.parentNode = this;
  this.children.splice(i, 0, k);
  return k;
};
El.prototype.cloneNode = function () {
  var c = new El(this.tagName, this.className, JSON.parse(JSON.stringify(this.attrs)));
  this.children.forEach(function (k) { c.appendChild(k.cloneNode ? k.cloneNode(true) : k); });
  this.elements.forEach(function (e) { if (!(e instanceof El)) c.elements.push({ name: e.name, value: e.value, type: e.type }); });
  c.textContent = this.textContent;
  return c;
};
Object.defineProperty(El.prototype, 'nextSibling', {
  get: function () {
    if (!this.parentNode) return null;
    var sib = this.parentNode.children;
    return sib[sib.indexOf(this) + 1] || null;
  }
});
El.prototype.removeChild = function (k) {
  var i = this.children.indexOf(k);
  if (i < 0) throw new Error('not a child');
  this.children.splice(i, 1);
  k.parentNode = null;
  return k;
};
Object.defineProperty(El.prototype, 'previousSibling', {
  get: function () {
    if (!this.parentNode) return null;
    var sib = this.parentNode.children, i = sib.indexOf(this);
    return i > 0 ? sib[i - 1] : null;
  }
});
Object.defineProperty(Text.prototype, 'previousSibling', Object.getOwnPropertyDescriptor(El.prototype, 'previousSibling'));
Object.defineProperty(Text.prototype, 'nextSibling', Object.getOwnPropertyDescriptor(El.prototype, 'nextSibling'));
El.prototype.submit = function () { this.submitted++; };
El.prototype.setAttribute = function (n, v) { this.attrs[n] = String(v); };
El.prototype.replaceChild = function (k, old) {
  var i = this.children.indexOf(old);
  if (i < 0) throw new Error('not a child');
  if (k.parentNode) k.parentNode.removeChild(k);
  this.children[i] = k;
  k.parentNode = this;
  old.parentNode = null;
  return old;
};
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
  f.appendChild(el('button'));
  return f;
}

function mkDoc(root) {
  var doc = el('#document');
  doc.appendChild(root);
  doc.listeners = [];
  doc.readyState = 'complete';
  doc.createElement = function (tag) { return el(tag); };
  doc.createTextNode = function (v) { return new Text(v); };
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

function body2(f) { return f.elements.filter(function (e) { return e.name; }).map(function (e) { return e.name + '=' + encodeURIComponent(e.value); }).join('&'); }

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
  var vf = form('hn-vote-form', '/p/hn/vote/story/7', [['how', 'up'], ['next', '/news#7']]);
  var cell = el('span', 'hn-vote-cell', {}, [vf]);
  var score = el('span', 'hn-score');
  score.textContent = '98 points';
  var user = el('a', 'hn-user', { href: '/user?id=bob' }), age = el('a', '', { href: '/item/7' });
  var sub = el('div', 'hn-sub', {}, [score, new Text(' by '), user, new Text(' '), age, new Text(' | '), el('a', 'hn-past', { href: '/search?q=t' })]);
  var row = el('li', 'hn-story', {}, [cell, sub]);
  return { row: row, form: vf, cell: cell, score: score, sub: sub, age: age, root: el('main', '', {}, [el('ol', 'hn-stories', {}, [row])]) };
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
  function replyVote(url, init) { return ok({ points: 99, voted: url.indexOf('how=un') < 0 && init.body.indexOf('how=un') < 0 }); }
  b = boot(s.root, mkFetch(replyVote));
  b.doc.fire('submit', s.form);
  await flush();
  call = b.win.fetch.calls[0];
  check('story: one fetch', b.win.fetch.calls.length === 1);
  check('story: url, method, credentials', call.url === '/p/hn/vote/story/7' && call.init.method === 'POST' && call.init.credentials === 'same-origin');
  check('story: Accept json', call.init.headers.Accept === 'application/json');
  check('story: body', call.init.body === 'how=up&next=%2Fnews%237');
  check('story: preventDefault', s.form.prevented === 1);
  var un = s.sub.querySelectorAll('form.hn-un');
  check('story: score text untouched', s.score.textContent === '98 points');
  check('story: vote form kept in the DOM, hidden', s.form.parentNode === s.cell && s.form.className.indexOf('hn-gone') >= 0 && s.cell.children.length === 1);
  check('story: cell marked voted', s.cell.className.indexOf('hn-voted') >= 0);
  check('story: no submit()', s.form.submitted === 0);
  check('story: exactly one unvote form after the age link, preceded by " | "',
    un.length === 1 && un[0].previousSibling.nodeValue === ' | ' && un[0].previousSibling.previousSibling === s.age);
  check('story: unvote action and next', un[0].getAttribute('action') === '/p/hn/vote/story/7?how=un' && body2(un[0]) === 'next=%2Fnews%237');
  check('story: unvote button', un[0].querySelector('button').textContent === 'unvote' && un[0].className.indexOf('hn-act') >= 0);
  check('story: row not moved', s.row.parentNode !== null && s.sub.children.length === 9);

  // 1b. JS unvote of that control: arrow back, control hidden, nothing removed
  var n0 = s.sub.children.length;
  b.doc.fire('submit', un[0]);
  await flush();
  check('unvote: fetched with how=un in the url', b.win.fetch.calls.length === 2 && b.win.fetch.calls[1].url === '/p/hn/vote/story/7?how=un' && un[0].prevented === 1);
  check('unvote: control hidden, arrow shown, cell not voted',
    un[0].className.indexOf('hn-gone') >= 0 && s.form.className.indexOf('hn-gone') < 0 && s.cell.className.indexOf('hn-voted') < 0);
  check('unvote: nothing removed, score untouched', s.sub.children.length === n0 && s.score.textContent === '98 points' && s.form.parentNode === s.cell);

  // 1c. vote again: the same control is reused, never created twice
  b.doc.fire('submit', s.form);
  await flush();
  check('revote: one unvote form, shown again', s.sub.querySelectorAll('form.hn-un').length === 1 && un[0].className.indexOf('hn-gone') < 0 && s.form.className.indexOf('hn-gone') >= 0);

  // 2. a server-rendered unvote with no hidden arrow posts natively
  s = storyRow();
  var sun = form('hn-act', '/p/hn/vote/story/7?how=un', [['next', '/news#7']]);
  s.sub.appendChild(sun);
  b = boot(s.root, mkFetch(function () { return ok({ points: 1, voted: false }); }));
  b.doc.fire('submit', sun);
  await flush();
  check('server unvote: not intercepted', b.win.fetch.calls.length === 0 && sun.prevented === 0);

  // 2b. a non-boolean voted keeps everything and falls back
  s = storyRow();
  b = boot(s.root, mkFetch(function () { return ok({ points: '5', voted: 'yes' }); }));
  b.doc.fire('submit', s.form);
  await flush();
  check('story: non-boolean voted keeps the form and the text', s.score.textContent === '98 points' && s.form.className.indexOf('hn-gone') < 0 && s.form.submitted === 1);

  // 3. comment vote success: arrows hidden, one visible unvote form, nothing removed
  c = commentCell();
  b = boot(c.root, mkFetch(replyVote));
  b.doc.fire('submit', c.up);
  await flush();
  var cun = c.cell.querySelectorAll('form.hn-un');
  check('comment: arrows kept and hidden', c.up.parentNode === c.cell && c.down.parentNode === c.cell && c.up.className.indexOf('hn-gone') >= 0 && c.down.className.indexOf('hn-gone') >= 0);
  check('comment: one visible unvote form', cun.length === 1 && cun[0].className.indexOf('hn-gone') < 0 && body2(cun[0]) === 'how=un' && cun[0].querySelector('button').textContent === 'unvote');
  check('comment: unvote clone is hn-un, not hn-vote-form (hn-classic hides .hn-vote-form button[title])', cun[0].className === 'hn-un' && c.cell.querySelectorAll('form.hn-vote-form').length === 2);
  check('comment: no submit()', c.up.submitted === 0 && c.down.submitted === 0);
  b.doc.fire('submit', cun[0]);
  await flush();
  check('comment unvote: arrows back, control hidden, forms kept',
    c.up.className.indexOf('hn-gone') < 0 && c.down.className.indexOf('hn-gone') < 0 && cun[0].className.indexOf('hn-gone') >= 0 && c.cell.children.length === 3 && c.cell.className.indexOf('hn-voted') < 0);

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

  // 10b. only /p/hn/vote/ forms are intercepted
  ['https://evil.example/p/hn/vote/story/7', '//evil.example/p/hn/vote/story/7', '/other/p/hn/vote/story/7', '/p/hn/votes/7', ''].forEach(function (action) {
    s = storyRow();
    s.form.attrs.action = action;
    b = boot(s.root, mkFetch(function () { return ok({ voted: true }); }));
    b.doc.fire('submit', s.form);
    check('foreign action ' + JSON.stringify(action) + ': no fetch, no preventDefault', b.win.fetch.calls.length === 0 && s.form.prevented === 0 && s.form.parentNode === s.cell);
  });

  // 11. double submit in flight
  s = storyRow();
  b = boot(s.root, mkFetch(function () { return ok({ points: 2, voted: true }); }));
  b.doc.fire('submit', s.form);
  b.doc.fire('submit', s.form);
  await flush();
  check('double submit: one fetch', b.win.fetch.calls.length === 1);
  check('double submit: both submits prevented', s.form.prevented === 2 && s.form.submitted === 0);

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

  var sec = el('section', 'hm-section');
  ub = el('div', 'hn-ub');
  sec.appendChild(ub);
  slot = el('div', 'hn-userbar', {}, [sec]);
  root = el('main', '', {}, [el('header', '', {}, [slot])]);
  b = boot(root, mkFetch(function () { return ok({}); }));
  check('userbar: already in the header slot, not moved', ub.parentNode === sec && sec.parentNode === slot && slot.children.length === 1);

  // 14b. submit text: the one-line input becomes a textarea (this file loads only with site.scripts)
  var input = el('input', '', { id: 'hn-text', type: 'text', name: 'text', size: '50' });
  input.value = 'draft';
  var td = el('td', '', {}, [input]);
  b = boot(el('main', '', {}, [el('form', '', {}, [td])]), undefined);
  var ta = td.children[0];
  check('submit text: input replaced by a textarea', input.parentNode === null && td.children.length === 1 && ta.tagName === 'textarea');
  check('submit text: id, name, rows, cols, value kept', ta.attrs.id === 'hn-text' && ta.attrs.name === 'text' && ta.attrs.rows === '4' && ta.attrs.cols === '49' && ta.value === 'draft');
  td = el('td', '', {}, [el('input', '', { id: 'hn-title', type: 'text', name: 'title' })]);
  b = boot(el('main', '', {}, [td]), undefined);
  check('submit text: other inputs untouched', td.children[0].tagName === 'input');

  // 14c. reply text: the one-line input becomes a textarea keeping the rotated name
  var rin = el('input', '', { id: 'hn-reply-text', type: 'text', name: 'f_abc', 'data-hn-reply': '', maxlength: '5000', required: '' });
  rin.value = 'draft reply';
  var rp = el('p', '', {}, [rin]);
  b = boot(el('main', '', {}, [el('form', '', {}, [rp])]), undefined);
  var rta = rp.children[0];
  check('reply text: input replaced by a textarea', rin.parentNode === null && rp.children.length === 1 && rta.tagName === 'textarea');
  check('reply text: id, name, rows, cols, maxlength, value kept', rta.attrs.id === 'hn-reply-text' && rta.attrs.name === 'f_abc' && rta.attrs.rows === '6' && rta.attrs.cols === '60' && rta.attrs.maxlength === '5000' && rta.attrs.required === '' && rta.value === 'draft reply');
  var sin = el('input', '', { id: 'hn-text', type: 'text', name: 'text' });
  rin = el('input', '', { id: 'hn-reply-text', type: 'text', name: 'f_x', 'data-hn-reply': '' });
  var both = el('main', '', {}, [el('p', '', {}, [sin]), el('p', '', {}, [rin])]);
  b = boot(both, undefined);
  check('reply text: both inputs on one page are swapped', both.children[0].children[0].tagName === 'textarea' && both.children[1].children[0].tagName === 'textarea' && both.children[1].children[0].attrs.required === undefined);
  // 14d. profile about: the one-line input becomes a textarea
  var ain = el('input', '', { id: 'hn-about', type: 'text', name: 'about' });
  ain.value = 'hello';
  var ap = el('p', '', {}, [ain]);
  b = boot(el('main', '', {}, [ap]), undefined);
  var ata = ap.children[0];
  check('about: swapped, name, rows, maxlength 2000, value kept', ain.parentNode === null && ata.tagName === 'textarea' && ata.attrs.name === 'about' && ata.attrs.rows === '5' && ata.attrs.maxlength === '2000' && ata.value === 'hello');
  // value attribute keeps line breaks that a text input's value strips
  var nin = el('input', '', { id: 'hn-about', type: 'text', name: 'about', value: 'a\nb' });
  nin.value = 'ab';
  var np = el('p', '', {}, [nin]);
  b = boot(el('main', '', {}, [np]), undefined);
  check('about: newlines from the value attribute survive', np.children[0].value === 'a\nb');
  rp = el('p', '', {}, [el('input', '', { id: 'q', type: 'text', name: 'q' })]);
  b = boot(el('main', '', {}, [rp]), undefined);
  check('reply text: a page without it is untouched', rp.children[0].tagName === 'input');

  // 16-21. viewer state
  var EID = '10000000-0000-4000-8000-000000000001';
  function page(entity, withCells) {
    var p = { cells: {}, flags: {}, vouches: {} };
    var kids = [];
    ['c1', 'c2', 'c3', 'c4', 'c5'].forEach(function (id) {
      var up = form('hn-vote-form', '/p/hn/vote/comment/' + id, [['how', 'up']]);
      var down = form('hn-vote-form', '/p/hn/vote/comment/' + id, [['how', 'down']]);
      var cell = el('span', 'hn-vote', { 'data-hn-comment': id }, [up, down]);
      var head = el('div', 'hn-head', {}, [cell]);
      p.cells[id] = { cell: cell, up: up, down: down, head: head };
      if (id !== 'c5') {
        var fl = form('hn-inline', '/p/hn/flag/comment/' + id, [['next', '/']]);
        head.appendChild(new Text(' | '));
        head.appendChild(fl);
        p.flags[id] = fl;
      }
      if (id === 'c3' || id === 'c1') {
        var vo = form('hn-inline', '/p/hn/vouch/comment/' + id, [['next', '/']]);
        head.appendChild(new Text(' | '));
        head.appendChild(vo);
        p.vouches[id] = vo;
      }
      kids.push(head);
    });
    var attrs = entity === null ? {} : { 'data-hm-entity': entity };
    p.root = el('main', '', attrs, withCells === false ? [] : kids);
    return p;
  }
  var STATE = {
    viewer: { karma: 40, flag: true, downvote: false, vouch: true },
    story: { voted: false },
    comments: {
      c1: { voted: 'up', own: false, dead: false },
      c2: { voted: '', own: true, dead: false },
      c3: { voted: '', own: false, dead: true },
      c4: { voted: '', own: false, dead: false }
    }
  };
  function stateFetch(json) { return mkFetch(function () { return ok(JSON.parse(JSON.stringify(json))); }); }
  function texts(head) { return head.children.filter(function (k) { return k.nodeType === 3; }).length; }

  var pg = page(EID);
  b = boot(pg.root, stateFetch(STATE));
  await flush();
  call = b.win.fetch.calls[0];
  check('state: one fetch to the state route', b.win.fetch.calls.length === 1 && call.url === '/p/hn/state/' + EID);
  check('state: Accept json, same-origin, GET', call.init.headers.Accept === 'application/json' && call.init.credentials === 'same-origin' && !call.init.method);
  check('state: voted/own/dead cells lose their vote forms', ['c1', 'c2', 'c3'].every(function (id) { return pg.cells[id].cell.children.length === 0; }));
  check('state: voted cell marked', pg.cells.c1.cell.className.indexOf('hn-voted') >= 0 && pg.cells.c2.cell.className.indexOf('hn-voted') < 0);
  check('state: live and unknown keep up, lose down', ['c4', 'c5'].every(function (id) {
    return pg.cells[id].up.parentNode === pg.cells[id].cell && pg.cells[id].down.parentNode === null;
  }));
  check('state: own flag and its separator removed', pg.flags.c2.parentNode === null && texts(pg.cells.c2.head) === 0);
  check('state: other flags kept', ['c1', 'c3', 'c4'].every(function (id) { return pg.flags[id].parentNode === pg.cells[id].head && texts(pg.cells[id].head) >= 1; }));
  check('state: vouch of a live comment removed, dead kept', pg.vouches.c1.parentNode === null && pg.vouches.c3.parentNode === pg.cells.c3.head);

  pg = page(EID);
  var vo5 = form('hn-inline', '/p/hn/vouch/comment/c5', [['next', '/']]);
  pg.cells.c5.cell.parentNode.appendChild(new Text(' | '));
  pg.cells.c5.cell.parentNode.appendChild(vo5);
  b = boot(pg.root, stateFetch(STATE));
  await flush();
  check('state: vouch of a comment missing from state kept', vo5.parentNode === pg.cells.c5.head && texts(pg.cells.c5.head) === 1);

  pg = page(EID);
  var unvote = form('hn-vote-form', '/p/hn/vote/comment/c1', [['how', 'un']]);
  pg.cells.c1.cell.appendChild(unvote);
  b = boot(pg.root, stateFetch(STATE));
  await flush();
  check('state: unvote form kept in a voted cell, up/down removed', unvote.parentNode === pg.cells.c1.cell && pg.cells.c1.cell.children.length === 1);

  pg = page(EID);
  var noRights = JSON.parse(JSON.stringify(STATE));
  noRights.viewer.flag = false;
  noRights.viewer.vouch = false;
  noRights.viewer.downvote = true;
  b = boot(pg.root, stateFetch(noRights));
  await flush();
  check('state: no flag or vouch right removes every form', Object.keys(pg.flags).every(function (id) { return pg.flags[id].parentNode === null; }) &&
    Object.keys(pg.vouches).every(function (id) { return pg.vouches[id].parentNode === null; }));
  check('state: downvote right keeps down forms', pg.cells.c4.down.parentNode === pg.cells.c4.cell);

  pg = page(EID);
  b = boot(pg.root, stateFetch({}));
  await flush();
  check('state {}: nothing removed', pg.cells.c1.up.parentNode === pg.cells.c1.cell && pg.flags.c2.parentNode !== null && pg.vouches.c1.parentNode !== null && texts(pg.cells.c2.head) === 1);

  var failing = [
    ['500', function () { return status(500); }],
    ['network error', function () { return Promise.reject(new Error('net')); }],
    ['non-JSON', function () { return Promise.resolve({ ok: true, json: function () { return Promise.reject(new Error('json')); } }); }],
    ['sync throw', function () { throw new Error('boom'); }]
  ];
  for (i = 0; i < failing.length; i++) {
    pg = page(EID);
    threw = false;
    try { b = boot(pg.root, mkFetch(failing[i][1])); } catch (e) { threw = true; }
    await flush();
    check('state ' + failing[i][0] + ': nothing removed, no throw, no submit()',
      !threw && pg.cells.c1.up.parentNode === pg.cells.c1.cell && pg.flags.c2.parentNode !== null && pg.cells.c1.up.submitted === 0);
  }

  pg = page(EID, false);
  b = boot(pg.root, stateFetch(STATE));
  await flush();
  check('state: no comment cells, no fetch', b.win.fetch.calls.length === 0);
  ['', null, '../x', 'not-a-uuid'].forEach(function (bad) {
    pg = page(bad);
    b = boot(pg.root, stateFetch(STATE));
    check('state: entity ' + JSON.stringify(bad) + ', no fetch', b.win.fetch.calls.length === 0);
  });

  // 15. static checks on the source
  ['eval', 'Function(', 'innerHTML', 'cookie', 'localStorage', 'sessionStorage', 'http:', 'https:', '=>', 'let ', 'const ', 'document.write']
    .forEach(function (bannedText) { check('source has no ' + bannedText, src.indexOf(bannedText) < 0); });
  check('source has no inline handler strings', !/\bon[a-z]+\s*=/.test(src));
  // This plugin's own size budget, not a core limit.
  check('source < 8192 bytes (' + Buffer.byteLength(src) + ')', Buffer.byteLength(src) < 8192);
  check('assets holds only plugin.js', fs.readdirSync(path.join(__dirname, 'assets')).join() === 'plugin.js');
}

main().then(function () {
  if (failures) { console.error(failures + ' check(s) failed'); process.exit(1); }
}, function (e) { console.error(e); process.exit(1); });
