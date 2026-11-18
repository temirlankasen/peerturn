import assert from 'node:assert/strict';
import test from 'node:test';
import { harness, SESSION } from './harness.mjs';

// A release that did not remember bound sessions leaves the plugin store empty
// after an upgrade. setup names the bound session; only that session may start
// a helper, and a refusal retires the hint for good.
const OTHER = 'ffffffff-ffff-4fff-8fff-ffffffffffff';
const pass = async value => value;
const refuse = $ => {
  $.process.spawn = async function* () { yield { stream: 'stderr', text: 'peerturn: needs_rebind\n' }; };
};

test('the session named by the upgrade hint brings its helper back with an empty store', async () => {
  const h = await harness('2.1.321', true, () => {}, false, new Map(), { peerturn_bound_session: SESSION });
  assert.equal(h.launched.length, 1);
});

test('a session the upgrade hint does not name launches nothing', async () => {
  const h = await harness('2.1.321', true, () => {}, false, new Map(), { peerturn_bound_session: OTHER });
  await h.hooks.get('prompt.submit')(h.$, { text: 'ordinary work', origin: { kind: 'composer' } }, pass);
  await h.hooks.get('tool.call')(h.$, { tool: 'Bash', command: 'make test' }, pass);
  await h.tick();
  assert.deepEqual(h.launched, []);
  assert.equal(h.timers.length, 0);
  assert.equal(h.requests.length, 0);
});

test('a hinted session the daemon refuses stays quiet although the hint remains configured', async () => {
  const store = new Map();
  let launches = 0;
  const count = $ => {
    refuse($);
    const spawn = $.process.spawn;
    $.process.spawn = (...args) => { launches += 1; return spawn(...args); };
  };
  const first = await harness('2.1.321', true, count, false, store, { peerturn_bound_session: SESSION });
  await first.tick();
  assert.equal(launches, 1);
  assert.equal(store.get('session:' + SESSION), 0);
  await harness('2.1.321', true, count, false, store, { peerturn_bound_session: SESSION });
  assert.equal(launches, 1);
});
