// Settings live in chrome.storage.local only (never sync: the pairing code
// must stay on this machine).
(function (root) {
  'use strict';

  const ext = root.browser || root.chrome;
  const DEFAULTS = {
    port: 47821,
    code: '',
    paused: false,
    sites: { chatgpt: true, claude: true, gemini: true },
  };

  async function load() {
    const got = await ext.storage.local.get('settings');
    const s = got.settings || {};
    return {
      port: validPort(s.port) ? Number(s.port) : DEFAULTS.port,
      code: typeof s.code === 'string' ? s.code.trim() : '',
      paused: !!s.paused,
      sites: Object.assign({}, DEFAULTS.sites, s.sites || {}),
    };
  }

  function save(s) { return ext.storage.local.set({ settings: s }); }

  function validPort(p) {
    const n = Number(p);
    return Number.isInteger(n) && n >= 1 && n <= 65535;
  }

  // Cortex only ever listens on the loopback address.
  const baseUrl = (port) => 'http://127.0.0.1:' + port;

  root.CortexSettings = { ext, DEFAULTS, load, save, validPort, baseUrl };
})(globalThis);
