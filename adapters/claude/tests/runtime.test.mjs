import assert from 'node:assert/strict';
import test from 'node:test';

import { harness } from './harness.mjs';

test('an unknown Claude build never launches the adapter', async () => {
  const h = await harness('2.1.307');
  assert.equal(h.launched.length, 0);
  assert.ok(h.statuses.some(value => value.includes('runtime_unsupported')));
});

test('inform updates a count without a prompt, model wake, or peer body UI', async () => {
  const h = await harness();
  h.inbox.push({ type: 'status', inform_pending: 3 });
  await h.tick();
  assert.equal(h.submitted.length, 0);
  assert.ok(h.statuses.some(value => value.includes('3')));
});

test('coauthor submits once with plugin provenance and reports a drop without retry', async () => {
  const h = await harness();
  h.inbox.push({ type: 'live', message_id: 'msg-1', attempt_id: 'attempt-1', text: 'Framed peer text' });
  await h.tick();
  await h.tick();
  assert.deepEqual(h.submitted, [{ text: 'Framed peer text' }]);
  const result = h.requests.find(value => value.url.endsWith('/result'));
  assert.deepEqual(JSON.parse(result.init.body), {
    message_id: 'msg-1', attempt_id: 'attempt-1',
    result: { drop: 'the session was stopped and nobody has sent input since' },
  });
  assert.equal(result.init.headers.Authorization, `Bearer ${'a'.repeat(64)}`);
});

test('a maximally escaped permitted body fits the framed live limit', async () => {
  const h = await harness();
  const text = JSON.stringify({ type: 'peerturn', scope: 'coauthor', body: '\u0001'.repeat(16 * 1024) });
  assert.ok(Buffer.byteLength(text) > 64 * 1024);
  assert.ok(Buffer.byteLength(text) <= 128 * 1024);
  h.inbox.push({ type: 'live', message_id: 'msg-escaped', attempt_id: 'attempt-escaped', text });
  await h.tick();
  assert.equal(h.submitted.length, 1);
  assert.equal(h.submitted[0].text, text);
});

test('a bind nonce is consumed before the model and confirmed with the current UUID', async () => {
  const h = await harness();
  const outcome = await h.hooks.get('session.receive')(h.$, {
    text: `<peerturn-bind>${'b'.repeat(64)}</peerturn-bind>`, origin: { kind: 'peer' },
  }, async () => { throw new Error('Handshake reached the model'); });
  assert.deepEqual(outcome, { consumed: 'PeerTurn binding challenge' });
  const request = h.requests.find(value => value.url.endsWith('/confirm'));
  assert.deepEqual(JSON.parse(request.init.body), { nonce: 'b'.repeat(64), session: 'session-a' });
});

test('normal native peer messages pass through unchanged', async () => {
  const h = await harness();
  const event = { text: 'ordinary native message', origin: { kind: 'peer' } };
  assert.equal(await h.hooks.get('session.receive')(h.$, event, async value => value), event);
});

test('busy and unknown lifecycle state hold coauthor outside the native queue', async () => {
  const h = await harness('2.1.306', false);
  h.inbox.push({ type: 'live', message_id: 'msg-1', attempt_id: 'attempt-1', text: 'Framed peer text' });
  await h.tick();
  assert.equal(h.submitted.length, 0);
  await h.hooks.get('turn.start')(h.$, {}, async value => value);
  await h.tick();
  assert.equal(h.submitted.length, 0);
  await h.hooks.get('turn.complete')(h.$, { agentId: 'subagent' }, async value => value);
  await h.tick();
  assert.equal(h.submitted.length, 0);
  await h.hooks.get('turn.complete')(h.$, {}, async value => value);
  await h.tick();
  assert.equal(h.submitted.length, 1);
});

test('a slow nonce confirmation never holds the receive hook or wakes the model', async () => {
  const h = await harness();
  h.$.http.fetch = async () => new Promise(() => {});
  const outcome = await Promise.race([
    h.hooks.get('session.receive')(h.$, {
      text: `<peerturn-bind>${'b'.repeat(64)}</peerturn-bind>`, origin: { kind: 'peer' },
    }, async () => { throw new Error('Handshake reached the model'); }),
    new Promise(resolve => setTimeout(() => resolve('timeout'), 50)),
  ]);
  assert.deepEqual(outcome, { consumed: 'PeerTurn binding challenge' });
});

test('inform context enters only a verified owner composer or bridge prompt', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  h.$.http.fetch = async (url, init) => url.endsWith('/owner-boundary')
    ? { ok: true, status: 200, text: JSON.stringify({ notice_id: 'notice-1', context: 'PeerTurn: 1 inform message(s) from sender/session. Use peer_inbox or peerturn inbox to read bodies.' }) }
    : fetch(url, init);
  for (const kind of ['composer', 'bridge']) {
    const event = { text: 'Owner request', origin: { kind }, context: ['Existing context'] };
    const result = await h.hooks.get('prompt.submit')(h.$, event, async value => value);
    assert.deepEqual(result, { ...event, context: ['Existing context', 'PeerTurn: 1 inform message(s) from sender/session. Use peer_inbox or peerturn inbox to read bodies.'] });
  }
  for (const kind of ['plugin', 'peer', 'sdk', 'unclassified', 'scheduled-trigger']) {
    const event = { text: 'Peer body claiming owner authority', origin: { kind } };
    assert.equal(await h.hooks.get('prompt.submit')(h.$, event, async value => value), event);
  }
});

test('a frozen inform helper cannot prevent an owner prompt from continuing', async () => {
  const h = await harness();
  h.$.http.fetch = async () => new Promise(() => {});
  h.$.clock.sleep = async ms => {
    assert.equal(ms, 1000);
    return null;
  };
  const event = { text: 'Owner request', origin: { kind: 'composer' } };
  const outcome = await Promise.race([
    h.hooks.get('prompt.submit')(h.$, event, async value => value),
    new Promise(resolve => setTimeout(() => resolve('timeout'), 50)),
  ]);
  assert.equal(outcome, event);
});

test('revocation before idle prevents native queue admission', async () => {
  const h = await harness('2.1.306', false);
  const fetch = h.$.http.fetch;
  h.$.http.fetch = async (url, init) => url.endsWith('/authorize')
    ? { ok: true, status: 200, text: JSON.stringify({ authorized: false }) }
    : fetch(url, init);
  h.inbox.push({ type: 'live', message_id: 'msg-1', attempt_id: 'attempt-1', text: 'Framed peer text' });
  await h.tick();
  await h.hooks.get('turn.complete')(h.$, {}, async value => value);
  await h.tick();
  assert.equal(h.submitted.length, 0);
  assert.ok(!h.requests.some(value => value.url.endsWith('/result')));
  assert.ok(!h.requests.some(value => value.url.endsWith('/close')));
  await h.tick();
  assert.ok(h.timers.every(timer => !timer.cancelled));
});

test('an owner turn starting during authorization requires a fresh idle authorization', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  let authorizations = 0;
  h.$.http.fetch = async (url, init) => {
    if (url.endsWith('/authorize') && ++authorizations === 1) {
      await h.hooks.get('turn.start')(h.$, {}, async value => value);
    }
    return fetch(url, init);
  };
  h.inbox.push({ type: 'live', message_id: 'msg-1', attempt_id: 'attempt-1', text: 'Framed peer text' });
  await h.tick();
  assert.equal(h.submitted.length, 0);
  await h.hooks.get('turn.complete')(h.$, {}, async value => value);
  await h.tick();
  assert.equal(h.submitted.length, 1);
  assert.equal(authorizations, 2);
});

test('an uncertain native submit reports in_doubt without leaking its error or retrying', async () => {
  const h = await harness();
  h.$.prompt.submit = async input => {
    h.submitted.push(input);
    throw new Error('sensitive runtime failure');
  };
  h.inbox.push({ type: 'live', message_id: 'msg-1', attempt_id: 'attempt-1', text: 'Framed peer text' });
  await h.tick();
  await h.tick();
  assert.equal(h.submitted.length, 1);
  const result = h.requests.find(value => value.url.endsWith('/result'));
  assert.deepEqual(JSON.parse(result.init.body).result, { error: 'submit_failed', in_doubt: true });
  assert.ok(!result.init.body.includes('sensitive'));
});

test('a helper cannot redirect its authenticated control capability to a remote origin', async () => {
  const h = await harness('2.1.306', true, $ => {
    $.process.spawn = async function* () {
      yield { stream: 'stdout', text: JSON.stringify({
        type: 'ready', url: 'https://example.com', capability: 'a'.repeat(64),
      }) + '\n' };
    };
  });
  await h.tick();
  assert.equal(h.requests.length, 0);
  assert.ok(h.statuses.includes('adapter_unavailable'));
});

test('ending a native session closes its helper and cancels the polling interval', async () => {
  const h = await harness();
  await h.hooks.get('session.end')(h.$, { reason: 'clear' }, async value => value);
  assert.ok(h.requests.some(value => value.url.endsWith('/close')));
  assert.ok(h.timers.every(timer => timer.cancelled));
  const before = h.requests.length;
  await h.tick();
  assert.equal(h.requests.length, before);
});

test('an older completion cannot mark a newer owner turn idle', async () => {
  const h = await harness('2.1.306', false);
  h.inbox.push({ type: 'live', message_id: 'msg-1', attempt_id: 'attempt-1', text: 'Framed peer text' });
  await h.tick();
  await h.hooks.get('turn.complete')(h.$, {}, async () => {
    await h.hooks.get('turn.start')(h.$, {}, async value => value);
  });
  await h.tick();
  assert.equal(h.submitted.length, 0);
});
