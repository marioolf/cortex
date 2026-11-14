'use strict';
// All HTML below is invented; it only imitates the hooks each extractor relies on.
const test = require('node:test');
const assert = require('node:assert/strict');
const { document } = require('./fakedom.js');
const chatgpt = require('../content/sites/chatgpt.js');
const claude = require('../content/sites/claude.js');
const gemini = require('../content/sites/gemini.js');

const loc = (host, pathname) => ({ hostname: host, pathname });

test('chatgpt: url handling', () => {
  assert.ok(chatgpt.matches(loc('chatgpt.com', '/')));
  assert.ok(chatgpt.matches(loc('chat.openai.com', '/')));
  assert.ok(!chatgpt.matches(loc('example.com', '/')));
  assert.equal(chatgpt.conversationId(loc('chatgpt.com', '/c/6750a1b2-aaaa-bbbb-cccc-123456789abc')), '6750a1b2-aaaa-bbbb-cccc-123456789abc');
  assert.equal(chatgpt.conversationId(loc('chatgpt.com', '/g/g-abc123-helper/c/6750a1b2-aaaa-bbbb-cccc-123456789abc')), '6750a1b2-aaaa-bbbb-cccc-123456789abc');
  assert.equal(chatgpt.conversationId(loc('chatgpt.com', '/')), null);
  assert.equal(chatgpt.conversationId(loc('chatgpt.com', '/c/')), null);
  assert.equal(chatgpt.conversationId(loc('chatgpt.com', '/gpts')), null);
  assert.equal(chatgpt.title({ title: 'Pasta sauce - ChatGPT' }), 'Pasta sauce');
  assert.equal(chatgpt.title({ title: 'ChatGPT' }), '');
});

test('chatgpt: messages', () => {
  const doc = document(`
    <main>
      <article><div data-message-author-role="user" data-message-id="m1"><div class="whitespace-pre-wrap">How long to simmer?
Two lines.</div></div></article>
      <article>
        <div data-message-author-role="assistant" data-message-id="m2">
          <div class="markdown prose"><p>About <strong>20 minutes</strong>.</p>
          <pre><div><span>bash</span><button>Copy code</button></div><code class="language-bash">timer 20m</code></pre></div>
        </div>
        <button data-testid="copy-turn-action-button">Copy</button>
      </article>
      <div data-message-author-role="tool" data-message-id="m3"><div>tool output</div></div>
    </main>`, { title: 'x' });
  assert.deepEqual(chatgpt.messages(doc), [
    { role: 'user', text: 'How long to simmer?\nTwo lines.' },
    { role: 'assistant', text: 'About **20 minutes**.\n\n```bash\ntimer 20m\n```' },
  ]);
  assert.equal(chatgpt.isStreaming(doc), false);
  assert.equal(chatgpt.isStreaming(document('<button data-testid="stop-button">Stop</button>')), true);
});

test('claude: url handling', () => {
  const id = '0a1b2c3d-1111-2222-3333-444455556666';
  assert.ok(claude.matches(loc('claude.ai', '/')));
  assert.equal(claude.conversationId(loc('claude.ai', '/chat/' + id)), id);
  assert.equal(claude.conversationId(loc('claude.ai', '/chat/new')), null);
  assert.equal(claude.conversationId(loc('claude.ai', '/recents')), null);
  assert.equal(claude.title({ title: 'Haiku help - Claude' }), 'Haiku help');
  assert.equal(claude.title({ title: 'Claude' }), '');
});

test('claude: messages, nested containers deduplicated, thinking skipped', () => {
  const doc = document(`
    <div>
      <div data-testid="user-message" class="font-user-message"><p>Write a haiku</p></div>
      <div class="font-claude-response">
        <div data-testid="thinking-block"><p>private reasoning</p></div>
        <div class="font-claude-message"><p>Rain on the roof,</p><p>soft taps.</p><ul><li>one</li></ul></div>
      </div>
      <div data-testid="user-message"><p>Thanks</p></div>
    </div>`);
  assert.deepEqual(claude.messages(doc), [
    { role: 'user', text: 'Write a haiku' },
    { role: 'assistant', text: 'Rain on the roof,\n\nsoft taps.\n\n- one' },
    { role: 'user', text: 'Thanks' },
  ]);
  assert.equal(claude.isStreaming(document('<div data-is-streaming="true"></div>')), true);
});

test('gemini: url handling', () => {
  assert.ok(gemini.matches(loc('gemini.google.com', '/app')));
  assert.equal(gemini.conversationId(loc('gemini.google.com', '/app/c8f3a1b2c3d4e5f6')), 'c8f3a1b2c3d4e5f6');
  assert.equal(gemini.conversationId(loc('gemini.google.com', '/u/1/app/c8f3a1b2c3d4e5f6')), 'c8f3a1b2c3d4e5f6');
  assert.equal(gemini.conversationId(loc('gemini.google.com', '/app')), null);
  assert.equal(gemini.title({ title: 'Trip plan - Google Gemini' }), 'Trip plan');
  assert.equal(gemini.title({ title: 'Google Gemini' }), '');
});

test('gemini: messages', () => {
  const doc = document(`
    <chat-window>
      <user-query><span class="cdk-visually-hidden">You said</span><div class="query-text"><p class="query-text-line">Plan a trip</p><p class="query-text-line">to Lisbon</p></div></user-query>
      <model-response>
        <model-thoughts><div>thinking...</div></model-thoughts>
        <message-content><div class="markdown"><h3>Day 1</h3><p>Walk <b>Alfama</b>.</p></div></message-content>
        <sources-carousel>sources</sources-carousel>
      </model-response>
    </chat-window>`);
  assert.deepEqual(gemini.messages(doc), [
    { role: 'user', text: 'Plan a trip\nto Lisbon' },
    { role: 'assistant', text: '### Day 1\n\nWalk **Alfama**.' },
  ]);
});

test('empty pages give no messages', () => {
  for (const s of [chatgpt, claude, gemini]) assert.deepEqual(s.messages(document('<div>hello</div>')), []);
});
