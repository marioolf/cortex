'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const root = path.join(__dirname, '..');
const manifest = JSON.parse(fs.readFileSync(path.join(root, 'manifest.json'), 'utf8'));

test('manifest references only existing files', () => {
  const files = new Set();
  Object.values(manifest.icons).forEach((f) => files.add(f));
  Object.values(manifest.action.default_icon).forEach((f) => files.add(f));
  files.add(manifest.background.service_worker);
  files.add(manifest.action.default_popup);
  files.add(manifest.options_ui.page);
  manifest.content_scripts.forEach((c) => c.js.forEach((f) => files.add(f)));
  for (const f of files) assert.ok(fs.existsSync(path.join(root, f)), 'missing ' + f);
});

test('permissions stay minimal', () => {
  assert.deepEqual(manifest.permissions, ['storage']);
  assert.deepEqual(manifest.host_permissions, ['http://127.0.0.1/*']);
  const matches = manifest.content_scripts.flatMap((c) => c.matches).sort();
  assert.deepEqual(matches, ['https://chat.openai.com/*', 'https://chatgpt.com/*', 'https://claude.ai/*', 'https://gemini.google.com/*']);
  assert.ok(!/unsafe|http:|https:/.test(manifest.content_security_policy.extension_pages));
});

test('html pages load only local scripts and no remote resources', () => {
  for (const f of ['options/options.html', 'popup/popup.html']) {
    const html = fs.readFileSync(path.join(root, f), 'utf8');
    assert.ok(!/(src|href)="https?:/.test(html), f);
    assert.ok(!/<script(?![^>]*src=)/.test(html), 'inline script in ' + f);
    for (const m of html.matchAll(/(?:src|href)="([^"#]+)"/g)) assert.ok(fs.existsSync(path.join(root, path.dirname(f), m[1])), f + ' -> ' + m[1]);
  }
});

test('source files contain no fetch to anything but the local worker call', () => {
  for (const f of ['background.js', 'content/content.js', 'popup/popup.js', 'options/options.js']) {
    const src = fs.readFileSync(path.join(root, f), 'utf8');
    const n = (src.match(/\bfetch\(/g) || []).length;
    assert.equal(n, f === 'background.js' ? 1 : 0, f);
    assert.ok(!/XMLHttpRequest|WebSocket|sendBeacon|eval\(|new Function/.test(src), f);
  }
});
