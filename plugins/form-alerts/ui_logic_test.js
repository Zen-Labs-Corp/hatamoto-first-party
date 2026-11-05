// Runs the settings checks of ui/app.js without a browser:
//   node ui_logic_test.js
'use strict';
var fs = require('fs');
var path = require('path');
var vm = require('vm');
var assert = require('assert');

var ctx = { hatamoto: { ready: new Promise(function () {}) }, document: {} };
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(path.join(__dirname, 'ui', 'app.js'), 'utf8'), ctx);

var forms = [
  { slug: 'contact', fields: ['name', 'email'] },
  { slug: 'quote', fields: ['budget'] },
];
function warn(s, f) { return Array.from(ctx.settingWarnings(Object.assign({ forms: [], fields: [] }, s), f || forms)); }

assert.deepStrictEqual(warn({}), [], 'nothing configured');
assert.deepStrictEqual(warn({ forms: ['contact'], fields: ['name'] }), [], 'all known');
assert.strictEqual(warn({ forms: ['Contact'] }).length, 1, 'wrong case is unknown');
assert.ok(warn({ forms: ['Contact'] })[0].includes('"Contact"'));
assert.strictEqual(warn({ forms: [' contact ', 'contact-form'] }).length, 1, 'only the typo');
assert.strictEqual(warn({ fields: ['Email'] }).length, 1, 'field typo');
assert.deepStrictEqual(warn({ forms: ['contact'], fields: ['budget'] }).length, 1, 'field of an unchosen form');
assert.deepStrictEqual(warn({ forms: ['x'], fields: ['x'] }, []), [], 'no form list, no claim');
console.log('ok');
