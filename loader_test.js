// Runs assets/plugin.js against a minimal DOM stub. No dependencies:
//   node loader_test.js
// Lives outside assets/ because everything under assets/ is served publicly.
'use strict';
var fs = require('fs');
var path = require('path');
var vm = require('vm');

var src = fs.readFileSync(path.join(__dirname, 'assets', 'plugin.js'), 'utf8');
var failures = 0;

// opts: meta (string|undefined), preset (bool), existing (bool).
function run(opts) {
  var scripts = [];
  var window = {};
  if (opts.preset) window.google_tag_manager = { 'GTM-ABC1234': {} };
  var parent = {
    insertBefore: function (el, ref) { scripts.splice(scripts.indexOf(ref), 0, el); },
  };
  if (opts.existing) scripts.push({ src: 'https://www.googletagmanager.com/gtm.js?id=GTM-ABC1234', parentNode: parent });
  var document = {
    querySelector: function (sel) {
      if (sel === 'meta[name="gtm-container"]') {
        return opts.meta === undefined ? null : { getAttribute: function () { return opts.meta; } };
      }
      var m = /^script\[src\*="(.*)"\]$/.exec(sel);
      if (!m) throw new Error('unexpected selector: ' + sel);
      return scripts.filter(function (s) { return s.src.indexOf(m[1]) >= 0; })[0] || null;
    },
    createElement: function () { return {}; },
    getElementsByTagName: function () { return scripts.length ? [scripts[0]] : []; },
    head: { appendChild: function (el) { scripts.push(el); } },
  };
  window.document = document;
  var ctx = vm.createContext({ window: window, document: document, encodeURIComponent: encodeURIComponent });
  vm.runInContext(src, ctx);
  vm.runInContext(src, ctx); // idempotent: a second run must change nothing
  return {
    gtm: scripts.filter(function (s) { return /googletagmanager\.com\/gtm\.js\?id=/.test(s.src); }),
    layer: window.dataLayer || [],
  };
}

function check(name, cond) {
  if (!cond) { failures++; console.error('FAIL: ' + name); } else console.log('ok: ' + name);
}

function loads(name, opts, id) {
  var r = run(opts);
  var starts = r.layer.filter(function (e) { return e.event === 'gtm.js'; });
  check(name + ': one gtm.js script', r.gtm.length === 1 && r.gtm[0].src === 'https://www.googletagmanager.com/gtm.js?id=' + id);
  check(name + ': one dataLayer gtm.js push', r.layer.length === 1 && starts.length === 1);
}

function nothing(name, opts) {
  var r = run(opts);
  var added = r.gtm.length - (opts.existing ? 1 : 0);
  check(name + ': nothing loaded', added === 0 && r.layer.length === 0);
}

loads('valid id', { meta: 'GTM-ABC1234' }, 'GTM-ABC1234');
loads('lowercase padded id', { meta: ' \n gtm-abc1234 ' }, 'GTM-ABC1234');
nothing('no meta', {});
nothing('empty meta', { meta: '' });
nothing('invalid id', { meta: 'nonsense' });
nothing('too short id', { meta: 'GTM-AB' });
nothing('injection attempt', { meta: '"><x' });
nothing('google_tag_manager preset', { meta: 'GTM-ABC1234', preset: true });
nothing('existing script tag', { meta: 'GTM-ABC1234', existing: true });

if (failures) { console.error(failures + ' check(s) failed'); process.exit(1); }
