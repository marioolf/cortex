// A tiny DOM for tests: parses a small subset of HTML into nodes with the
// interface the extension relies on (nodeType, tagName, childNodes, parentNode,
// textContent, getAttribute, querySelector(All)). Test-only; not a real parser.
'use strict';

const VOID = new Set(['br', 'hr', 'img', 'input', 'meta', 'link']);

class Node {
  constructor(nodeType) { this.nodeType = nodeType; this.childNodes = []; this.parentNode = null; }
  get textContent() { return this.childNodes.map((c) => c.textContent).join(''); }
}
class Text extends Node {
  constructor(t) { super(3); this._t = t; }
  get textContent() { return this._t; }
}
class Element extends Node {
  constructor(tag, attrs) { super(1); this.tagName = tag.toUpperCase(); this.attrs = attrs; }
  getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; }
  querySelectorAll(sel) { return query(this, sel); }
  querySelector(sel) { return query(this, sel)[0] || null; }
}

function decode(s) {
  return s.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&nbsp;/g, ' ').replace(/&amp;/g, '&');
}

function parse(html) {
  const root = new Element('#root', {});
  root.nodeType = 1;
  const stack = [root];
  const re = /<!--[\s\S]*?-->|<\/([A-Za-z][\w:-]*)\s*>|<([A-Za-z][\w:-]*)((?:\s+[^\s>=]+(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+))?)*)\s*(\/?)>|([^<]+)/g;
  let m;
  while ((m = re.exec(html))) {
    const top = stack[stack.length - 1];
    if (m[5] !== undefined) {
      const t = new Text(decode(m[5])); t.parentNode = top; top.childNodes.push(t);
    } else if (m[1]) {
      const name = m[1].toUpperCase();
      for (let i = stack.length - 1; i > 0; i--) if (stack[i].tagName === name) { stack.length = i; break; }
    } else if (m[2]) {
      const attrs = {};
      m[3].replace(/([^\s=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?/g, (_, k, a, b, c) => { attrs[k] = decode(a ?? b ?? c ?? ''); });
      const el = new Element(m[2], attrs);
      el.parentNode = top; top.childNodes.push(el);
      if (!VOID.has(m[2].toLowerCase()) && !m[4]) stack.push(el);
    }
  }
  return root;
}

// Selector subset: comma lists, descendant combinator, tag, .class, [attr], [attr="v"].
function compound(sel) {
  const c = { tag: null, classes: [], attrs: [] };
  const re = /^([A-Za-z][\w-]*)|\.([\w-]+)|\[([\w-]+)(?:="([^"]*)")?\]/;
  let rest = sel;
  while (rest) {
    const m = re.exec(rest);
    if (!m) throw new Error('fakedom: unsupported selector ' + sel);
    if (m[1]) c.tag = m[1].toUpperCase();
    else if (m[2]) c.classes.push(m[2]);
    else c.attrs.push([m[3], m[4]]);
    rest = rest.slice(m[0].length);
  }
  return c;
}
function matchC(el, c) {
  if (c.tag && el.tagName !== c.tag) return false;
  const cls = (el.getAttribute('class') || '').split(/\s+/);
  if (!c.classes.every((k) => cls.includes(k))) return false;
  return c.attrs.every(([k, v]) => { const a = el.getAttribute(k); return a !== null && (v === undefined || a === v); });
}
function matchSel(el, chain) {
  if (!matchC(el, chain[chain.length - 1])) return false;
  let i = chain.length - 2;
  for (let p = el.parentNode; p && i >= 0; p = p.parentNode) if (p.nodeType === 1 && matchC(p, chain[i])) i--;
  return i < 0;
}
function query(root, sel) {
  const chains = sel.split(',').map((s) => s.trim().split(/\s+(?=(?:[^"]*"[^"]*")*[^"]*$)/).map(compound));
  const out = [];
  (function walk(n) {
    for (const k of n.childNodes) {
      if (k.nodeType !== 1) continue;
      if (chains.some((ch) => matchSel(k, ch))) out.push(k);
      walk(k);
    }
  })(root);
  return out;
}

// document(html, {title}) -> object shaped like the parts of Document we use.
function document(html, opts) {
  const root = parse(html);
  return {
    title: (opts && opts.title) || '',
    querySelectorAll: (s) => query(root, s),
    querySelector: (s) => query(root, s)[0] || null,
    root,
  };
}

module.exports = { parse, document, query };
