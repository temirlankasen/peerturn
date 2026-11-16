import assert from 'node:assert/strict';
import test from 'node:test';
import { harness } from './harness.mjs';

test('a delayed same-session lookup cannot rotate an active helper', async () => {
  const h = await harness();
  const session = await h.$.session.id();
  let reads = 0;
  h.$.session.id = async () => ++reads === 1 ? new Promise(() => {}) : session;
  h.$.clock.sleep = async () => undefined;
  const event = { text: 'synthetic owner input', origin: { kind: 'composer' } };
  assert.equal(await h.hooks.get('prompt.submit')(h.$, event, async value => value), event);
  await h.tick();
  assert.equal(h.launched.length, 1);
  assert.ok(!h.requests.some(r => r.url.endsWith('/close')));
  assert.equal(h.timers.length, 1);
  assert.equal(h.timers[0].cancelled, false);
  assert.equal(h.submitted.length, 0);
});

test('concurrent same-session startup launches one helper', async () => {
  const h = await harness();
  await h.hooks.get('session.end')(h.$, {}, async value => value);
  const before = h.launched.length;
  await Promise.all([
    h.hooks.get('session.start')(h.$, {}, async value => value),
    h.hooks.get('session.start')(h.$, {}, async value => value),
  ]);
  await h.tick();
  assert.equal(h.launched.length, before + 1);
  assert.equal(h.timers.filter(timer => !timer.cancelled).length, 1);
});

for (const known of [false, true]) {
  test(`a proven different native session still retires the old helper (new session ${known ? 'bound before' : 'never bound'})`, async () => {
    const other = '22222222-2222-4222-8222-222222222222';
    const h = await harness();
    if (known) h.store.set('session:' + other, 2);
    h.$.session.id = async () => other;
    await h.hooks.get('session.start')(h.$, {}, async value => value);
    await h.tick();
    // Only a session bound before gets a helper of its own.
    assert.equal(h.launched.length, known ? 2 : 1);
    assert.ok(h.requests.some(r => r.url.endsWith('/close')));
    assert.equal(h.timers[0].cancelled, true);
    assert.equal(h.submitted.length, 0);
  });
}

test('an unproven current session never sends owner consent from a kept helper', async () => {
  const h = await harness();
  // The lookup outlives the bound, then reveals another session.
  h.$.session.id = () => new Promise(resolve => setTimeout(resolve, 30, '22222222-2222-4222-8222-222222222222'));
  h.$.clock.sleep = async () => undefined;
  const event = { text: 'join https://relay.example/i/route#secret', origin: { kind: 'composer' } };
  assert.equal(await h.hooks.get('prompt.submit')(h.$, event, async value => value), event);
  await h.tick();
  assert.ok(!h.requests.some(r => r.url.endsWith('/owner-boundary')));
  assert.equal(h.launched.length, 1);
});
