// Runs assets/plugin.js against a minimal DOM stub. No dependencies:
//   node loader_test.js
// Lives outside assets/ because everything under assets/ is served publicly.
'use strict';
var fs = require('fs');
var path = require('path');
var vm = require('vm');

var src = fs.readFileSync(path.join(__dirname, 'assets', 'plugin.js'), 'utf8');
var failures = 0;
var KEY = 'hatamoto-ga-exclude';

function element(tag) {
  var listeners = {};
  return {
    tag: tag,
    children: [],
    parentNode: null,
    appendChild: function (c) { c.parentNode = this; this.children.push(c); },
    removeChild: function (c) { this.children.splice(this.children.indexOf(c), 1); c.parentNode = null; },
    addEventListener: function (n, f) { listeners[n] = f; },
    click: function () { listeners.click(); },
  };
}

// opts: meta, hash, flag (stored value), throwStorage, existing, layer (preset dataLayer), gtag (preset gtag).
// The loader runs twice to prove it is idempotent.
function run(opts) {
  var scripts = [];
  var store = {};
  if (opts.flag) store[KEY] = opts.flag;
  var log = { replace: [], nav: [], reload: 0, listeners: [] };
  var window = {
    location: {
      hash: opts.hash || '', pathname: '/page', search: '?a=1',
      reload: function () { log.reload++; },
      replace: function (url) { log.nav.push(url); },
    },
    history: {
      replaceState: function (s, t, url) {
        if (opts.throwHistory) throw new Error('blocked');
        log.replace.push(url);
      },
    },
  };
  if (opts.throwStorage) {
    Object.defineProperty(window, 'localStorage', { get: function () { throw new Error('denied'); } });
  } else {
    window.localStorage = {
      getItem: function (k) { return k in store ? store[k] : null; },
      setItem: function (k, v) { store[k] = String(v); },
      removeItem: function (k) { delete store[k]; },
    };
  }
  if (opts.layer) window.dataLayer = opts.layer;
  if (opts.gtag) window.gtag = opts.gtag;
  var parent = {
    insertBefore: function (el, ref) { scripts.splice(scripts.indexOf(ref), 0, el); },
  };
  if (opts.existing) scripts.push({ src: 'https://www.googletagmanager.com/gtag/js?id=G-ABC123XYZ9', parentNode: parent });
  var body = element('body');
  var document = {
    querySelector: function (sel) {
      if (sel === 'meta[name="google-analytics-id"]') {
        return opts.meta === undefined ? null : { getAttribute: function () { return opts.meta; } };
      }
      var m = /^script\[src\*="(.*)"\]$/.exec(sel);
      if (!m) throw new Error('unexpected selector: ' + sel);
      return scripts.filter(function (s) { return s.src.indexOf(m[1]) >= 0; })[0] || null;
    },
    createElement: function (tag) { return element(tag); },
    getElementsByTagName: function () { return scripts.length ? [scripts[0]] : []; },
    head: { appendChild: function (el) { scripts.push(el); } },
    body: body,
  };
  window.document = document;
  var ctx = vm.createContext({ window: window, document: document, encodeURIComponent: encodeURIComponent });
  vm.runInContext(src, ctx);
  if (!opts.once) vm.runInContext(src, ctx);
  return {
    gtag: scripts.filter(function (s) { return /googletagmanager\.com\/gtag\/js\?id=/.test(s.src); }),
    layer: window.dataLayer || [],
    window: window, store: store, log: log, body: body,
  };
}

function check(name, cond) {
  if (!cond) { failures++; console.error('FAIL: ' + name); } else console.log('ok: ' + name);
}

function kinds(layer) {
  return layer.map(function (a) { return a[0]; }).join(',');
}

function loads(name, opts, id) {
  var r = run(opts);
  check(name + ': one gtag.js script',
    r.gtag.length === 1 && r.gtag[0].src === 'https://www.googletagmanager.com/gtag/js?id=' + id && r.gtag[0].async === true);
  check(name + ': one js and one config call',
    kinds(r.layer.filter(function (a) { return a[0] === 'js' || a[0] === 'config'; })) === 'js,config' &&
    r.layer[r.layer.length - 1][1] === id);
  return r;
}

function nothing(name, opts) {
  var r = run(opts);
  var added = r.gtag.length - (opts.existing ? 1 : 0);
  check(name + ': nothing loaded', added === 0 && r.layer.length === 0);
  return r;
}

loads('valid G id', { meta: 'G-ABC123XYZ9' }, 'G-ABC123XYZ9');
loads('lowercase padded id', { meta: ' \n g-abc123 ' }, 'G-ABC123');
loads('GT id', { meta: 'GT-ABCD12' }, 'GT-ABCD12');
nothing('no meta', {});
nothing('empty meta', { meta: '' });
nothing('UA id', { meta: 'UA-123-1' });
nothing('GTM id', { meta: 'GTM-ABC1' });
nothing('injection attempt', { meta: '"><x' });
nothing('existing script tag', { meta: 'G-ABC123XYZ9', existing: true });

// exclusion
var ex = run({ meta: 'G-ABC123XYZ9', hash: '#ga-exclude', once: true });
check('exclude: flag set', ex.store[KEY] === '1');
check('exclude: no script, no dataLayer', ex.gtag.length === 0 && ex.layer.length === 0);
check('exclude: ga-disable set', ex.window['ga-disable-G-ABC123XYZ9'] === true);
check('exclude: fragment cleared', ex.log.replace.length === 1 && ex.log.replace[0] === '/page?a=1');
check('exclude: notice shown', ex.body.children.length === 1 && ex.body.children[0].className === 'hm-ga-notice');
var bar = ex.body.children[0];
check('exclude: notice has text and two buttons',
  bar.children.length === 3 && bar.children[0].textContent === 'Google Analytics is off in this browser.' &&
  bar.children[1].textContent === 'Count it again');
bar.children[2].click();
check('exclude: close removes the notice', ex.body.children.length === 0);
bar.children[1].click();
check('exclude: count it again clears flag and reloads', !(KEY in ex.store) && ex.log.reload === 1);
nothing('exclude without valid id', { meta: 'nonsense', hash: '#ga-exclude' });
check('exclude without valid id: no flag', !(KEY in run({ meta: 'nonsense', hash: '#ga-exclude', once: true }).store));

// include
var inc = run({ meta: 'G-ABC123XYZ9', hash: '#ga-include', flag: '1', once: true });
check('include: flag removed', !(KEY in inc.store));
check('include: navigates once to the URL without fragment', inc.log.nav.length === 1 && inc.log.nav[0] === '/page?a=1' && inc.log.reload === 0);
check('include: no notice', inc.body.children.length === 0);

// flag set
var fl = nothing('flag set', { meta: 'G-ABC123XYZ9', flag: '1' });
check('flag set: ga-disable set', fl.window['ga-disable-G-ABC123XYZ9'] === true);
loads('flag with other value', { meta: 'G-ABC123XYZ9', flag: '0' }, 'G-ABC123XYZ9');

// coexistence with an existing dataLayer / gtag (e.g. the gtm plugin)
var layer = [{ event: 'gtm.js' }];
var kept = run({ meta: 'G-ABC123XYZ9', layer: layer });
check('existing dataLayer kept', kept.window.dataLayer === layer && layer[0].event === 'gtm.js' && layer.length === 3);
var called = 0;
var mine = function () { called++; };
var g = run({ meta: 'G-ABC123XYZ9', gtag: mine, once: true });
check('existing gtag not replaced', g.window.gtag === mine && called === 2);

// fail open
var ts = loads('throwing localStorage', { meta: 'G-ABC123XYZ9', throwStorage: true }, 'G-ABC123XYZ9');
var te = run({ meta: 'G-ABC123XYZ9', hash: '#ga-exclude', throwStorage: true, once: true });
check('throwing localStorage on exclude: still no script, notice shown', te.gtag.length === 0 && te.body.children.length === 1);
var th = run({ meta: 'G-ABC123XYZ9', hash: '#ga-exclude', throwHistory: true, once: true });
check('throwing replaceState: flag set, notice shown', th.store[KEY] === '1' && th.body.children.length === 1);
var ti = run({ meta: 'G-ABC123XYZ9', hash: '#ga-include', flag: '1', throwHistory: true, once: true });
check('include with throwing replaceState: one navigation, no fragment, flag removed',
  ti.log.nav.length === 1 && ti.log.nav[0].indexOf('#') < 0 && ti.log.reload === 0 && !(KEY in ti.store));

if (failures) { console.error(failures + ' check(s) failed'); process.exit(1); }
