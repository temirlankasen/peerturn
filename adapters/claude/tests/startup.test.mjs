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
