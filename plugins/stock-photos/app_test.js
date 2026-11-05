// Runs ui/app.js against a minimal DOM stub; no dependencies:
//   node app_test.js
// Lives outside ui/ because everything under ui/ is served.
'use strict';
const fs = require('fs');
const path = require('path');
const vm = require('vm');

const src = fs.readFileSync(path.join(__dirname, 'ui', 'app.js'), 'utf8');

// refuse: the bridge rejects runInputs, as on the Settings tab.
async function run(refuse) {
  const note = { hidden: true };
  const content = { hidden: false };
  const field = { value: '' };
  const form = { elements: { provider: { value: 'pexels' } }, addEventListener() {} };
  const els = { '#form': form, '#q': field, '#count': { value: '12' }, '#orientation': { value: 'any' }, '#runs-note': note, '#content': content };
  const calls = [];
  const hatamoto = {
    ready: Promise.resolve(),
    runInputs(v) {
      calls.push(v);
      return refuse ? Promise.reject(new Error('no run form on this page')) : Promise.resolve();
    },
  };
  const unhandled = [];
  process.once('unhandledRejection', (e) => unhandled.push(e));
  vm.runInNewContext(src, { document: { querySelector: (s) => els[s] }, hatamoto, Math, Number });
  await new Promise((r) => setTimeout(r, 10));
  return { note, content, calls, unhandled };
}

(async () => {
  let failures = 0;
  const check = (ok, msg) => { if (!ok) { failures++; console.error('FAIL: ' + msg); } };

  let r = await run(true);
  check(r.calls.length === 1, 'runInputs called once');
  check(r.note.hidden === false, 'note shown when the admin refuses the inputs');
  check(r.content.hidden === true, 'form hidden when the admin refuses the inputs');
  check(r.unhandled.length === 0, 'no unhandled rejection');

  r = await run(false);
  check(r.note.hidden === true, 'note hidden on the Runs tab');
  check(r.content.hidden === false, 'form shown on the Runs tab');

  if (failures) process.exit(1);
  console.log('ok');
})();
