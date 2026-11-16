import assert from 'node:assert/strict';
import test from 'node:test';
import { harness, SESSION } from './harness.mjs';

// Owners keep dozens of Claude Code sessions open and use PeerTurn in one or
// two. A session that never used it must cost no helper process and no poll.
const pass = async value => value;
const bash = command => ({ tool: 'Bash', command });
const owner = text => ({ text, origin: { kind: 'composer' } });

test('a session that never used PeerTurn launches nothing on start, prompts or ordinary Bash', async () => {
  const h = await harness('2.1.321', true, () => {}, false);
  await h.hooks.get('prompt.submit')(h.$, owner('please refactor this'), pass);
  for (const command of ['ls', 'cd /src/peerturn && make test', 'git -C /src/peerturn status',
    'ls ~/.local/bin/peerturn', 'echo peerturn', 'command -v peerturn-other', 'cat "/opt/peerturn"x',
    'PROJECT_DIR=/src/peerturn true', 'env PROJECT_DIR=/src/peerturn make test', 'tail /tmp/peerturn.log',
    'peerturn-other run', 'A="x y" make']) {
    await h.hooks.get('tool.call')(h.$, bash(command), pass);
  }
  await h.hooks.get('tool.call')(h.$, { tool: 'Read', file_path: '/tmp/peerturn' }, pass);
  await h.tick();
  assert.deepEqual(h.launched, []);
  assert.equal(h.timers.length, 0);
  assert.equal(h.requests.length, 0);
  assert.deepEqual(h.statuses, []);
});

for (const command of ['peerturn connect', '~/.local/bin/peerturn status --json', 'cd /tmp && peerturn doctor',
  'PEERTURN_NO_UPDATE_CHECK=1 peerturn session bind --current', 'echo x | peerturn send --current --stdin',
  'command peerturn connect', '"/opt/tools/bin/peerturn" connect', "'peerturn' status", 'env A=1 peerturn status',
  'command -- peerturn connect', 'env LABEL="hello world" peerturn connect',
  '"/opt/App Support/peerturn" connect', '"/tmp/build=debug/peerturn" connect',
  '"$HOME"/.local/bin/peerturn status', 'PATH="$HOME/.local/bin":$PATH peerturn connect', '"$HOME/.local/bin"/peerturn connect']) {
  test('running the peerturn command starts the helper: ' + command, async () => {
    const h = await harness('2.1.321', true, () => {}, false);
    await h.hooks.get('tool.call')(h.$, bash(command), pass);
    await h.tick();
    assert.equal(h.launched.length, 1);
  });
}

test('a subagent running peerturn does not start the root helper', async () => {
  const h = await harness('2.1.321', true, () => {}, false);
  await h.hooks.get('tool.call')(h.$, { ...bash('peerturn connect'), agentId: 'child' }, pass);
  assert.equal(h.launched.length, 0);
});

test('a pasted invitation link starts the helper; other origins do not', async () => {
  const link = 'join https://relay.example/i/route#secret';
  const quiet = await harness('2.1.321', true, () => {}, false);
  await quiet.hooks.get('prompt.submit')(quiet.$, { text: link, origin: { kind: 'bridge' } }, pass);
  assert.equal(quiet.launched.length, 0);
  const h = await harness('2.1.321', true, () => {}, false);
  await h.hooks.get('prompt.submit')(h.$, owner(link), pass);
  assert.equal(h.launched.length, 1);
});

test('a PeerTurn MCP tool call starts the helper so a bound session can prove itself', async () => {
  const h = await harness('2.1.321', true, () => {}, false);
  await h.hooks.get('tool.call')(h.$, { tool: 'mcp__playwright__browser_click' }, pass);
  assert.equal(h.launched.length, 0);
  await h.hooks.get('tool.call')(h.$, { tool: 'mcp__peerturn__peer_status' }, pass);
  assert.equal(h.launched.length, 1);
});

test('a confirmed binding is remembered, so the next start of the session brings the helper back', async () => {
  const store = new Map();
  const h = await harness('2.1.321', true, () => {}, false, store);
  await h.hooks.get('tool.call')(h.$, bash('peerturn connect'), pass);
  await h.tick();
  await h.hooks.get('session.receive')(h.$, { text: `<peerturn-bind>${'b'.repeat(64)}</peerturn-bind>`, origin: { kind: 'peer' } }, pass);
  await h.tick();
  assert.ok(h.requests.some(r => r.url.endsWith('/confirm')));
  assert.ok(Number.isSafeInteger(store.get('session:' + SESSION)));
  const next = await harness('2.1.321', true, () => {}, false, store);
  assert.equal(next.launched.length, 1);
});

test('a refused confirmation is not remembered', async () => {
  const store = new Map();
  const h = await harness('2.1.321', true, $ => {
    const fetch = $.http.fetch;
    $.http.fetch = async (url, init) => url.endsWith('/confirm')
      ? { ok: false, status: 409, text: JSON.stringify({ error: 'needs_rebind' }) } : fetch(url, init);
  }, false, store);
  await h.hooks.get('tool.call')(h.$, bash('peerturn connect'), pass);
  await h.tick();
  await h.hooks.get('session.receive')(h.$, { text: `<peerturn-bind>${'b'.repeat(64)}</peerturn-bind>`, origin: { kind: 'peer' } }, pass);
  await h.tick();
  assert.equal(store.size, 0);
});

test('a remembered session the daemon refuses is forgotten and stays quiet afterwards', async () => {
  let launches = 0;
  const h = await harness('2.1.321', true, $ => {
    $.process.spawn = async function* () {
      launches += 1;
      yield { stream: 'stderr', text: 'peerturn: needs_rebind\n' };
    };
  });
  await h.tick();
  assert.equal(launches, 1);
  assert.equal(h.store.has('session:' + SESSION), false);
  await h.hooks.get('prompt.submit')(h.$, owner('ordinary work'), pass);
  await h.hooks.get('tool.call')(h.$, bash('make test'), pass);
  await h.hooks.get('session.start')(h.$, {}, pass);
  await h.tick();
  assert.equal(launches, 1);
  assert.deepEqual(h.statuses, []);
});

test('remembered sessions are bounded, keeping the newest', async () => {
  const store = new Map();
  for (let i = 0; i < 40; i += 1) store.set(`session:${String(i).padStart(8, '0')}-0000-4000-8000-000000000000`, i + 1);
  store.set('unrelated', 'kept');
  const h = await harness('2.1.321', true, () => {}, false, store);
  await h.hooks.get('tool.call')(h.$, bash('peerturn connect'), pass);
  await h.tick();
  await h.hooks.get('session.receive')(h.$, { text: `<peerturn-bind>${'c'.repeat(64)}</peerturn-bind>`, origin: { kind: 'peer' } }, pass);
  await h.tick();
  const sessions = [...store.keys()].filter(key => key.startsWith('session:'));
  assert.equal(sessions.length, 32);
  assert.ok(store.has('session:' + SESSION));
  assert.ok(!store.has('session:00000000-0000-4000-8000-000000000000'));
  assert.equal(store.get('unrelated'), 'kept');
});

test('a missing or failing store never blocks owner work and launches nothing', async () => {
  const h = await harness('2.1.321', true, $ => {
    $.store = { get: () => { throw new Error('store unavailable'); }, set: async () => { throw new Error('no'); } };
  }, false);
  const prompt = owner('ordinary work');
  assert.equal(await h.hooks.get('prompt.submit')(h.$, prompt, pass), prompt);
  assert.equal(h.launched.length, 0);
});

// The hook runs before every Bash call; a long generated command must not stall it.
test('recognising the peerturn command stays linear on long commands', async () => {
  const h = await harness('2.1.321', true, () => {}, false);
  const started = performance.now();
  for (const command of [';'.repeat(100000), ';A=x'.repeat(25000), '\n'.repeat(100000), ' \n'.repeat(50000),
    ';"'.repeat(50000), '\nA="'.repeat(25000), 'A=1 '.repeat(25000) + 'x', '"a"'.repeat(33000)]) {
    await h.hooks.get('tool.call')(h.$, bash(command), pass);
  }
  assert.ok(performance.now() - started < 1000);
  assert.equal(h.launched.length, 0);
});
