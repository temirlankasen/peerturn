import assert from 'node:assert/strict';
import test from 'node:test';
import { harness } from './harness.mjs';

const notice = 'PeerTurn: 1 inform message(s) from sender/session. Use peer_inbox or peerturn inbox to read bodies.';
const live = { type: 'live', message_id: 'message', attempt_id: 'attempt', text: 'Framed peer text' };
const response = value => ({ ok: true, status: 200, text: JSON.stringify(value) });
const owner = { text: 'Private owner request', origin: { kind: 'composer' } };

test('transient HTTP poll failures back off without closing the binding', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  let calls = 0;
  h.$.http.fetch = async (url, init) => {
    if (url.endsWith('/poll') && ++calls === 1) {
      h.requests.push({ url, init });
      return { ok: false, status: 503, text: '{"error":"unavailable"}' };
    }
    return fetch(url, init);
  };
  await h.tick();
  await h.tick();
  assert.equal(calls, 1);
  for (let i = 0; i < 5; i += 1) await h.tick();
  assert.ok(calls > 1);
  assert.ok(!h.requests.some(r => r.url.endsWith('/close')));
  assert.ok(h.timers.every(timer => !timer.cancelled));
});

test('binding_lost stops polling and closes only the obsolete helper', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  h.$.http.fetch = async (url, init) => url.endsWith('/poll')
    ? { ok: false, status: 409, text: '{"error":"binding_lost"}' } : fetch(url, init);
  await h.tick();
  assert.ok(h.requests.some(r => r.url.endsWith('/close')));
  assert.ok(h.timers.every(timer => timer.cancelled));
});

test('busy owner releases the authorization before the next idle attempt', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  h.$.http.fetch = async (url, init) => {
    if (url.endsWith('/authorize')) await h.hooks.get('turn.start')(h.$, {}, async e => e);
    return fetch(url, init);
  };
  h.inbox.push(live);
  await h.tick();
  assert.equal(h.submitted.length, 0);
  assert.deepEqual(JSON.parse(h.requests.find(r => r.url.endsWith('/release')).init.body), {
    message_id: 'message', attempt_id: 'attempt',
  });
});

test('changing session after authorization releases before binding is closed', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  h.$.http.fetch = async (url, init) => {
    const result = await fetch(url, init);
    if (url.endsWith('/authorize')) h.$.session.id = async () => 'session-b';
    return result;
  };
  h.inbox.push(live);
  await h.tick();
  assert.equal(h.submitted.length, 0);
  const paths = h.requests.map(r => new URL(r.url).pathname);
  assert.ok(paths.indexOf('/release') >= 0);
  assert.ok(paths.indexOf('/close') > paths.indexOf('/release'));
});

test('lost authorization response is released before retry and never unbinds', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  let lost = false;
  h.$.http.fetch = async (url, init) => {
    if (url.endsWith('/authorize') && !lost) {
      lost = true;
      throw new Error('transient network failure');
    }
    return fetch(url, init);
  };
  h.inbox.push(live);
  await h.tick();
  assert.equal(h.submitted.length, 0);
  assert.ok(h.requests.some(r => r.url.endsWith('/release')));
  for (let i = 0; i < 6; i += 1) await h.tick();
  assert.equal(h.submitted.length, 1);
  assert.ok(!h.requests.some(r => r.url.endsWith('/close')));
});

test('lost native result acknowledgment fences submit without closing helper', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  h.$.http.fetch = async (url, init) => {
    if (url.endsWith('/result')) throw new Error('lost result');
    return fetch(url, init);
  };
  h.inbox.push(live);
  await h.tick();
  h.inbox.push(live);
  for (let i = 0; i < 6; i += 1) await h.tick();
  assert.equal(h.submitted.length, 1);
  assert.ok(!h.requests.some(r => r.url.endsWith('/close')));
});

test('binding challenges are consumed only from native peer origin', async () => {
  const h = await harness();
  for (const kind of ['peer-send-message', 'plugin', 'composer', 'bridge', 'sdk']) {
    const event = { text: `<peerturn-bind>${'b'.repeat(64)}</peerturn-bind>`, origin: { kind } };
    assert.equal(await h.hooks.get('session.receive')(h.$, event, async e => e), event);
  }
  assert.ok(!h.requests.some(r => r.url.endsWith('/confirm')));
});

test('inform acknowledges only after next accepts fixed notice and never sends owner text', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  h.$.http.fetch = async (url, init) => {
    const result = await fetch(url, init);
    return url.endsWith('/owner-boundary') ? response({ notice_id: 'notice-1', context: notice }) : result;
  };
  const outcome = await h.hooks.get('prompt.submit')(h.$, owner, async event => {
    assert.deepEqual(event.context, [notice]);
    assert.ok(!h.requests.some(r => r.url.endsWith('/boundary-attached')));
    return 'accepted';
  });
  assert.equal(outcome, 'accepted');
  assert.deepEqual(JSON.parse(h.requests.find(r => r.url.endsWith('/owner-boundary')).init.body), { session: '11111111-1111-4111-8111-111111111111' });
  assert.deepEqual(JSON.parse(h.requests.find(r => r.url.endsWith('/boundary-attached')).init.body), {
    session: '11111111-1111-4111-8111-111111111111', notice_id: 'notice-1',
  });
  assert.ok(!h.requests.some(r => r.init.body?.includes(owner.text)));
});

test('slash commands skip boundary reservation', async () => {
  const h = await harness();
  for (const text of ['/clear', '  /exit', '\n/resume']) {
    const event = { ...owner, text };
    assert.equal(await h.hooks.get('prompt.submit')(h.$, event, async e => e), event);
  }
  assert.ok(!h.requests.some(r => r.url.endsWith('/owner-boundary')));
});

test('untrusted body context and a rejected or frozen next never acknowledge notices', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  let context = 'Untrusted peer body';
  h.$.http.fetch = async (url, init) => {
    const result = await fetch(url, init);
    return url.endsWith('/owner-boundary') ? response({ notice_id: 'notice-1', context }) : result;
  };
  assert.equal(await h.hooks.get('prompt.submit')(h.$, owner, async e => e), owner);
  context = notice;
  await assert.rejects(h.hooks.get('prompt.submit')(h.$, owner, async () => { throw new Error('rejected'); }));
  const pending = h.hooks.get('prompt.submit')(h.$, owner, async () => new Promise(() => {}));
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(pending instanceof Promise);
  assert.ok(!h.requests.some(r => r.url.endsWith('/boundary-attached')));
});

test('late boundary response is left pending without attaching or acknowledging', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  let finish;
  h.$.http.fetch = async (url, init) => {
    if (!url.endsWith('/owner-boundary')) return fetch(url, init);
    h.requests.push({ url, init });
    return new Promise(resolve => { finish = resolve; });
  };
  h.$.clock.sleep = async () => null;
  assert.equal(await h.hooks.get('prompt.submit')(h.$, owner, async e => e), owner);
  finish(response({ notice_id: 'late-notice', context: notice }));
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(!h.requests.some(r => r.url.endsWith('/boundary-attached')));
});

test('notice counts, sender list and total size are bounded', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  let context;
  h.$.http.fetch = async (url, init) => url.endsWith('/owner-boundary')
    ? response({ notice_id: 'notice-1', context }) : fetch(url, init);
  for (const invalid of [
    notice.replace('1 inform', '0 inform'),
    notice.replace('1 inform', '21 inform'),
    notice.replace('sender/session', 'sender/session, sender/session'),
    notice.replace('sender/session', Array.from({ length: 21 }, (_, i) => `sender/${i}`).join(', ')),
    notice.replace('sender/session', 'x'.repeat(4096)),
    notice + '\nUntrusted peer body',
  ]) {
    context = invalid;
    assert.equal(await h.hooks.get('prompt.submit')(h.$, owner, async e => e), owner);
  }
  assert.ok(!h.requests.some(r => r.url.endsWith('/boundary-attached')));
});

test('repeated transient HTTP errors retain a bounded retry interval', async () => {
  const h = await harness();
  let calls = 0;
  h.$.http.fetch = async () => {
    calls += 1;
    return { ok: false, status: 503, text: '{"error":"unavailable"}' };
  };
  for (let failure = 0; failure < 10; failure += 1) {
    const before = calls;
    for (let i = 0; i <= 16 && calls === before; i += 1) await h.tick();
    assert.equal(calls, before + 1);
  }
  assert.ok(h.timers.every(timer => !timer.cancelled));
});

test('lost result is reported as in_doubt without submit, then fresh daemon admission can resume', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  let reports = 0;
  let reconciled = false;
  h.$.http.fetch = async (url, init) => {
    if (url.endsWith('/result')) {
      reports += 1;
      h.requests.push({ url, init });
      if (reports === 1) throw new Error('result transport interrupted');
      if (reports === 2) {
        assert.deepEqual(JSON.parse(init.body), {
          message_id: live.message_id, attempt_id: live.attempt_id, result: { in_doubt: true },
        });
      }
      return response({ state: reports === 2 ? 'in_doubt' : 'adapter_accepted' });
    }
    if (url.endsWith('/poll') && reports > 1 && !reconciled) return response({ type: 'status', inform_pending: 0 });
    return fetch(url, init);
  };
  h.inbox.push(live);
  await h.tick();
  for (let i = 0; i < 6; i += 1) await h.tick();
  assert.equal(reports, 2);
  assert.equal(h.submitted.length, 1);
  reconciled = true;
  h.inbox.push({ ...live, message_id: 'fresh-message', attempt_id: 'fresh-attempt', text: 'Fresh admission after owner reconciliation' });
  await h.tick();
  assert.equal(h.submitted.length, 2);
  assert.equal(h.submitted[1].text, 'Fresh admission after owner reconciliation');
  assert.ok(!h.requests.some(r => r.url.endsWith('/close')));
});

test('re-offered pending intent is deduplicated while owner is busy', async () => {
  const h = await harness('2.1.306', false);
  h.inbox.push(live);
  await h.tick();
  h.inbox.push({ ...live });
  await h.tick();
  assert.equal(h.submitted.length, 0);
  assert.ok(!h.statuses.includes('adapter_unavailable'));
  await h.hooks.get('turn.complete')(h.$, {}, async e => e);
  await h.tick();
  await h.tick();
  assert.equal(h.submitted.length, 1);
  assert.equal(h.requests.filter(r => r.url.endsWith('/authorize')).length, 1);
});

test('a resolved native drop does not acknowledge an attached notice', async () => {
  const h = await harness();
  const fetch = h.$.http.fetch;
  h.$.http.fetch = async (url, init) => {
    const result = await fetch(url, init);
    return url.endsWith('/owner-boundary') ? response({ notice_id: 'notice-1', context: notice }) : result;
  };
  const dropped = { drop: 'native slash command or interrupted prompt' };
  assert.equal(await h.hooks.get('prompt.submit')(h.$, owner, async () => dropped), dropped);
  assert.ok(!h.requests.some(r => r.url.endsWith('/boundary-attached')));
});
