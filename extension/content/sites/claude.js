// claude.ai. Only this file knows its HTML.
//
// Hooks: user text is in [data-testid="user-message"]; assistant text in an
// element with class font-claude-message (older builds: font-claude-response).
// Streaming shows [data-is-streaming="true"] on the message or a "Stop
// response" button. Documented by community exporters such as
// https://github.com/da-troll/DOM-Daddy and the Greasy Fork script
// https://greasyfork.org/scripts/499310-claude-ai-sharegpt-exporter .
// Not verified against the live site.
(function (root) {
  'use strict';
  const md = typeof module !== 'undefined' && module.exports ? require('../../lib/md.js') : root.CortexMd;

  const site = {
    id: 'claude',
    source: 'claude-ai',

    matches(loc) { return loc.hostname === 'claude.ai'; },

    conversationId(loc) {
      const m = /^\/chat\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\/?$/i.exec(loc.pathname);
      return m ? m[1].toLowerCase() : null;
    },

    title(doc) {
      const t = (doc.title || '').replace(/\s*[-|]\s*Claude\s*$/, '').trim();
      return /^Claude$/i.test(t) ? '' : t;
    },

    isStreaming(doc) {
      return !!(doc.querySelector('[data-is-streaming="true"]') || doc.querySelector('button[aria-label="Stop response"]'));
    },

    // Thinking, tool use, search results, artifacts and citations are separate blocks.
    skip(el) { return /think|tool|search|artifact|citation/i.test(md.attr(el, 'data-testid')); },

    messages(doc) {
      const found = Array.from(doc.querySelectorAll('[data-testid="user-message"], .font-claude-message, .font-claude-response'));
      const set = new Set(found);
      const out = [];
      for (const el of found) {
        if (insideAny(el, set)) continue; // font-claude-response may wrap font-claude-message
        const role = md.attr(el, 'data-testid') === 'user-message' ? 'user' : 'assistant';
        const text = md.toMarkdown(el, site);
        if (text) out.push({ role, text });
      }
      return out;
    },
  };

  function insideAny(el, set) {
    for (let p = el.parentNode; p; p = p.parentNode) if (set.has(p)) return true;
    return false;
  }

  if (typeof module !== 'undefined' && module.exports) module.exports = site;
  else (root.CortexSites = root.CortexSites || []).push(site);
})(typeof globalThis !== 'undefined' ? globalThis : this);
