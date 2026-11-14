// Content script: watches the open conversation and hands it to the
// background worker, which is the only code that talks to Cortex. It never
// modifies the page and never shows anything in it.
(function () {
  'use strict';
  const { ext, load } = CortexSettings;
  const { fingerprint, backoff } = CortexUtil;

  const DEBOUNCE_MS = 3000;      // quiet time after the last DOM change
  const MIN_INTERVAL_MS = 5000;  // never send more often than this
  const URL_POLL_MS = 1000;      // SPA navigation does not fire page events

  const site = (globalThis.CortexSites || []).find((s) => s.matches(location));
  if (!site) return;

  let settings = null;
  let convId = null;
  let timer = null;
  let sentHash = {};            // conversation id -> fingerprint last accepted by Cortex
  let lastSentAt = 0;           // ms, last accepted send for the current conversation
  let lastAttemptAt = 0;
  let failures = 0;
  let status = 'idle';          // idle | saved | not-conversation | off | paused | down | bad-code
  let pending = null;           // last extracted conversation not yet accepted
  let inFlight = false;

  function enabled() {
    return settings && !settings.paused && settings.sites[site.id] !== false;
  }

  function currentState() {
    if (!site.conversationId(location)) return 'not-conversation';
    if (settings && settings.paused) return 'paused';
    if (settings && settings.sites[site.id] === false) return 'off';
    return status;
  }

  function extract() {
    const id = site.conversationId(location);
    if (!id) return null;
    const messages = site.messages(document);
    if (!messages.length) return null;
    return { source: site.source, id, title: site.title(document), url: location.origin + location.pathname, messages };
  }

  function schedule(delay) {
    clearTimeout(timer);
    timer = setTimeout(() => run(false), delay);
  }

  // force: skip the debounce and rate limit (leaving the page, "Save now").
  async function run(force) {
    clearTimeout(timer);
    if (!settings) settings = await load();
    const id = site.conversationId(location);
    if (id !== convId) { convId = id; lastSentAt = 0; failures = 0; pending = null; }
    if (!id || !enabled()) return { state: currentState() };
    if (inFlight) { schedule(1000); return { state: 'pending' }; }
    if (!force && site.isStreaming && site.isStreaming(document)) { schedule(DEBOUNCE_MS); return { state: 'pending' }; }

    let conv = null;
    try { conv = extract(); } catch (e) { /* a site change must never break the page */ }
    if (conv) pending = conv; else conv = pending; // keep the last good extraction
    if (!conv || conv.id !== id) return { state: currentState() };
    if (sentHash[id] === fingerprint(conv)) return { state: currentState() };

    const wait = lastAttemptAt + (failures ? backoff(failures) : MIN_INTERVAL_MS) - Date.now();
    if (!force && wait > 0) { schedule(wait); return { state: 'pending' }; }

    inFlight = true;
    lastAttemptAt = Date.now();
    let res;
    try { res = await ext.runtime.sendMessage({ type: 'ingest', payload: conv }); }
    catch (e) { res = { ok: false, error: 'down' }; }
    inFlight = false;
    if (id !== convId) return { state: currentState() };

    if (res && res.ok) {
      sentHash[id] = fingerprint(conv);
      lastSentAt = Date.now();
      failures = 0;
      status = 'saved';
    } else if (res && (res.error === 'invalid' || res.error === 'too-large')) {
      sentHash[id] = fingerprint(conv); // retrying the same content cannot help
      status = 'rejected';
    } else {
      failures++;
      status = res && res.error === 'bad-code' ? 'bad-code' : 'down';
      schedule(backoff(failures));
    }
    return { state: currentState() };
  }

  const onChange = () => { if (enabled() && site.conversationId(location)) schedule(DEBOUNCE_MS); };

  function start() {
    new MutationObserver(onChange).observe(document.documentElement, { childList: true, subtree: true, characterData: true });
    document.addEventListener('visibilitychange', () => { if (document.visibilityState === 'hidden') run(true); });
    addEventListener('pagehide', () => run(true));
    addEventListener('popstate', onChange);
    let href = location.href;
    setInterval(() => {
      if (location.href !== href) { href = location.href; pending = null; failures = 0; status = 'idle'; onChange(); }
    }, URL_POLL_MS);
    onChange();
  }

  ext.runtime.onMessage.addListener((msg, _sender, reply) => {
    if (!msg || typeof msg.type !== 'string') return;
    if (msg.type === 'getPageState') {
      reply({ site: site.id, state: currentState(), lastSentAt: lastSentAt || null });
    } else if (msg.type === 'saveNow') {
      run(true).then((r) => reply({ site: site.id, state: r.state, lastSentAt: lastSentAt || null }));
      return true; // async reply
    }
  });

  ext.storage.onChanged.addListener((changes, area) => {
    if (area !== 'local' || !changes.settings) return;
    load().then((s) => { settings = s; if (enabled()) onChange(); });
  });

  load().then((s) => { settings = s; start(); });
})();
