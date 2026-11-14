// Service worker: the only place that talks to Cortex (127.0.0.1). Content
// scripts run on https pages, where fetching http://127.0.0.1 is subject to
// mixed-content and private-network rules; the worker is not.
importScripts('lib/settings.js');
const { ext, load, validPort, baseUrl } = CortexSettings;

const TIMEOUT_MS = 10000;

async function call(settings, path, init) {
  if (!validPort(settings.port)) return { ok: false, error: 'bad-port' };
  if (!settings.code) return { ok: false, error: 'no-code' };
  const ctl = new AbortController();
  const t = setTimeout(() => ctl.abort(), TIMEOUT_MS);
  try {
    const r = await fetch(baseUrl(settings.port) + path, Object.assign({
      signal: ctl.signal,
      cache: 'no-store',
      credentials: 'omit',
      headers: Object.assign({ 'X-Cortex-Extension': settings.code }, (init && init.headers) || {}),
    }, init && { method: init.method, body: init.body }));
    let body = null;
    try { body = await r.json(); } catch (e) { /* not JSON */ }
    if (r.status === 200) return { ok: true, body };
    const error = { 400: 'invalid', 403: 'bad-code', 413: 'too-large' }[r.status] || 'http-' + r.status;
    return { ok: false, error };
  } catch (e) {
    return { ok: false, error: 'down' };
  } finally {
    clearTimeout(t);
  }
}

// Remembered for the popup, and shown as a badge when the user has to act.
async function setStatus(error, extra) {
  const state = error ? error : 'ok';
  await ext.storage.local.set({ status: Object.assign({ state, at: Date.now() }, extra || {}) });
  const bad = error === 'down' || error === 'bad-code' || error === 'no-code' || error === 'bad-port';
  await ext.action.setBadgeText({ text: bad ? '!' : '' });
  if (bad) await ext.action.setBadgeBackgroundColor({ color: '#b3261e' });
  await ext.action.setTitle({ title: 'Cortex' + (bad ? ': ' + (error === 'down' ? 'not running' : 'check pairing code and port') : '') });
}

async function ingest(payload) {
  const s = await load();
  if (s.paused || s.sites[siteOf(payload)] === false) return { ok: false, error: 'off' };
  const r = await call(s, '/api/ext/ingest', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  await setStatus(r.ok || r.error === 'invalid' || r.error === 'too-large' ? null : r.error);
  return r.ok ? { ok: true, result: r.body && r.body.result } : r;
}

const siteOf = (p) => ({ chatgpt: 'chatgpt', 'claude-ai': 'claude', gemini: 'gemini' })[p && p.source];

async function ping(port, code) {
  const r = await call({ port, code }, '/api/ext/ping');
  if (r.ok) return { ok: true, version: (r.body && r.body.version) || '' };
  return r;
}

ext.runtime.onMessage.addListener((msg, sender, reply) => {
  // Only our own content scripts and pages may use the worker.
  if (sender.id !== ext.runtime.id || !msg) return;
  if (msg.type === 'ingest') {
    ingest(msg.payload).then(reply, () => reply({ ok: false, error: 'down' }));
    return true;
  }
  if (msg.type === 'ping') {
    ping(msg.port, msg.code).then((r) => {
      if (msg.record) setStatus(r.ok ? null : r.error, r.ok ? { version: r.version } : {});
      reply(r);
    });
    return true;
  }
});
