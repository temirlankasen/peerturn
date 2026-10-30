import test from 'node:test';
import assert from 'node:assert/strict';
import { harness } from './harness.mjs';

for (const code of ['runtime_unsupported', 'daemon_unavailable', 'needs_rebind',
  'permission_denied', 'peer_identity_failed', 'auth_failed']) {
  test(`pre-ready helper failure reaches the UI: ${code}`, async () => {
    const h = await harness('2.1.306', true, $ => {
      $.process.spawn = async function* () {
        yield { stream: 'stderr', text: 'peerturn: ' + code.slice(0, 4) };
        yield { stream: 'stderr', text: code.slice(4) + '\n' };
      };
    });
    assert.deepEqual(h.statuses, [code]);
    assert.equal(h.timers.length, 0);
    assert.equal(h.requests.length, 0);
  });
}

for (const text of ['private-path/token=private-marker\n',
  'peerturn: private-marker\n', 'peerturn: needs_rebind private-marker\n',
  'peerturn: ' + 'x'.repeat(5000) + '\n']) {
  test('untrusted stderr never becomes a UI diagnostic: ' + text.length, async () => {
    const h = await harness('2.1.306', true, $ => {
      $.process.spawn = async function* () { yield { stream: 'stderr', text }; };
    });
    assert.deepEqual(h.statuses, ['adapter_unavailable']);
    assert.equal(h.requests.length, 0);
  });
}

test('stderr after readiness cannot replace the lifecycle status', async () => {
  const h = await harness('2.1.306', true, $ => {
    $.process.spawn = async function* () {
      yield { stream: 'stdout', text: JSON.stringify({
        type: 'ready', url: 'http://127.0.0.1:39827', capability: 'a'.repeat(64),
      }) + '\n' };
      yield { stream: 'stderr', text: 'peerturn: needs_rebind\nprivate-marker\n' };
    };
  });
  assert.deepEqual(h.statuses, ['adapter_stopped']);
  assert.ok(h.timers.every(timer => timer.cancelled));
});

test('a helper that expired before connect restarts before the next Bash operation', async () => {
  let launches = 0;
  const h = await harness('2.1.308', true, $ => {
    $.process.spawn = async function* () {
      launches += 1;
      if (launches === 1) {
        yield { stream: 'stderr', text: 'peerturn: daemon_unavailable\n' };
        return;
      }
      yield { stream: 'stdout', text: JSON.stringify({
        type: 'ready', url: 'http://127.0.0.1:39827', capability: 'a'.repeat(64),
      }) + '\n' };
      await new Promise(() => {});
    };
  });
  assert.equal(launches, 1);
  const event = { tool: 'Bash', args: { command: 'peerturn connect' } };
  assert.equal(await h.hooks.get('tool.call')(h.$, event, async value => value), event);
  await h.tick();
  assert.equal(launches, 2);
  assert.equal(h.submitted.length, 0);
  assert.ok(!h.requests.some(value => value.url.endsWith('/authorize')));
});

test('subagent tools cannot restart the selected root helper', async () => {
  let launches = 0;
  const h = await harness('2.1.308', true, $ => {
    $.process.spawn = async function* () {
      launches += 1;
      yield { stream: 'stderr', text: 'peerturn: daemon_unavailable\n' };
    };
  });
  const event = { tool: 'Bash', agentId: 'child', args: {} };
  assert.equal(await h.hooks.get('tool.call')(h.$, event, async value => value), event);
  await h.tick();
  assert.equal(launches, 1);
  assert.equal(h.submitted.length, 0);
});

test('a frozen provider lookup cannot hold an owner Bash operation', async () => {
  const h = await harness();
  await h.hooks.get('session.end')(h.$, {}, async value => value);
  h.$.env.get = async () => new Promise(() => {});
  h.$.clock.sleep = async () => new Promise(resolve => setTimeout(resolve, 15));
  const event = { tool: 'Bash', args: {} };
  const outcome = await Promise.race([
    h.hooks.get('tool.call')(h.$, event, async value => value),
    new Promise(resolve => setTimeout(() => resolve('blocked'), 100)),
  ]);
  assert.equal(outcome, event);
  assert.equal(h.launched.length, 1);
  assert.equal(h.submitted.length, 0);
});

test('an active helper is not replaced by ordinary Bash operations', async () => {
  const h = await harness();
  const event = { tool: 'Bash', args: { command: 'echo owner work' } };
  assert.equal(await h.hooks.get('tool.call')(h.$, event, async value => value), event);
  assert.equal(h.launched.length, 1);
  assert.equal(h.submitted.length, 0);
});
