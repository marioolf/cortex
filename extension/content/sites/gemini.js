// Gemini (gemini.google.com). Only this file knows its HTML.
//
// Hooks: Angular custom elements, <user-query> (text in .query-text) and
// <model-response> (rendered answer in <message-content> / .markdown).
// Documented by community exporters such as
// https://github.com/da-troll/DOM-Daddy . Not verified against the live site.
(function (root) {
  'use strict';
  const md = typeof module !== 'undefined' && module.exports ? require('../../lib/md.js') : root.CortexMd;

  const site = {
    id: 'gemini',
    source: 'gemini',

    matches(loc) { return loc.hostname === 'gemini.google.com'; },

    // /app/<id>, /u/1/app/<id>, /gem/<gem>/<id>
    conversationId(loc) {
      const m = /^(?:\/u\/\d+)?(?:\/app|\/gem\/[^/]+)\/([A-Za-z0-9_-]{6,})\/?$/.exec(loc.pathname);
      return m ? m[1] : null;
    },

    title(doc) {
      const t = (doc.title || '').replace(/\s*[-|]\s*(Google )?Gemini\s*$/, '').trim();
      return /^(Google )?Gemini$/i.test(t) ? '' : t;
    },

    isStreaming(doc) {
      return !!(doc.querySelector('button[aria-label="Stop response"]') || doc.querySelector('button.stop'));
    },

    skip(el) {
      const t = md.tag(el).toLowerCase();
      return md.hasClass(el, 'cdk-visually-hidden') || md.hasClass(el, 'thoughts-container') ||
        t === 'model-thoughts' || t === 'sources-carousel' || t === 'source-footnote';
    },

    messages(doc) {
      const out = [];
      for (const el of doc.querySelectorAll('user-query, model-response')) {
        const user = md.tag(el) === 'USER-QUERY';
        let text;
        if (user) {
          const q = el.querySelectorAll('.query-text-line');
          if (q.length) text = Array.from(q).map((l) => (l.textContent || '').trim()).join('\n').trim();
          else text = md.toMarkdown(el.querySelector('.query-text') || el, site);
        } else {
          const body = el.querySelector('message-content') || el.querySelector('.markdown') || el;
          text = md.toMarkdown(body, site);
        }
        if (text) out.push({ role: user ? 'user' : 'assistant', text });
      }
      return out;
    },
  };

  if (typeof module !== 'undefined' && module.exports) module.exports = site;
  else (root.CortexSites = root.CortexSites || []).push(site);
})(typeof globalThis !== 'undefined' ? globalThis : this);
