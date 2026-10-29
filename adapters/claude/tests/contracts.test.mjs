import test from 'node:test';
import assert from 'node:assert/strict';
import { harness } from './harness.mjs';

const notice = 'PeerTurn: 1 inform message(s) from sender/session. Use peer_inbox or peerturn inbox to read bodies.';

test('missing clock.sleep is a global prerequisite failure and owner work continues', async () => {
  const h = await harness('2.1.308', false, $ => { $.clock.sleep = undefined; });
  const result = await h.mod.inspectRuntimeContract(h.$);
  const failure = result.failures.find(value => value.contract_id === 'claude.clock.sleep');
  assert.deepEqual(failure.affected_capabilities,
    ['coauthor', 'owner_invitation', 'inform_boundary', 'inform_notification']);
  assert.equal(h.launched.length, 0);
  const owner = { text: 'Owner request', origin: { kind: 'composer' } };
  assert.equal(await h.hooks.get('prompt.submit')(h.$, owner, async value => value), owner);
});

test('runtime inspection is model-free and distinguishes unobserved lifecycle semantics', async () => {
  const h = await harness('2.1.308');
  const before = [h.launched.length, h.requests.length, h.submitted.length];
  const result = await h.mod.inspectRuntimeContract(h.$);
  assert.equal(result.version, '2.1.308');
  assert.equal(result.failures.length, 0);
  assert.equal(result.lifecycle, 'not_observed');
  assert.deepEqual([h.launched.length, h.requests.length, h.submitted.length], before);
});

for (const change of [
  $ => { $.session.version = async () => null; },
  $ => { $.session.version = async () => ({ version: '2.1.308', base: '2.1.306' }); },
  $ => { $.session.id = async () => 'not-a-uuid'; },
  $ => { $.http.fetch = undefined; },
]) {
  test('malformed core contracts stop admission and preserve owner continuation', async () => {
    const h = await harness('2.1.308', false, change);
    assert.equal(h.launched.length, 0);
    assert.equal(h.submitted.length, 0);
    assert.ok(h.statuses.some(value => value.includes('runtime_contract_changed') && value.includes('doctor')));
    const owner = { text: 'Owner request', origin: { kind: 'composer' } };
    assert.equal(await h.hooks.get('prompt.submit')(h.$, owner, async value => value), owner);
  });
}

test('missing submit API closes coauthor while newer runtime owner invitations and notices work', async () => {
  const h = await harness('2.1.308', true, $ => { $.prompt.submit = undefined; });
  assert.equal(h.launched.length, 1);
  const fetch = h.$.http.fetch;
  h.$.http.fetch = async (url, init) => url.endsWith('/owner-boundary')
    ? (h.requests.push({ url, init }), { ok: true, status: 200, text: JSON.stringify({ notice_id: 'n1', context: notice }) })
    : fetch(url, init);
  const event = { text: 'https://relay.example/i/explicit-owner-link', origin: { kind: 'composer' } };
  const outcome = await h.hooks.get('prompt.submit')(h.$, event, async value => value);
  assert.deepEqual(outcome.context, [notice]);
  const boundary = h.requests.find(value => value.url.endsWith('/owner-boundary'));
  assert.deepEqual(JSON.parse(boundary.init.body).invitation_links, [event.text]);
  h.inbox.push({ type: 'live', message_id: 'm1', attempt_id: 'a1', text: 'private peer body' });
  await h.hooks.get('turn.complete')(h.$, {}, async value => value);
  await h.tick();
  assert.equal(h.submitted.length, 0);
  assert.ok(h.requests.some(value => value.url.endsWith('/contract') && JSON.parse(value.init.body).contract_id === 'claude.prompt.submit'));
});

test('malformed lifecycle closes only coauthor and never blocks the owner', async () => {
  const h = await harness('2.1.308');
  const malformed = { agentId: null };
  assert.equal(await h.hooks.get('turn.complete')(h.$, malformed, async value => value), malformed);
  h.inbox.push({ type: 'live', message_id: 'm1', attempt_id: 'a1', text: 'private peer body' });
  await h.tick();
  assert.equal(h.submitted.length, 0);
  assert.ok(h.statuses.some(value => value.includes('runtime_contract_changed')));
  assert.ok(h.requests.some(value => value.url.endsWith('/contract') && JSON.parse(value.init.body).contract_id === 'claude.lifecycle'));
});

test('missing toast API preserves native coauthor admission', async () => {
  const h = await harness('2.1.308', true, $ => { $.ui.toast = undefined; });
  h.inbox.push({ type: 'status', inform_pending: 1 });
  await h.tick();
  h.inbox.push({ type: 'live', message_id: 'm1', attempt_id: 'a1', text: 'Framed peer text' });
  await h.tick();
  assert.equal(h.submitted.length, 1);
});

test('newer runtimes keep normal coauthor and exact native nonce checks', async () => {
  const h = await harness('2.1.308');
  const frame = { text: `<peerturn-bind>${'b'.repeat(64)}</peerturn-bind>` };
  for (const kind of ['composer', 'bridge', 'plugin', 'sdk']) {
    const event = { ...frame, origin: { kind } };
    assert.equal(await h.hooks.get('session.receive')(h.$, event, async value => value), event);
    const prompt = { text: 'https://relay.example/i/explicit-link', origin: { kind } };
    await h.hooks.get('prompt.submit')(h.$, prompt, async value => value);
  }
  const links = h.requests.filter(value => value.url.endsWith('/owner-boundary'))
    .map(value => JSON.parse(value.init.body).invitation_links);
  assert.deepEqual(links, [['https://relay.example/i/explicit-link'], undefined]);
  const result = await h.hooks.get('session.receive')(h.$, { ...frame, origin: { kind: 'peer' } },
    async () => { throw new Error('binding frame reached the model'); });
  assert.equal(result.consumed, 'PeerTurn binding challenge');
  await h.hooks.get('turn.complete')(h.$, {}, async value => value);
  h.inbox.push({ type: 'live', message_id: 'm1', attempt_id: 'a1', text: 'Framed peer text' });
  await h.tick();
  assert.deepEqual(h.submitted, [{ text: 'Framed peer text' }]);
});

test('a frozen diagnostic UI never blocks an owner continuation after contract loss', async () => {
  const h = await harness('2.1.308');
  h.$.ui.status = async () => new Promise(() => {});
  const event = { agentId: null };
  const result = await Promise.race([
    h.hooks.get('turn.complete')(h.$, event, async value => value),
    new Promise(resolve => setTimeout(() => resolve('timeout'), 50)),
  ]);
  assert.equal(result, event);
});

test('an active runtime never rereads a hanging version API before owner continuation', async () => {
  const h = await harness('2.1.308');
  h.$.session.version = async () => new Promise(() => {});
  const event = { text: 'Owner request', origin: { kind: 'composer' } };
  const result = await Promise.race([
    h.hooks.get('prompt.submit')(h.$, event, async value => value),
    new Promise(resolve => setTimeout(() => resolve('timeout'), 50)),
  ]);
  assert.equal(result, event);
});

test('a startup version API timeout preserves owner continuation without model effects', async () => {
  const h = await harness('2.1.308', false, $ => {
    $.session.version = async () => new Promise(() => {});
    $.clock.sleep = async () => null;
  });
  const event = { text: 'Owner request', origin: { kind: 'composer' } };
  assert.equal(await h.hooks.get('prompt.submit')(h.$, event, async value => value), event);
  assert.equal(h.launched.length, 0);
  assert.equal(h.submitted.length, 0);
});
