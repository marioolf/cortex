'use strict';
const { ext, load, save, validPort } = CortexSettings;
const $ = (id) => document.getElementById(id);
const SITES = ['chatgpt', 'claude', 'gemini'];
let saveTimer = null;

function read() {
  return {
    port: validPort($('port').value) ? Number($('port').value) : 47821,
    code: $('code').value.trim(),
    paused: $('paused').checked,
    sites: Object.fromEntries(SITES.map((s) => [s, $('site-' + s).checked])),
  };
}

function queueSave() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(async () => {
    await save(read());
    $('saved').textContent = 'Saved.';
    setTimeout(() => { $('saved').textContent = ''; }, 1500);
  }, 300);
}

const MESSAGES = {
  'no-code': ['err', 'Enter the pairing code first.'],
  'bad-port': ['err', 'The port must be a number from 1 to 65535.'],
  'bad-code': ['err', 'Wrong pairing code. Copy it again from the Cortex panel.'],
  down: ['err', 'Cortex is not running on this port.'],
};

async function test() {
  const r = $('result');
  r.className = ''; r.textContent = 'Testing...';
  const s = read();
  await save(s);
  const res = await ext.runtime.sendMessage({ type: 'ping', port: s.port, code: s.code, record: true });
  if (res && res.ok) { r.className = 'ok'; r.textContent = 'Connected' + (res.version ? ' (Cortex ' + res.version + ')' : '') + '.'; return; }
  const [cls, text] = MESSAGES[res && res.error] || ['err', 'Unexpected answer from Cortex (' + ((res && res.error) || 'no reply') + ').'];
  r.className = cls; r.textContent = text;
}

(async () => {
  const s = await load();
  $('code').value = s.code;
  $('port').value = s.port;
  $('paused').checked = s.paused;
  for (const k of SITES) $('site-' + k).checked = s.sites[k] !== false;
  for (const el of document.querySelectorAll('input')) el.addEventListener('input', queueSave);
  $('show').addEventListener('click', () => {
    const hidden = $('code').type === 'password';
    $('code').type = hidden ? 'text' : 'password';
    $('show').textContent = hidden ? 'Hide' : 'Show';
  });
  $('test').addEventListener('click', test);
})();
