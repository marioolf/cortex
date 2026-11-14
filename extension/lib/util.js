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

  const api = { hash, fingerprint, backoff };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.CortexUtil = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
