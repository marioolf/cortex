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
