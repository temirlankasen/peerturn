const VERSION = '2.1.306';
const MAX_FRAME = 262144;
let runtime = null;
let working = null;
let turnGeneration = 0;

function object(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function identifier(value) {
  return typeof value === 'string' && /^[A-Za-z0-9._:-]{1,160}$/.test(value);
}

async function status($, text) {
  try { await $.ui.status(text); } catch { /* UI failure does not affect the owner. */ }
}

async function stop($, state, reason) {
  if (state.active && state.url && reason === 'binding_lost') {
    void request($, state, '/close', {}).catch(() => {});
  }
  state.active = false;
  state.timer?.cancel();
  await status($, reason);
}

async function request($, state, path, body) {
  if (!state.active || !state.url) throw new Error('adapter_unavailable');
  const response = await $.http.fetch(state.url + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: {
      Authorization: 'Bearer ' + state.capability,
      'Content-Type': 'application/json',
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  if (typeof response.text !== 'string' || response.text.length > MAX_FRAME) {
    throw new Error('adapter_protocol_error');
  }
  const value = JSON.parse(response.text);
  if (!response.ok || response.status !== 200) {
    const lost = object(value) && ['binding_lost', 'needs_rebind'].includes(value.error);
    throw new Error(lost ? 'binding_lost' : 'adapter_protocol_error');
  }
  return value;
}

async function retryLater($, state, error, reason = 'adapter_unavailable') {
  if (error?.message === 'binding_lost') return stop($, state, 'binding_lost');
  // Keep retries within the daemon's 20-second lease and helper inactivity bound.
  state.failures = Math.min(state.failures + 1, 4);
  state.backoff = 2 ** state.failures;
  await status($, reason);
}

async function release($, state, frame) {
  await request($, state, '/release', { message_id: frame.message_id, attempt_id: frame.attempt_id });
  state.release = false;
}

async function confirm($, state, nonce) {
  const session = await $.session.id();
  if (!state.active || session !== state.session) return;
  await request($, state, '/confirm', { nonce, session });
}

async function confirmLater($, state, nonce) {
  try { await confirm($, state, nonce); } catch { await status($, 'binding_unconfirmed'); }
}

async function deliver($, state, frame) {
  state.submitting = true;
  let submitted = false;
  try {
    if (state.release) await release($, state, frame);
    state.release = true;
    const admission = await request($, state, '/authorize', {
      message_id: frame.message_id, attempt_id: frame.attempt_id,
    });
    if (object(admission) && admission.authorized === false) {
      // The daemon already committed the terminal per-item rejection.
      state.pending = null;
      state.release = false;
      return;
    }
    if (!object(admission) || admission.authorized !== true) throw new Error('adapter_protocol_error');
    let result = { drop: 'admission_refused' };
    if (object(admission) && admission.authorized === true && state.active) {
      const session = await $.session.id();
      // No native submission happened, so this reservation can safely be released.
      if (working !== false || session !== state.session) {
        await release($, state, frame);
        if (session !== state.session) await stop($, state, 'binding_lost');
        return;
      }
      submitted = true;
      state.release = false;
      try {
        // The helper supplies the grant-aware framing; preserve plugin provenance.
        result = await $.prompt.submit({ text: frame.text });
      } catch {
        result = { error: 'submit_failed', in_doubt: true };
      }
    }
    await request($, state, '/result', {
      message_id: frame.message_id, attempt_id: frame.attempt_id, result,
    });
    state.pending = null;
    state.release = false;
  } catch (error) {
    if (submitted) {
      // A lost native result/receipt fences delivery while the binding stays live.
      state.fenced = true;
      state.pending = null;
      state.report = { message_id: frame.message_id, attempt_id: frame.attempt_id, result: { in_doubt: true } };
      await retryLater($, state, error, 'in_doubt');
    } else {
      try { await release($, state, frame); } catch { /* Retry release before authorization. */ }
      await retryLater($, state, error);
    }
  } finally {
    state.submitting = false;
  }
}

async function poll($, state) {
  if (!state.active || !state.url || state.polling) return;
  if (state.backoff > 0) { state.backoff -= 1; return; }
  state.polling = true;
  try {
    if (await $.session.id() !== state.session) {
      await stop($, state, 'binding_lost');
      return;
    }
    if (state.report) {
      // Reporting the uncertainty is retryable; the native submission never is.
      await request($, state, '/result', state.report);
      state.report = null;
    }
    if (state.pending && !state.submitting && !state.fenced && working === false) {
      void deliver($, state, state.pending);
    }
    const frame = await request($, state, '/poll');
    state.failures = 0;
    if (!object(frame)) throw new Error('adapter_protocol_error');
    if (frame.type === 'status' && Number.isSafeInteger(frame.inform_pending)
        && frame.inform_pending >= 0 && frame.inform_pending <= 1000000) {
      if (frame.inform_pending !== state.inform) {
        state.inform = frame.inform_pending;
        await status($, 'inform: ' + frame.inform_pending + ' pending');
        if (frame.inform_pending > 0) await $.ui.toast('PeerTurn inbox has pending inform messages');
      }
      return;
    }
    if (frame.type === 'live' && state.pending && frame.message_id === state.pending.message_id
        && frame.attempt_id === state.pending.attempt_id && frame.text === state.pending.text) return;
    if (frame.type !== 'live' || !identifier(frame.message_id) || !identifier(frame.attempt_id)
        || typeof frame.text !== 'string' || frame.text.length === 0
        || new TextEncoder().encode(frame.text).length > 128 * 1024 || state.pending) {
      throw new Error('adapter_protocol_error');
    }
    // Only the daemon can resume admission after trusted owner reconciliation.
    state.fenced = false;
    state.pending = frame;
    if (working === false) void deliver($, state, frame);
  } catch (error) {
    await retryLater($, state, error, state.report ? 'in_doubt' : 'adapter_unavailable');
  } finally {
    state.polling = false;
  }
}

async function launch($, state) {
  let buffered = '';
  try {
    for await (const chunk of $.process.spawn({ argv: [
      state.binary, 'adapter', 'claude', '--native-session', state.session,
      '--runtime-version', VERSION, '--provider', state.provider,
    ] })) {
      if (!state.active) return;
      if (chunk.stream !== 'stdout') continue;
      if (typeof chunk.text !== 'string' || buffered.length + chunk.text.length > 4096) {
        throw new Error('adapter_protocol_error');
      }
      buffered += chunk.text;
      const newline = buffered.indexOf('\n');
      if (newline < 0) continue;
      const frame = JSON.parse(buffered.slice(0, newline));
      buffered = buffered.slice(newline + 1);
      if (state.url || buffered.length > 0 || !object(frame) || frame.type !== 'ready'
          || typeof frame.url !== 'string' || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]{0,4}$/.test(frame.url)
          || Number(new URL(frame.url).port) > 65535 || typeof frame.capability !== 'string'
          || !/^[a-f0-9]{64}$/.test(frame.capability)) {
        throw new Error('adapter_protocol_error');
      }
      state.url = frame.url;
      state.capability = frame.capability;
      if (state.nonce) {
        void confirmLater($, state, state.nonce);
        state.nonce = null;
      }
      state.timer = $.clock.every(500, async () => poll($, state));
    }
    if (state.active) await stop($, state, 'adapter_stopped');
  } catch {
    await stop($, state, 'adapter_unavailable');
  }
}

async function start($, options) {
  const session = await $.session.id();
  if (runtime?.active && runtime.session === session) return;
  const version = await $.session.version();
  if (version.version !== VERSION || version.base !== VERSION) {
    await status($, 'runtime_unsupported');
    return;
  }
  const binary = options.peerturn_binary;
  if (typeof binary !== 'string' || binary.length > 4096 || !binary.startsWith('/')
      || /[\u0000-\u001f\u007f]/.test(binary)) {
    await status($, 'adapter_unconfigured');
    return;
  }
  if (runtime) await stop($, runtime, 'binding_lost');
  const state = {
    session, binary, provider: await $.env.get('ANTHROPIC_BASE_URL') ? 'gateway' : 'anthropic',
    active: true, url: null, capability: null, polling: false, submitting: false,
    nonce: null, inform: -1, pending: null, timer: null,
    failures: 0, backoff: 0, fenced: false, release: false, report: null,
  };
  runtime = state;
  void launch($, state);
}

async function ownerBoundary($, e) {
  if (e.origin.kind !== 'composer' && e.origin.kind !== 'bridge') return null;
  working = true;
  turnGeneration += 1;
  const state = runtime;
  if (!state?.active || !state.url || typeof e.text !== 'string' || /^\s*\//.test(e.text)) return null;
  // Only the human composer can authorize joining. Bridge/plugin/peer input
  // cannot create an owner invitation, even on an ordinary prompt boundary.
  const links = e.origin.kind === 'composer'
    ? (e.text.match(/https:\/\/[^\s<>"`()]+/g) ?? [])
      .map(link => link.replace(/[\)\]},.;']+$/, ''))
      .filter(link => link.includes('/i/') && link.length <= 2048).slice(0, 32)
    : [];
  const result = await Promise.race([
    request($, state, '/owner-boundary', {
      session: state.session, ...(links.length ? { invitation_links: links } : {}),
    }),
    $.clock.sleep(1000).then(() => null),
  ]);
  if (!state.active || await $.session.id() !== state.session || !object(result)
      || !identifier(result.notice_id) || typeof result.context !== 'string'
      || new TextEncoder().encode(result.context).length > 4096) return null;
  // Only the daemon's count/sender notice is permitted in owner context, never bodies.
  const notice = /^PeerTurn: ([1-9]|1[0-9]|20) inform message\(s\) from ([A-Za-z0-9._:/-]+(?:, [A-Za-z0-9._:/-]+)*)\. Use peer_inbox or peerturn inbox to read bodies\.$/.exec(result.context);
  if (!notice) return null;
  const labels = notice[2].split(', ');
  if (labels.length > 20 || new Set(labels).size !== labels.length) return null;
  return { state, notice_id: result.notice_id, prompt: { ...e, context: [...(e.context ?? []), result.context] } };
}

export function register(on, options) {
  on('session.start', async ($, e, next) => {
    try { await start($, options); } catch { await status($, 'adapter_unavailable'); }
    return next(e);
  });
  on('prompt.submit', async ($, e, next) => {
    // clear/resume ends a mod session without firing session.start again.
    let prompt = e;
    let boundary = null;
    try {
      await start($, options);
      boundary = await ownerBoundary($, e);
      if (boundary) prompt = boundary.prompt;
    } catch { await status($, 'adapter_unavailable'); }
    const result = await next(prompt);
    const accepted = !object(result) || (!('drop' in result) && !('error' in result));
    if (accepted && boundary && boundary.state.active && await $.session.id() === boundary.state.session) {
      // A failed or timed-out continuation never consumes the reserved notices.
      void request($, boundary.state, '/boundary-attached', {
        session: boundary.state.session, notice_id: boundary.notice_id,
      }).catch(() => {});
    }
    return result;
  });
  on('session.receive', async ($, e, next) => {
    const match = typeof e.text === 'string' && /^<peerturn-bind>([a-f0-9]{64})<\/peerturn-bind>$/.exec(e.text);
    if (!match || e.origin.kind !== 'peer') return next(e);
    if (runtime?.active && e.origin.kind === 'peer') {
      if (runtime.url) void confirmLater($, runtime, match[1]);
      else runtime.nonce = match[1];
    }
    return { consumed: 'PeerTurn binding challenge' };
  });
  on('turn.start', async ($, e, next) => {
    if (e.agentId === undefined) {
      working = true;
      turnGeneration += 1;
    }
    return next(e);
  });
  on('turn.complete', async ($, e, next) => {
    const generation = turnGeneration;
    const result = await next(e);
    if (e.agentId === undefined && generation === turnGeneration) working = false;
    return result;
  });
  on('session.end', async ($, e, next) => {
    working = null;
    turnGeneration += 1;
    if (runtime) {
      const state = runtime;
      runtime = null;
      if (state.active && state.url) void request($, state, '/close', {}).catch(() => {});
      state.active = false;
      state.timer?.cancel();
    }
    return next(e);
  });
}
