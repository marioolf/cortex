// HTML to Markdown, written against a minimal node interface so that it runs
// in the page and, in tests, on a fake DOM without a browser:
//   node.nodeType (1 element, 3 text), node.tagName, node.childNodes,
//   node.textContent, node.getAttribute(name).
// Nothing here reads innerText, styles or layout.
(function (root) {
  'use strict';

  const IGNORED = new Set(['BUTTON', 'SVG', 'SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE', 'INPUT', 'SELECT', 'TEXTAREA', 'CANVAS', 'IFRAME', 'HEAD', 'AUDIO', 'VIDEO', 'LINK', 'META']);
  const INLINE = new Set(['A', 'SPAN', 'STRONG', 'B', 'EM', 'I', 'CODE', 'DEL', 'S', 'STRIKE', 'U', 'MARK', 'SUP', 'SUB', 'SMALL', 'KBD', 'ABBR', 'CITE', 'Q', 'LABEL', 'BR', 'IMG', 'PICTURE', 'TIME', 'FONT', 'BDI', 'BDO', 'WBR', 'VAR', 'SAMP', 'DFN', 'INS']);

  // Deeper than this, a subtree is taken as its plain text: the recursion below
  // would otherwise overflow the stack on a hostile or broken page, and the
  // conversation must still be saved.
  const MAX_DEPTH = 500;
  const flat = (n) => (n.textContent || '').replace(/\s+/g, ' ').trim();

  const tag = (n) => (n.tagName || '').toUpperCase();
  const attr = (n, a) => (n.getAttribute ? n.getAttribute(a) : null) || '';
  const isEl = (n) => n.nodeType === 1;
  const kids = (n) => Array.from(n.childNodes || []);
  function hasClass(n, c) {
    return (' ' + attr(n, 'class').replace(/\s+/g, ' ') + ' ').indexOf(' ' + c + ' ') >= 0;
  }
  function find(n, pred, depth) {
    depth = depth || 0;
    if (depth >= MAX_DEPTH) return null;
    for (const k of kids(n)) {
      if (!isEl(k)) continue;
      if (pred(k)) return k;
      const f = find(k, pred, depth + 1);
      if (f) return f;
    }
    return null;
  }

  function skipped(n, ctx) {
    if (IGNORED.has(tag(n))) return true;
    if (n.getAttribute && n.getAttribute('hidden') !== null && n.getAttribute('hidden') !== undefined) return true;
    return !!(ctx.skip && ctx.skip(n));
  }

  // ---- inline ----
  function inlineChildren(n, ctx) {
    let s = '';
    for (const k of kids(n)) s += inline(k, ctx);
    return s;
  }

  // Wrap text in a mark, keeping surrounding spaces outside it.
  function wrap(text, mark) {
    const m = /^(\s*)([\s\S]*?)(\s*)$/.exec(text);
    if (!m[2]) return text;
    return m[1] + mark + m[2] + mark + m[3];
  }

  function codeSpan(text) {
    text = text.replace(/\n/g, ' ');
    let fence = '`';
    while (text.indexOf(fence) >= 0) fence += '`';
    const pad = /^`|`$/.test(text) ? ' ' : '';
    return fence + pad + text + pad + fence;
  }

  function katex(n) {
    const a = find(n, (e) => tag(e) === 'ANNOTATION' && attr(e, 'encoding') === 'application/x-tex');
    return a ? a.textContent.trim() : null;
  }

  function inline(n, ctx) {
    if (n.nodeType === 3) return (n.textContent || '').replace(/\s+/g, ' ');
    if (!isEl(n) || skipped(n, ctx)) return '';
    if (ctx.depth >= MAX_DEPTH) return (n.textContent || '').replace(/\s+/g, ' ');
    ctx.depth++;
    try { return inlineEl(n, ctx); } finally { ctx.depth--; }
  }

  // A URL goes in the (...) of a link: parentheses, spaces and angle brackets
  // would end or break it, so they are percent-encoded (the URL stays the same URL).
  const encodeUrl = (u) => u.replace(/[\s()<>]/g, (c) => (c === '(' ? '%28' : c === ')' ? '%29' : encodeURIComponent(c)));

  function inlineEl(n, ctx) {
    const t = tag(n);
    switch (t) {
      case 'BR': return '\n';
      case 'IMG': case 'PICTURE': return '[image]';
      case 'STRONG': case 'B': return wrap(inlineChildren(n, ctx), '**');
      case 'EM': case 'I': return wrap(inlineChildren(n, ctx), '*');
      case 'DEL': case 'S': case 'STRIKE': return wrap(inlineChildren(n, ctx), '~~');
      case 'CODE': return codeSpan(n.textContent || '');
      case 'A': {
        const text = inlineChildren(n, ctx).trim();
        const href = attr(n, 'href');
        if (!/^(https?:|mailto:)/i.test(href) || !text) return text;
        const url = encodeUrl(href);
        return text === href && url === href ? href : '[' + text + '](' + url + ')';
      }
    }
    if (hasClass(n, 'katex')) {
      const tex = katex(n);
      if (tex) return '$' + tex + '$';
    }
    return inlineChildren(n, ctx);
  }

  function flush(buf, out) {
    const s = buf.replace(/[ \t]*\n[ \t]*/g, '  \n').replace(/^[ \t\n]+|[ \t\n]+$/g, '');
    if (s) out.push(s);
  }

  // ---- blocks ----
  function walkBlocks(children, out, ctx) {
    if (ctx.depth >= MAX_DEPTH) {
      const text = children.map((k) => (k.nodeType === 3 || (isEl(k) && !skipped(k, ctx)) ? flat(k) : '')).filter(Boolean).join(' ');
      if (text) out.push(text);
      return;
    }
    ctx.depth++;
    try { walkLevel(children, out, ctx); } finally { ctx.depth--; }
  }

  function walkLevel(children, out, ctx) {
    let buf = '';
    for (const k of children) {
      if (k.nodeType === 3) { buf += inline(k, ctx); continue; }
      if (!isEl(k) || skipped(k, ctx)) continue;
      const t = tag(k);
      const display = hasClass(k, 'katex-display');
      if (!display && (hasClass(k, 'katex') || (INLINE.has(t) && !(t === 'SPAN' && hasBlockChild(k))))) { buf += inline(k, ctx); continue; }
      flush(buf, out); buf = '';
      block(k, t, out, ctx);
    }
    flush(buf, out);
  }

  function hasBlockChild(n, depth) {
    depth = depth || 0;
    if (depth >= MAX_DEPTH) return false;
    return kids(n).some((k) => isEl(k) && (!INLINE.has(tag(k)) || hasBlockChild(k, depth + 1)) && !IGNORED.has(tag(k)));
  }

  function block(n, t, out, ctx) {
    if (/^H[1-6]$/.test(t)) {
      const text = inlineChildren(n, ctx).replace(/\s*\n\s*/g, ' ').trim();
      if (text) out.push('#'.repeat(Number(t[1])) + ' ' + text);
    } else if (t === 'PRE') {
      out.push(fence(n));
    } else if (t === 'UL' || t === 'OL') {
      const l = list(n, ctx);
      if (l) out.push(l);
    } else if (t === 'BLOCKQUOTE') {
      const inner = [];
      walkBlocks(kids(n), inner, ctx);
      const text = inner.join('\n\n');
      if (text) out.push(text.split('\n').map((l) => (l ? '> ' + l : '>')).join('\n'));
    } else if (t === 'TABLE') {
      const tb = table(n, ctx);
      if (tb) out.push(tb);
    } else if (t === 'HR') {
      out.push('---');
    } else if (hasClass(n, 'katex-display') && katex(n)) {
      out.push('$$\n' + katex(n) + '\n$$');
    } else {
      walkBlocks(kids(n), out, ctx);
    }
  }

  function fence(pre) {
    const code = find(pre, (e) => tag(e) === 'CODE') || pre;
    let lang = '';
    for (const el of [code, pre]) {
      const m = /(?:^|\s)(?:language|lang)-([^\s]+)/.exec(attr(el, 'class'));
      if (m) { lang = m[1]; break; }
      if (attr(el, 'data-language')) { lang = attr(el, 'data-language'); break; }
    }
    if (!lang && code !== pre) {
      // A header label before the code, as ChatGPT and Gemini draw ("python" + a copy button).
      for (const k of kids(pre)) {
        if (isEl(k) && !find(k, (e) => tag(e) === 'CODE') && tag(k) !== 'CODE') {
          const label = plainText(k).trim();
          if (/^[A-Za-z0-9+#._-]{1,20}$/.test(label)) { lang = label.toLowerCase(); break; }
        }
      }
    }
    let text = (code.textContent || '').replace(/\r\n/g, '\n').replace(/\n+$/, '');
    let f = '```';
    while (text.indexOf(f) >= 0) f += '`';
    return f + lang + '\n' + text + '\n' + f;
  }

  // Text content without buttons, svg and the like.
  function plainText(n, depth) {
    depth = depth || 0;
    if (n.nodeType === 3) return n.textContent || '';
    if (!isEl(n) || IGNORED.has(tag(n))) return '';
    if (depth >= MAX_DEPTH) return n.textContent || '';
    return kids(n).map((k) => plainText(k, depth + 1)).join('');
  }

  function list(n, ctx) {
    const ordered = tag(n) === 'OL';
    let num = parseInt(attr(n, 'start'), 10);
    if (!(num >= 0)) num = 1;
    const items = [];
    for (const li of kids(n)) {
      if (!isEl(li) || skipped(li, ctx) || tag(li) !== 'LI') continue;
      const marker = ordered ? num++ + '. ' : '- ';
      const pad = ' '.repeat(marker.length);
      const blocks = [];
      walkBlocks(kids(li), blocks, ctx);
      if (!blocks.length) continue;
      let s = marker;
      blocks.forEach((b, i) => {
        const text = b.split('\n').map((l, j) => (i === 0 && j === 0 ? l : l ? pad + l : l)).join('\n');
        s += i === 0 ? text : (/^(\s*)([-]|\d+\.) /.test(b) ? '\n' : '\n\n') + text;
      });
      items.push(s);
    }
    return items.join('\n');
  }

  function table(n, ctx) {
    const rows = [];
    (function collect(p, depth) {
      if (depth >= MAX_DEPTH) return;
      for (const k of kids(p)) {
        if (!isEl(k) || skipped(k, ctx)) continue;
        if (tag(k) === 'TR') {
          const cells = kids(k).filter((c) => isEl(c) && (tag(c) === 'TH' || tag(c) === 'TD'));
          rows.push(cells.map((c) => inlineChildren(c, ctx).replace(/\s*\n\s*/g, ' ').replace(/\|/g, '\\|').trim()));
        } else if (tag(k) !== 'TABLE') collect(k, depth + 1);
      }
    })(n, 0);
    if (!rows.length) return '';
    const width = Math.max(...rows.map((r) => r.length));
    const line = (r) => '| ' + Array.from({ length: width }, (_, i) => r[i] || '').join(' | ') + ' |';
    const out = [line(rows[0]), '| ' + Array(width).fill('---').join(' | ') + ' |'];
    for (const r of rows.slice(1)) out.push(line(r));
    return out.join('\n');
  }

  // opts.skip(element) -> true drops that element and everything under it.
  function toMarkdown(rootNode, opts) {
    const ctx = { skip: opts && opts.skip, depth: 0 };
    const out = [];
    if (isEl(rootNode) && !skipped(rootNode, ctx)) walkBlocks([rootNode], out, ctx);
    else if (!isEl(rootNode)) walkBlocks([rootNode], out, ctx);
    return out.join('\n\n').trim();
  }

  const api = { toMarkdown, hasClass, attr, tag, kids, encodeUrl };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else root.CortexMd = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
