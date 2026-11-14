'use strict';
const { ext, load } = CortexSettings;
const { MESSAGES } = CortexUtil;
const $ = (id) => document.getElementById(id);

const CONN = {
  down: ['err', 'Cortex is not running.'],
  'bad-code': ['err', 'Wrong pairing code.'],
  'no-code': ['err', MESSAGES.notPaired + '.'],
  'bad-port': ['err', 'Invalid port. Open Options.'],
};

function ago(ms) {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 60) return s + ' seconds ago';
  const m = Math.round(s / 60);
  return m + (m === 1 ? ' minute ago' : ' minutes ago');
}

function pageText(r) {
  if (!r) return 'This page: not a conversation.';
  switch (r.state) {
    case 'saved': return 'This page: saved ' + (r.lastSentAt ? ago(r.lastSentAt) : 'just now') + '.';
    case 'off': return 'This page: site turned off.';
    case 'paused': return 'Saving is paused.';
    case 'skipped': return 'This page: ' + MESSAGES.skipped + '.';
    case 'no-code': return 'This page: ' + MESSAGES.notPaired + '.';
    case 'down': return 'This page: Cortex not running.';
    case 'bad-code': return 'This page: wrong pairing code.';
    case 'rejected': return 'This page: Cortex refused it (too large or invalid).';
    case 'pending': return 'This page: saving...';
    case 'not-conversation': return 'This page: not a conversation.';
    default: return r.lastSentAt ? 'This page: saved ' + ago(r.lastSentAt) + '.' : 'This page: not saved yet.';
  }
}

async function pageState(type) {
  try {
    const [tab] = await ext.tabs.query({ active: true, currentWindow: true });
    return await ext.tabs.sendMessage(tab.id, { type });
  } catch (e) {
    return null; // no content script here: not one of the supported sites
  }
}

(async () => {
  $('opts').addEventListener('click', (e) => { e.preventDefault(); ext.runtime.openOptionsPage(); window.close(); });

  const s = await load();
  const res = await ext.runtime.sendMessage({ type: 'ping', port: s.port, code: s.code, record: true });
  if (res && res.ok) { $('conn').className = 'ok'; $('conn').textContent = 'Connected' + (res.version ? ' (Cortex ' + res.version + ')' : '') + '.'; }
  else { const [c, t] = CONN[res && res.error] || ['err', 'Cortex answered unexpectedly.']; $('conn').className = c; $('conn').textContent = t; }

  const r = await pageState('getPageState');
  $('page').textContent = pageText(r);
  const canSave = r && r.state !== 'not-conversation' && r.state !== 'off' && r.state !== 'paused' && r.state !== 'no-code' && res && res.ok;
  $('save').disabled = !canSave;
  $('save').addEventListener('click', async () => {
    $('save').disabled = true;
    $('page').textContent = 'This page: saving...';
    $('page').textContent = pageText(await pageState('saveNow'));
    $('save').disabled = false;
  });
})();
