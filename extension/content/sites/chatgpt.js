// ChatGPT (chatgpt.com, chat.openai.com). Only this file knows its HTML.
//
// Hooks: every message is an element with data-message-author-role
// ("user" | "assistant" | "tool" | "system") and data-message-id; assistant
// text is rendered inside a ".markdown" element, user text in
// ".whitespace-pre-wrap". Documented by community exporters, among them
// https://github.com/pionxzh/chatgpt-exporter and
// https://github.com/da-troll/DOM-Daddy . Not verified against the live site.
(function (root) {
  'use strict';
  const md = typeof module !== 'undefined' && module.exports ? require('../../lib/md.js') : root.CortexMd;

  const site = {
    id: 'chatgpt',
    source: 'chatgpt',

    matches(loc) { return /^(www\.)?(chatgpt\.com|chat\.openai\.com)$/.test(loc.hostname); },

    // /c/<id> and /g/<gizmo>/c/<id>
    conversationId(loc) {
      const m = /^(?:\/g\/[^/]+)?\/c\/([A-Za-z0-9_-]{8,})\/?$/.exec(loc.pathname);
      return m ? m[1] : null;
    },

    title(doc) {
      const t = (doc.title || '').replace(/\s*[-|]\s*ChatGPT\s*$/, '').trim();
      return /^ChatGPT$/i.test(t) ? '' : t;
    },

    isStreaming(doc) {
      return !!(doc.querySelector('[data-testid="stop-button"]') || doc.querySelector('button[aria-label="Stop streaming"]'));
    },

    skip(el) {
      // Reasoning summaries and tool/search chrome, should they appear inside a message.
      const tid = md.attr(el, 'data-testid');
      return /think|reason|citation|tool|search/i.test(tid);
    },

    messages(doc) {
      const out = [];
      for (const el of outermost(Array.from(doc.querySelectorAll('[data-message-author-role]')))) {
        const role = md.attr(el, 'data-message-author-role');
        if (role !== 'user' && role !== 'assistant') continue;
        let text;
        if (role === 'assistant') {
          const parts = outermost(Array.from(el.querySelectorAll('.markdown')));
          text = parts.length
            ? parts.map((p) => md.toMarkdown(p, site)).filter(Boolean).join('\n\n')
            : md.toMarkdown(el, site);
        } else {
          const box = el.querySelector('.whitespace-pre-wrap');
          text = box ? userText(box) : md.toMarkdown(el, site);
        }
        if (text) out.push({ role, text });
      }
      return out;
    },
  };

  // Of a list of matched elements, the ones that are not inside another one of
  // the list: a nested match would be converted twice, once on its own and once
  // as part of its container.
  function outermost(els) {
    const set = new Set(els);
    return els.filter((el) => {
      for (let p = el.parentNode; p; p = p.parentNode) if (set.has(p)) return false;
      return true;
    });
  }

  // User text is plain text with newlines; keep it as typed.
  function userText(box) { return (box.textContent || '').replace(/\r\n/g, '\n').trim(); }

  if (typeof module !== 'undefined' && module.exports) module.exports = site;
  else (root.CortexSites = root.CortexSites || []).push(site);
})(typeof globalThis !== 'undefined' ? globalThis : this);
