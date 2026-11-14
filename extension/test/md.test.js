'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { parse } = require('./fakedom.js');
const { toMarkdown } = require('../lib/md.js');

const md = (html, opts) => toMarkdown(parse(html), opts);

test('paragraphs, headings, emphasis, inline code, links', () => {
  assert.equal(
    md('<h2>Title</h2><p>Some <strong>bold</strong> and <em>italic</em> and <code>x &lt; y</code>.</p><p>See <a href="https://example.org/a">the docs</a>.</p>'),
    '## Title\n\nSome **bold** and *italic* and `x < y`.\n\nSee [the docs](https://example.org/a).'
  );
});

test('emphasis keeps outer spaces outside the marks; javascript: links lose their href', () => {
  assert.equal(md('<p>a<strong> spaced </strong>b <a href="javascript:alert(1)">x</a></p>'), 'a **spaced** b x');
});

test('line breaks and whitespace collapse', () => {
  assert.equal(md('<p>one<br>two\n   three</p>'), 'one  \ntwo three');
});

test('fenced code with language from class, and from a header label', () => {
  assert.equal(md('<pre><code class="language-python">def f():\n    return 1\n</code></pre>'), '```python\ndef f():\n    return 1\n```');
  assert.equal(md('<pre><div><span>JavaScript</span><button>Copy</button></div><div><code>let a = 1;</code></div></pre>'), '```javascript\nlet a = 1;\n```');
  assert.equal(md('<pre><code>no lang</code></pre>'), '```\nno lang\n```');
});

test('code containing backtick fences gets a longer fence', () => {
  assert.equal(md('<pre><code>```\nx\n```</code></pre>'), '````\n```\nx\n```\n````');
});

test('lists, ordered start, nested', () => {
  assert.equal(
    md('<ul><li>one</li><li>two<ul><li>two a</li><li>two b</li></ul></li></ul><ol start="3"><li><p>third</p><p>more</p></li><li>fourth</li></ol>'),
    '- one\n- two\n  - two a\n  - two b\n\n3. third\n\n   more\n4. fourth'
  );
});

test('blockquote and hr', () => {
  assert.equal(md('<blockquote><p>quoted</p><p>again</p></blockquote><hr><p>after</p>'), '> quoted\n>\n> again\n\n---\n\nafter');
});

test('tables become pipe tables', () => {
  assert.equal(
    md('<table><thead><tr><th>A</th><th>B</th></tr></thead><tbody><tr><td>1</td><td>x|y</td></tr><tr><td>2</td></tr></tbody></table>'),
    '| A | B |\n| --- | --- |\n| 1 | x\\|y |\n| 2 |  |'
  );
});

test('buttons, svg, scripts, hidden are ignored; images become [image]', () => {
  assert.equal(md('<div><button>Copy</button><svg><text>icon</text></svg><script>evil()</script><span hidden>no</span><p>text <img src="a.png" alt="x"></p></div>'), 'text [image]');
});

test('skip callback drops subtrees', () => {
  assert.equal(md('<div><div data-testid="thinking">secret</div><p>kept</p></div>', { skip: (e) => e.getAttribute('data-testid') === 'thinking' }), 'kept');
});

test('divs and unknown custom elements are containers; spans stay inline', () => {
  assert.equal(md('<div><my-box>first</my-box><my-box>second <span>inline</span></my-box></div>'), 'first\n\nsecond inline');
});

test('math with an annotation', () => {
  assert.equal(md('<p>Let <span class="katex"><span class="katex-mathml"><math><semantics><mrow>x</mrow><annotation encoding="application/x-tex">x^2</annotation></semantics></math></span><span class="katex-html">x2</span></span> be.</p>'), 'Let $x^2$ be.');
});

test('links with parentheses, spaces and angle brackets stay valid Markdown', () => {
  const url = 'https://example.org/wiki/Foo_(bar)?q=a b&r=<x>';
  const out = md('<p>see <a href="' + url.replace(/"/g, '&quot;').replace(/</g, '&lt;').replace(/>/g, '&gt;') + '">the page</a> now</p>');
  assert.equal(out, 'see [the page](https://example.org/wiki/Foo_%28bar%29?q=a%20b&r=%3Cx%3E) now');
  // Round trip: no raw delimiter is left in the destination, and decoding gives the URL back.
  const dest = /\]\((.*)\)/.exec(out)[1];
  assert.ok(!/[\s()<>]/.test(dest));
  assert.equal(decodeURIComponent(dest), url);
  // A plain URL is unchanged, also as its own text.
  assert.equal(md('<p><a href="https://example.org/a">https://example.org/a</a></p>'), 'https://example.org/a');
  // A URL as its own text that would not survive as a bare link gets the full form.
  assert.equal(md('<p><a href="https://e.org/a_(b)">https://e.org/a_(b)</a></p>'), '[https://e.org/a_(b)](https://e.org/a_%28b%29)');
});

// A node whose textContent does not recurse, so that the test can nest deeper than a stack allows.
function deep(levels, leafText, tagName) {
  let node = { nodeType: 3, textContent: leafText };
  for (let i = 0; i < levels; i++) {
    const child = node;
    const text = leafText;
    node = { nodeType: 1, tagName: tagName || 'DIV', childNodes: [child], getAttribute: () => null, textContent: text };
  }
  return node;
}

test('extremely deep nesting still converts', () => {
  for (const tagName of ['DIV', 'SPAN', 'BLOCKQUOTE', 'LI']) {
    const root = tagName === 'LI' ? { nodeType: 1, tagName: 'UL', childNodes: [deep(20000, 'deep text', 'LI')], getAttribute: () => null, textContent: 'deep text' } : deep(20000, 'deep text', tagName);
    let out;
    assert.doesNotThrow(() => { out = toMarkdown(root); }, tagName);
    assert.ok(out.includes('deep text'), tagName + ': ' + out);
  }
  // Within the cap nothing changes.
  assert.equal(toMarkdown(deep(50, 'shallow')), 'shallow');
});
