// Small pure helpers shared by the content script and the tests.
(function (root) {
  'use strict';

  // 53-bit string hash (cyrb53). Only used to notice that a conversation changed.
  function hash(str) {
    let h1 = 0xdeadbeef, h2 = 0x41c6ce57;
    for (let i = 0; i < str.length; i++) {
      const c = str.charCodeAt(i);
      h1 = Math.imul(h1 ^ c, 2654435761);
      h2 = Math.imul(h2 ^ c, 1597334677);
    }
    h1 = Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^ Math.imul(h2 ^ (h2 >>> 13), 3266489909);
    h2 = Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^ Math.imul(h1 ^ (h1 >>> 13), 3266489909);
    return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(36);
  }

  // Fingerprint of what would be sent: id, title and every message.
  function fingerprint(conv) {
    return hash(JSON.stringify([conv.id, conv.title, conv.messages.map((m) => [m.role, m.text])]));
  }

  // Retry delay in ms after n consecutive failures: 10 s, 20 s, 40 s ... 5 min.
  function backoff(failures) {
    return Math.min(300000, 10000 * Math.pow(2, Math.max(0, failures - 1)));
  }

  const MIN_INTERVAL_MS = 5000;     // never send more often than this
  const SKIPPED_RETRY_MS = 60000;   // while Cortex is paused or the site is off in Cortex

  const MESSAGES = {
    notPaired: "Not paired yet: paste the code from the Cortex panel in the extension's options",
    skipped: 'Cortex is paused or this site is turned off in Cortex',
  };

  // Without a pairing code nothing can be sent, so nothing is tried or retried.
  function paired(settings) {
    return !!(settings && typeof settings.code === 'string' && settings.code.trim());
  }

  // What the content script does with the background worker's answer to a push.
  //   sent:    the conversation is remembered as saved (never sent again unchanged)
  //   status:  what the popup shows
  //   failures: the new consecutive failure count
  //   gap:     minimum ms before the next attempt; null means do not retry by timer
  // Cortex answering "skipped" (paused, or the source is off in Cortex) is not
  // a save: it is retried slowly so the conversation is stored once the user
  // resumes, without hammering.
  function outcome(res, failures) {
    if (res && res.ok) {
      if (res.result === 'skipped') return { sent: false, status: 'skipped', failures: 0, gap: SKIPPED_RETRY_MS };
      return { sent: true, status: 'saved', failures: 0, gap: MIN_INTERVAL_MS };
    }
    const error = res && res.error;
    if (error === 'invalid' || error === 'too-large') return { sent: true, status: 'rejected', failures, gap: MIN_INTERVAL_MS }; // retrying the same content cannot help
    if (error === 'no-code') return { sent: false, status: 'no-code', failures: 0, gap: null };
    const n = failures + 1;
    return { sent: false, status: error === 'bad-code' ? 'bad-code' : 'down', failures: n, gap: backoff(n) };
  }

  // Minimum wait before the next send, given how the last attempt ended.
  function nextGap(status, failures) {
    if (status === 'skipped') return SKIPPED_RETRY_MS;
    return failures ? backoff(failures) : MIN_INTERVAL_MS;
  }

  // Toolbar badge for the error last seen by the worker ('' = none).
  function badge(error) {
    switch (error) {
      case 'down': return { bad: true, title: 'Cortex: not running' };
      case 'no-code': return { bad: true, title: 'Cortex: ' + MESSAGES.notPaired };
      case 'bad-code': case 'bad-port': return { bad: true, title: 'Cortex: check pairing code and port' };
      default: return { bad: false, title: 'Cortex' };
    }
  }

  const api = { hash, fingerprint, backoff, paired, outcome, nextGap, badge, MESSAGES, MIN_INTERVAL_MS, SKIPPED_RETRY_MS };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.CortexUtil = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
