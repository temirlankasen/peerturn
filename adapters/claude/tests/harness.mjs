import { readFile } from 'node:fs/promises';

const source = await readFile(new URL('../hooks/register.js', import.meta.url), 'utf8');

export async function harness(version = '2.1.306', idle = true, configure = () => {}) {
  const mod = await import(`data:text/javascript,${encodeURIComponent(source)}#${Math.random()}`);
  const hooks = new Map();
  const timers = [];
  const submitted = [];
  const requests = [];
  const launched = [];
  const statuses = [];
  const inbox = [];
  const $ = {
    session: {
      id: async () => 'session-a',
      version: async () => ({ version, base: version }),
    },
    env: { get: async () => undefined },
    clock: {
      every: (ms, fn) => {
        const timer = { fn, cancelled: false };
        timers.push(timer);
        return { cancel: () => { timer.cancelled = true; } };
      },
      sleep: ms => new Promise(resolve => setTimeout(resolve, ms)),
    },
    ui: { status: async text => statuses.push(text), toast: async () => {} },
    process: {
      spawn: async function* ({ argv }) {
        launched.push(argv);
        yield { stream: 'stdout', text: JSON.stringify({
          type: 'ready', url: 'http://127.0.0.1:39827', capability: 'a'.repeat(64),
        }) + '\n' };
        await new Promise(() => {});
      },
    },
    http: { fetch: async (url, init) => {
      requests.push({ url, init });
      return { ok: true, status: 200, text: JSON.stringify(
        url.endsWith('/poll') ? inbox.shift() ?? { type: 'status', inform_pending: 0 }
          : url.endsWith('/authorize') ? { authorized: true } : {},
      ) };
    } },
    prompt: { submit: async input => {
      submitted.push(input);
      return { drop: 'the session was stopped and nobody has sent input since' };
    } },
  };
  configure($);
  mod.register((name, hook) => hooks.set(name, hook), {
    peerturn_binary: '/fixture/peerturn', provider: 'fixture',
  });
  const tick = async () => {
    for (const timer of timers) if (!timer.cancelled) await timer.fn();
    await new Promise(resolve => setImmediate(resolve));
  };
  await hooks.get('session.start')?.($, {}, async value => value);
  await tick();
  if (idle) await hooks.get('turn.complete')?.($, {}, async value => value);
  return { $, hooks, tick, inbox, submitted, requests, launched, statuses, timers };
}

