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
