// Runs assets/plugin.js against a minimal DOM stub. No dependencies:
//   node popups_test.js
// Lives outside assets/ because everything under assets/ is served publicly.
'use strict';
var assert = require('assert');
var fs = require('fs');
var path = require('path');

var srcPath = path.join(__dirname, 'assets', 'plugin.js');
var src = fs.readFileSync(srcPath, 'utf8');
var p = require(srcPath);
var failures = 0;

function test(name, fn) {
  try { fn(); console.log('ok   ' + name); } catch (e) { failures++; console.log('FAIL ' + name + ': ' + e.message); }
}

// ---- DOM stub ----
function element(tag) {
  var listeners = {};
  var el = {
    tag: tag, children: [], parentNode: null, attrs: {}, className: '', _text: '',
    setAttribute: function (k, v) { this.attrs[k] = v; },
    getAttribute: function (k) { return k in this.attrs ? this.attrs[k] : null; },
    appendChild: function (c) { c.parentNode = this; this.children.push(c); return c; },
    removeChild: function (c) { this.children.splice(this.children.indexOf(c), 1); c.parentNode = null; },
    addEventListener: function (n, f) { (listeners[n] = listeners[n] || []).push(f); },
    emit: function (n, e) { (listeners[n] || []).forEach(function (f) { f(e || {}); }); },
    listenerCount: function (n) { return (listeners[n] || []).length; },
    focus: function () { el.ownerDoc.activeElement = el; },
    ownerDoc: null,
  };
  Object.defineProperty(el, 'textContent', {
    get: function () { return el._text; }, set: function (v) { el._text = String(v); },
  });
  Object.defineProperty(el, 'innerHTML', { set: function () { throw new Error('innerHTML used'); } });
  if (tag === 'dialog') {
    el.showModal = function () { el.open = true; };
    el.close = function () { el.open = false; el.emit('close'); };
  }
  return el;
}
function find(el, pred, out) {
  out = out || [];
  if (pred(el)) out.push(el);
  el.children.forEach(function (c) { find(c, pred, out); });
  return out;
}

function storage(throwing) {
  var data = {};
  return {
    data: data,
    getItem: function (k) { if (throwing) throw new Error('denied'); return k in data ? data[k] : null; },
    setItem: function (k, v) { if (throwing) throw new Error('denied'); data[k] = String(v); },
  };
}

// opts: campaigns, raw (meta content), path, session, local, hover, noDialog, scroll {y, h, vh}
function setup(opts) {
  var body = element('body');
  var doc = {
    body: body, activeElement: null, listeners: {},
    documentElement: { scrollHeight: (opts.scroll && opts.scroll.h) || 1000 },
    createElement: function (t) {
      var e = element(t);
      e.ownerDoc = doc;
      if (t === 'dialog' && opts.noDialog) delete e.showModal;
      return e;
    },
    querySelector: function (sel) {
      assert.strictEqual(sel, 'meta[name="hatamoto-popups"]');
      var raw = opts.raw !== undefined ? opts.raw : (opts.campaigns ? JSON.stringify(opts.campaigns) : null);
      return raw === null ? null : { getAttribute: function (k) { return k === 'content' ? raw : null; } };
    },
    addEventListener: function (n, f) { (doc.listeners[n] = doc.listeners[n] || []).push(f); },
  };
  body.ownerDoc = doc;
  var timers = [];
  var winListeners = {};
  var win = {
    location: { pathname: opts.path || '/' },
    sessionStorage: opts.session || storage(),
    localStorage: opts.local || storage(),
    pageYOffset: (opts.scroll && opts.scroll.y) || 0,
    innerHeight: (opts.scroll && opts.scroll.vh) || 500,
    matchMedia: function () { return { matches: opts.hover !== false }; },
    setTimeout: function (f, ms) { timers.push({ f: f, ms: ms }); },
    addEventListener: function (n, f, o) { (winListeners[n] = winListeners[n] || []).push({ f: f, o: o }); },
  };
  return { doc: doc, win: win, body: body, timers: timers, winListeners: winListeners };
}
var bar = { id: 'b1', kind: 'bar', message: 'Spring <b>sale</b>', link_label: 'Plans', link_url: '/pricing', frequency: 'session', position: 'top' };
var pop = { id: 'p1', kind: 'popup', message: 'Webinar', frequency: 'day', trigger: 'load' };
function dialogs(s) { return find(s.body, function (e) { return e.tag === 'dialog'; }); }
function bars(s) { return find(s.body, function (e) { return e.className.indexOf('hm-popups-bar') >= 0; }); }

// ---- pure functions ----
test('matchPath globs', function () {
  assert.ok(p.matchPath('/anything', [], []));
  assert.ok(p.matchPath('/blog/a/b', ['/blog/**'], []));
  assert.ok(p.matchPath('/blog', ['/blog/**'], []));
  assert.ok(!p.matchPath('/blogger', ['/blog/**'], []));
  assert.ok(p.matchPath('/blog/a', ['/blog/*'], []));
  assert.ok(!p.matchPath('/blog/a/b', ['/blog/*'], []));
  assert.ok(!p.matchPath('/cart', [], ['/cart']));
  assert.ok(!p.matchPath('/checkout/x', ['/**'], ['/checkout/**']));
  assert.ok(p.matchPath('/a.b', ['/a.b'], []));
  assert.ok(!p.matchPath('/axb', ['/a.b'], []));
});

test('parseUTC', function () {
  assert.strictEqual(p.parseUTC('', false), null);
  assert.strictEqual(p.parseUTC('2026-11-04', false), Date.UTC(2026, 10, 4, 0, 0, 0));
  assert.strictEqual(p.parseUTC('2026-11-04', true), Date.UTC(2026, 10, 4, 23, 59, 59));
  assert.strictEqual(p.parseUTC('2026-11-04 18:30', true), Date.UTC(2026, 10, 4, 18, 30, 0));
  assert.strictEqual(p.parseUTC('2026-11-04T18:30', false), Date.UTC(2026, 10, 4, 18, 30, 0));
  assert.ok(isNaN(p.parseUTC('2026-02-30', false)));
  assert.ok(isNaN(p.parseUTC('tomorrow', false)));
});

test('inWindow', function () {
  var c = { start: '2026-11-04', end: '2026-11-08' };
  assert.ok(!p.inWindow(c, Date.UTC(2026, 10, 3, 23, 59, 59)));
  assert.ok(p.inWindow(c, Date.UTC(2026, 10, 4)));
  assert.ok(p.inWindow(c, Date.UTC(2026, 10, 8, 23, 59, 59)));
  assert.ok(!p.inWindow(c, Date.UTC(2026, 10, 9)));
  assert.ok(p.inWindow({}, 5));
  assert.ok(!p.inWindow({ end: 'junk' }, 5));
});

test('safeLink', function () {
  ['https://example.org/x', 'http://example.org', '/pricing', '/a?b=1'].forEach(function (u) { assert.ok(p.safeLink(u), u); });
  ['javascript:alert(1)', 'JavaScript:x', '//evil.example', '/\\evil', 'data:text/html,x', 'ftp://x', 'pricing', ''].forEach(function (u) {
    assert.ok(!p.safeLink(u), u);
  });
});

test('frequency session/day/week/once', function () {
  var now = Date.UTC(2026, 10, 10);
  var st = { session: storage(), local: storage() };
  var day = 86400000;
  ['session', 'day', 'week', 'once'].forEach(function (f) {
    var c = { id: 'x-' + f, frequency: f };
    assert.ok(p.shouldShow(c, st, now), f + ' first view');
    p.recordDismissal(c, st, now);
  });
  assert.ok('hm-popups:x-session' in st.session.data && !('hm-popups:x-session' in st.local.data));
  assert.ok('hm-popups:x-once' in st.local.data);
  assert.ok(!p.shouldShow({ id: 'x-session', frequency: 'session' }, st, now));
  assert.ok(!p.shouldShow({ id: 'x-once', frequency: 'once' }, st, now + 400 * day));
  assert.ok(!p.shouldShow({ id: 'x-day', frequency: 'day' }, st, now + day - 1));
  assert.ok(p.shouldShow({ id: 'x-day', frequency: 'day' }, st, now + day));
  assert.ok(!p.shouldShow({ id: 'x-week', frequency: 'week' }, st, now + 6 * day));
  assert.ok(p.shouldShow({ id: 'x-week', frequency: 'week' }, st, now + 7 * day));
  assert.ok(p.shouldShow({ id: 'other', frequency: 'once' }, st, now));
});

test('throwing storage shows and never throws', function () {
  var st = { session: storage(true), local: storage(true) };
  ['session', 'day', 'week', 'once'].forEach(function (f) {
    var c = { id: 'x', frequency: f };
    assert.ok(p.shouldShow(c, st, 1), f);
    p.recordDismissal(c, st, 1);
  });
  assert.ok(p.shouldShow({ id: 'x', frequency: 'once' }, { session: null, local: null }, 1));
  var s = setup({ campaigns: [bar], session: storage(true), local: storage(true) });
  p.run(s.win, s.doc);
  assert.strictEqual(bars(s).length, 1);
});

// ---- DOM ----
test('bar: region, textContent, safe link, dismiss stays dismissed', function () {
  var s = setup({ campaigns: [bar] });
  p.run(s.win, s.doc);
  var b = bars(s);
  assert.strictEqual(b.length, 1);
  assert.strictEqual(b[0].attrs.role, 'region');
  assert.strictEqual(b[0].attrs['aria-label'], 'Announcement');
  assert.ok(b[0].className.indexOf('hm-popups-top') >= 0);
  var msg = find(b[0], function (e) { return e.className === 'hm-popups-message'; })[0];
  assert.strictEqual(msg.textContent, 'Spring <b>sale</b>');
  var a = find(b[0], function (e) { return e.tag === 'a'; })[0];
  assert.strictEqual(a.attrs.href, '/pricing');
  assert.strictEqual(a.textContent, 'Plans');
  var close = find(b[0], function (e) { return e.tag === 'button'; })[0];
  assert.strictEqual(close.attrs['aria-label'], 'Close');
  close.emit('click');
  assert.strictEqual(bars(s).length, 0);
  assert.ok('hm-popups:b1' in s.win.sessionStorage.data);
  var again = setup({ campaigns: [bar], session: s.win.sessionStorage });
  p.run(again.win, again.doc);
  assert.strictEqual(bars(again).length, 0);
});

test('bar: clicking the link records the dismissal', function () {
  var s = setup({ campaigns: [bar] });
  p.run(s.win, s.doc);
  var a = find(bars(s)[0], function (e) { return e.tag === 'a'; })[0];
  assert.ok(!('hm-popups:b1' in s.win.sessionStorage.data));
  a.emit('click');
  assert.ok('hm-popups:b1' in s.win.sessionStorage.data);
});

test('popup: clicking the link records the dismissal', function () {
  var s = setup({ campaigns: [Object.assign({}, pop, { link_label: 'Go', link_url: '/x' })] });
  p.run(s.win, s.doc);
  var a = find(dialogs(s)[0], function (e) { return e.tag === 'a'; })[0];
  a.emit('click');
  assert.ok('hm-popups:p1' in s.win.localStorage.data);
});

test('bar: unsafe link is not rendered', function () {
  var s = setup({ campaigns: [Object.assign({}, bar, { link_url: 'javascript:alert(1)' })] });
  p.run(s.win, s.doc);
  assert.strictEqual(find(s.body, function (e) { return e.tag === 'a'; }).length, 0);
});

test('one bar per position, one popup', function () {
  var s = setup({ campaigns: [
    bar, Object.assign({}, bar, { id: 'b2' }),
    Object.assign({}, bar, { id: 'b3', position: 'bottom' }),
    pop, Object.assign({}, pop, { id: 'p2' }),
  ] });
  p.run(s.win, s.doc);
  assert.strictEqual(bars(s).length, 2);
  assert.strictEqual(dialogs(s).length, 1);
  assert.strictEqual(dialogs(s)[0].open, true);
});

test('filters by path, window and bad input', function () {
  var now = Date.now();
  var past = new Date(now - 5 * 86400000).toISOString().slice(0, 10);
  var s = setup({ path: '/cart', campaigns: [
    Object.assign({}, bar, { id: 'a', exclude_paths: ['/cart'] }),
    Object.assign({}, bar, { id: 'b', end: past }),
    Object.assign({}, bar, { id: 'c', position: 'bottom', start: '2999-01-01' }),
    { id: 'd', kind: 'bar' },
    null,
  ] });
  p.run(s.win, s.doc);
  assert.strictEqual(bars(s).length, 0);
  ['not json', '{"a":1}', ''].forEach(function (raw) {
    var t = setup({ raw: raw });
    p.run(t.win, t.doc);
    assert.strictEqual(t.body.children.length, 0);
  });
  var none = setup({});
  p.run(none.win, none.doc);
  assert.strictEqual(none.body.children.length, 0);
});

test('popup: showModal, close button, focus restored, dismissal recorded', function () {
  var s = setup({ campaigns: [Object.assign({}, pop, { link_label: 'Go', link_url: 'https://example.org' })] });
  var opener = s.doc.createElement('button');
  opener.focus();
  p.run(s.win, s.doc);
  var d = dialogs(s)[0];
  assert.strictEqual(d.open, true);
  var close = find(d, function (e) { return e.tag === 'button'; })[0];
  assert.strictEqual(close.attrs['aria-label'], 'Close');
  s.doc.activeElement = null;
  close.emit('click');
  assert.strictEqual(d.open, false);
  assert.strictEqual(s.doc.activeElement, opener);
  assert.strictEqual(dialogs(s).length, 0);
  assert.ok('hm-popups:p1' in s.win.localStorage.data);
});

test('popup: Esc (native close) restores focus and records', function () {
  var s = setup({ campaigns: [pop] });
  var opener = s.doc.createElement('a');
  opener.focus();
  p.run(s.win, s.doc);
  var d = dialogs(s)[0];
  s.doc.activeElement = null;
  d.emit('close'); // the browser fires close after Esc
  assert.strictEqual(s.doc.activeElement, opener);
  assert.ok('hm-popups:p1' in s.win.localStorage.data);
});

test('popup without dialog support is skipped', function () {
  var s = setup({ campaigns: [pop], noDialog: true });
  p.run(s.win, s.doc);
  assert.strictEqual(s.body.children.length, 0);
});

test('trigger delay', function () {
  var s = setup({ campaigns: [Object.assign({}, pop, { trigger: 'delay', delay_seconds: 5 })] });
  p.run(s.win, s.doc);
  assert.strictEqual(dialogs(s).length, 0);
  assert.strictEqual(s.timers[0].ms, 5000);
  s.timers[0].f();
  assert.strictEqual(dialogs(s).length, 1);
});

test('trigger scroll', function () {
  var s = setup({ campaigns: [Object.assign({}, pop, { trigger: 'scroll', scroll_percent: 80 })], scroll: { y: 0, h: 2000, vh: 500 } });
  p.run(s.win, s.doc);
  var l = s.winListeners.scroll[0];
  assert.deepStrictEqual(l.o, { passive: true });
  l.f();
  assert.strictEqual(dialogs(s).length, 0);
  s.win.pageYOffset = 1100;
  l.f();
  l.f();
  assert.strictEqual(dialogs(s).length, 1);
});

test('trigger exit: desktop only, top edge only, once', function () {
  var s = setup({ campaigns: [Object.assign({}, pop, { trigger: 'exit' })] });
  p.run(s.win, s.doc);
  var f = s.doc.listeners.mouseout[0];
  f({ clientY: 300, relatedTarget: null });
  f({ clientY: 0, relatedTarget: {} });
  assert.strictEqual(dialogs(s).length, 0);
  f({ clientY: -1, relatedTarget: null });
  f({ clientY: 0, relatedTarget: null });
  assert.strictEqual(dialogs(s).length, 1);
  var t = setup({ campaigns: [Object.assign({}, pop, { trigger: 'exit' })], hover: false });
  p.run(t.win, t.doc);
  assert.ok(!t.doc.listeners.mouseout);
  assert.strictEqual(dialogs(t).length, 0);
});

test('bars ignore the trigger', function () {
  var s = setup({ campaigns: [Object.assign({}, bar, { trigger: 'exit' })] });
  p.run(s.win, s.doc);
  assert.strictEqual(bars(s).length, 1);
});

test('popup that cannot show here does not take the popup slot', function () {
  var exit = Object.assign({}, pop, { id: 'x1', trigger: 'exit' });
  var touch = setup({ campaigns: [exit, pop], hover: false });
  p.run(touch.win, touch.doc);
  assert.strictEqual(dialogs(touch).length, 1);
  assert.strictEqual(dialogs(touch)[0].open, true);
  var nodlg = setup({ campaigns: [pop, bar], noDialog: true });
  p.run(nodlg.win, nodlg.doc);
  assert.strictEqual(bars(nodlg).length, 1);
  assert.strictEqual(dialogs(nodlg).length, 0);
});

test('globs without a leading slash are normalised', function () {
  assert.ok(p.matchPath('/blog/a', ['blog/**'], []));
  assert.ok(!p.matchPath('/cart', [], ['cart']) );
  assert.ok(p.matchPath('/x', ['*'], []));
});

test('safeLink rejects urls over 200 characters', function () {
  assert.ok(p.safeLink('/' + 'a'.repeat(199)));
  assert.ok(!p.safeLink('/' + 'a'.repeat(200)));
});

test('plugin.json id pattern is valid under the v flag', function () {
  var schema = JSON.parse(fs.readFileSync(path.join(__dirname, 'plugin.json'), 'utf8'));
  var item = JSON.stringify(schema).match(/"pattern":"(\^\[a-z0-9\][^"]*)"/);
  var re = new RegExp(JSON.parse('"' + item[1] + '"'), 'v');
  assert.ok(re.test('spring-sale-1'));
  assert.ok(!re.test('-x') && !re.test('Bad') && !re.test('a'.repeat(41)));
});

test('run never throws', function () {
  p.run({}, {});
  p.run(null, null);
});

test('source has no inline handlers, eval or innerHTML', function () {
  ['onclick', 'onload', 'eval(', 'innerHTML', 'document.write', 'insertAdjacentHTML'].forEach(function (bad) {
    assert.ok(src.indexOf(bad) < 0, bad);
  });
});

test('plugin.css has no animation or transition', function () {
  var css = fs.readFileSync(path.join(__dirname, 'assets', 'plugin.css'), 'utf8');
  var rules = css.replace(/\/\*[\s\S]*?\*\//g, '').replace(/@media \(prefers-reduced-motion: reduce\)[\s\S]*$/, '');
  assert.ok(!/animation|transition/.test(rules));
  assert.ok(/prefers-reduced-motion: reduce/.test(css));
  assert.ok(/\.hm-popups-bar\s*\{[^}]*position:\s*fixed/.test(css));
});

if (failures) { console.log(failures + ' failed'); process.exit(1); }
console.log('all passed');
