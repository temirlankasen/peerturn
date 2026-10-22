import test from 'node:test';
import assert from 'node:assert/strict';
import { harness } from './harness.mjs';

const link = 'https://relay.example/i/AAAAAAAAAAAAAAAAAAAAAA#v=1&ik='
  + 'A'.repeat(43) + '&ek=' + 'A'.repeat(43) + '&fp=' + 'a'.repeat(64) + '&mh=' + 'b'.repeat(64);

test('only human composer sends explicit invitation links to the private helper', async () => {
  for (const kind of ['composer', 'bridge', 'plugin', 'peer']) {
    const h = await harness();
    const event = { text: 'private owner prose <' + link + '>', origin: { kind } };
    await h.hooks.get('prompt.submit')(h.$, event, async value => value);
    const request = h.requests.find(r => r.url.endsWith('/owner-boundary'));
    if (kind === 'composer') {
      assert.deepEqual(JSON.parse(request.init.body), { session: 'session-a', invitation_links: [link] });
    } else if (request) {
      assert.equal(JSON.parse(request.init.body).invitation_links, undefined);
    }
    assert.ok(!h.requests.some(r => r.init.body?.includes('private owner prose')));
  }
});

test('invitation extraction has a count and link length bound', async () => {
  const h = await harness();
  const event = { text: Array(40).fill(link).join('\n'), origin: { kind: 'composer' } };
  await h.hooks.get('prompt.submit')(h.$, event, async value => value);
  const request = h.requests.find(r => r.url.endsWith('/owner-boundary'));
  assert.equal(JSON.parse(request.init.body).invitation_links.length, 32);
});
