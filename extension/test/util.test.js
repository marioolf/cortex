'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { fingerprint, backoff } = require('../lib/util.js');

test('fingerprint changes with content, not with identity of objects', () => {
  const a = { id: '1', title: 't', messages: [{ role: 'user', text: 'hi' }] };
  const b = JSON.parse(JSON.stringify(a));
  assert.equal(fingerprint(a), fingerprint(b));
  b.messages.push({ role: 'assistant', text: 'yo' });
  assert.notEqual(fingerprint(a), fingerprint(b));
  const c = JSON.parse(JSON.stringify(a)); c.title = 'other';
  assert.notEqual(fingerprint(a), fingerprint(c));
});

test('backoff doubles and caps', () => {
  assert.deepEqual([1, 2, 3, 4, 10].map(backoff), [10000, 20000, 40000, 80000, 300000]);
});

const { paired, outcome, nextGap, badge, MESSAGES, SKIPPED_RETRY_MS, MIN_INTERVAL_MS } = require('../lib/util.js');

test('"skipped" is not saved: it is retried slowly, never marked as sent', () => {
  const o = outcome({ ok: true, result: 'skipped' }, 3);
  assert.equal(o.sent, false);
  assert.equal(o.status, 'skipped');
  assert.equal(o.failures, 0);
  assert.equal(o.gap, 60000);
  assert.equal(nextGap('skipped', 0), SKIPPED_RETRY_MS);
  assert.equal(MESSAGES.skipped, 'Cortex is paused or this site is turned off in Cortex');
  // Once the user resumes, the same answer path saves it.
  const saved = outcome({ ok: true, result: 'created' }, 0);
  assert.deepEqual([saved.sent, saved.status], [true, 'saved']);
  assert.equal(outcome({ ok: true }, 0).sent, true);
});

test('failures back off, rejections are final, a missing code is not retried', () => {
  assert.deepEqual(outcome({ ok: false, error: 'down' }, 0), { sent: false, status: 'down', failures: 1, gap: 10000 });
  assert.deepEqual(outcome({ ok: false, error: 'down' }, 2), { sent: false, status: 'down', failures: 3, gap: 40000 });
  assert.equal(outcome({ ok: false, error: 'bad-code' }, 0).status, 'bad-code');
  assert.equal(outcome(undefined, 0).status, 'down');
  for (const error of ['invalid', 'too-large']) {
    const o = outcome({ ok: false, error }, 1);
    assert.deepEqual([o.sent, o.status, o.failures], [true, 'rejected', 1]);
  }
  const n = outcome({ ok: false, error: 'no-code' }, 4);
  assert.deepEqual(n, { sent: false, status: 'no-code', failures: 0, gap: null });
  assert.equal(nextGap('saved', 0), MIN_INTERVAL_MS);
  assert.equal(nextGap('down', 2), 20000);
});

test('without a pairing code nothing runs and the message says why', () => {
  assert.equal(paired({ code: '' }), false);
  assert.equal(paired({ code: '   ' }), false);
  assert.equal(paired({}), false);
  assert.equal(paired(null), false);
  assert.equal(paired({ code: 'abc' }), true);
  assert.equal(MESSAGES.notPaired, "Not paired yet: paste the code from the Cortex panel in the extension's options");
  const b = badge('no-code');
  assert.equal(b.bad, true);
  assert.ok(b.title.includes(MESSAGES.notPaired) && !/not running/.test(b.title));
  assert.match(badge('down').title, /not running/);
  assert.deepEqual(badge(null), { bad: false, title: 'Cortex' });
});
