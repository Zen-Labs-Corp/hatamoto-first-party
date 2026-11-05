// Runs assets/plugin.js against a minimal browser stub. No dependencies:
//   node plugin_test.js
// Lives outside assets/ because everything under assets/ is served publicly.
'use strict';
var fs = require('fs');
var path = require('path');
var vm = require('vm');

var src = fs.readFileSync(path.join(__dirname, 'assets', 'plugin.js'), 'utf8');
var failures = 0;
process.on('unhandledRejection', function () { failures++; console.error('FAIL: unhandled fetch rejection'); });

function check(name, cond) {
  if (!cond) { failures++; console.error('FAIL: ' + name); } else console.log('ok: ' + name);
}

var SID = '7f3c1d52-0000-4000-8000-00000000a91e';

// opts: consent (undefined = no banner, else {marketing:boolean}), search,
// cookie (initial document.cookie string), https (bool), fetchFails (bool).
function load(opts) {
  var env = { cookieReads: 0, cookieWrites: [], searchReads: 0, listeners: {}, fetches: [] };
  var jar = opts.cookie || '';
  var consent = opts.consent;
  var document = {
    addEventListener: function (type, fn) { (env.listeners[type] = env.listeners[type] || []).push(fn); },
  };
  Object.defineProperty(document, 'cookie', {
    get: function () { env.cookieReads++; return jar; },
    set: function (v) { env.cookieWrites.push(v); jar = (jar ? jar + '; ' : '') + v.split(';')[0]; },
  });
  var location = { protocol: opts.https ? 'https:' : 'http:' };
  Object.defineProperty(location, 'search', { get: function () { env.searchReads++; return opts.search || ''; } });
  var window = {
    document: document,
    location: location,
    fetch: function (url, init) {
      env.fetches.push({ url: url, init: init });
      return opts.fetchFails ? Promise.reject(new Error('down')) : Promise.resolve({});
    },
  };
  if (consent) window.hm = { consent: { allows: function (k) { return k === 'marketing' && consent.marketing; } } };
  var ctx = vm.createContext({ window: window, document: document, JSON: JSON, encodeURIComponent: encodeURIComponent, decodeURIComponent: decodeURIComponent });
  vm.runInContext(src, ctx);
  env.consent = consent;
  env.fire = function (type, detail) { (env.listeners[type] || []).forEach(function (fn) { fn({ type: type, detail: detail }); }); };
  env.cookieValue = function () {
    var w = env.cookieWrites[env.cookieWrites.length - 1];
    return w ? JSON.parse(decodeURIComponent(w.split(';')[0].slice('hm_adc='.length))) : null;
  };
  return env;
}

// Before consent: nothing is read or set, and only hm:consent is listened to.
var e = load({ consent: { marketing: false }, search: '?gclid=abc', cookie: 'li_fat_id=x' });
check('before consent: no cookie read', e.cookieReads === 0);
check('before consent: no cookie written', e.cookieWrites.length === 0);
check('before consent: address not read', e.searchReads === 0);
check('before consent: only hm:consent is listened to', Object.keys(e.listeners).join() === 'hm:consent');
e.fire('hm:consent', {});
check('a consent event that is still no changes nothing', e.cookieReads === 0 && e.cookieWrites.length === 0 && !e.listeners['hm:conversion']);
e.consent.marketing = true;
e.fire('hm:consent', { marketing: true });
check('consent turning true starts the script', e.cookieValue() && e.cookieValue().gclid === 'abc' && e.cookieValue().li_fat_id === 'x');
check('after consent the conversion listener is set', e.listeners['hm:conversion'].length === 1);
e.fire('hm:consent', { marketing: true });
check('a second consent event does not start twice', e.listeners['hm:conversion'].length === 1);

// No banner (hm.consent undefined) = allowed.
e = load({ search: '?gclid=abc&utm_source=x&ttclid=tt1' });
check('no banner: starts at once', e.cookieValue() && e.cookieValue().gclid === 'abc' && e.cookieValue().ttclid === 'tt1');
check('no banner: unknown keys are not kept', Object.keys(e.cookieValue()).sort().join() === 'gclid,ttclid');

// Cookie flags.
e = load({ search: '?gclid=abc', https: true });
var flags = e.cookieWrites[0];
check('cookie flags on https', /^hm_adc=/.test(flags) && /; path=\//.test(flags) && /; max-age=7776000/.test(flags) && /; SameSite=Lax/.test(flags) && /; Secure$/.test(flags));
e = load({ search: '?gclid=abc', https: false });
check('no Secure flag on http', e.cookieWrites[0].indexOf('Secure') < 0);
check('only the hm_adc cookie is written', e.cookieWrites.length === 1);

// Validation.
e = load({ search: '?gclid=' + encodeURIComponent('a b;c') + '&gbraid=' + new Array(258).join('a') + '&wbraid=ok-1_2.3~4&ttclid=%E0%A4%A' });
check('invalid ids are dropped, valid ones kept', e.cookieValue() && Object.keys(e.cookieValue()).join() === 'wbraid');
e = load({ search: '?gclid=a%20b&utm=1' });
check('no valid id: no cookie written', e.cookieWrites.length === 0);

// Merging: newer overwrites older; LinkedIn cookie is read.
e = load({ search: '?gclid=new', cookie: 'hm_adc=' + encodeURIComponent('{"gclid":"old","ttclid":"keep","junk":"x","wbraid":"a b"}') + '; li_fat_id=li1' });
var v = e.cookieValue();
check('newer ids overwrite older, others are kept', v.gclid === 'new' && v.ttclid === 'keep' && v.li_fat_id === 'li1' && !v.junk && !v.wbraid);
e = load({ search: '', cookie: 'hm_adc=' + encodeURIComponent('{"gclid":"same"}') });
check('nothing new: the cookie is not rewritten', e.cookieWrites.length === 0);

// Posting.
e = load({ search: '?gclid=abc' });
e.fire('hm:conversion', { id: SID, form: 'contact', value: null });
var f = e.fetches[0];
var sent = f && JSON.parse(f.init.body);
check('posts on hm:conversion', e.fetches.length === 1 && f.url === '/p/ads-conversions/click' && f.init.method === 'POST' && f.init.keepalive === true &&
  f.init.headers['Content-Type'] === 'application/json');
check('post body', sent.submission_id === SID && sent.form === 'contact' && sent.ids.gclid === 'abc');

e = load({ consent: { marketing: true }, search: '?gclid=abc' });
e.consent.marketing = false;
e.fire('hm:conversion', { id: SID, form: 'contact' });
check('consent withdrawn: no post', e.fetches.length === 0);

e = load({ search: '' });
e.fire('hm:conversion', { id: SID, form: 'contact' });
check('no ids: no post', e.fetches.length === 0);

e = load({ search: '?gclid=abc' });
e.fire('hm:conversion', { id: 'not-a-uuid', form: 'contact' });
e.fire('hm:conversion', {});
e.fire('hm:conversion', undefined);
check('bad or missing submission id: no post', e.fetches.length === 0);
e.fire('hm:conversion', { id: SID, form: 'bad form!' });
check('bad form slug is left out, id still posted', e.fetches.length === 1 && JSON.parse(e.fetches[0].init.body).form === undefined);

e = load({ search: '?gclid=abc', fetchFails: true });
e.fire('hm:conversion', { id: SID, form: 'contact' });
check('a failing post is ignored', e.fetches.length === 1);
setTimeout(function () {
  if (failures) { console.error(failures + ' failure(s)'); process.exit(1); }
}, 0);

// The file itself: no storage, no external host, no other cookie name.
check('no localStorage/sessionStorage/XMLHttpRequest', !/localStorage|sessionStorage|XMLHttpRequest/.test(src));
check('no external host', !/https?:\/\//.test(src));
check('only hm_adc and li_fat_id cookies are named', (src.match(/COOKIE = '([^']+)'/g) || []).length === 1 && /readCookie\('li_fat_id'\)/.test(src));

