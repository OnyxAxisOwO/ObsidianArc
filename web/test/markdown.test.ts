import { describe, it, expect, vi } from 'vitest';
import { safeHref, parse, parseInline, render, renderInto } from '../src/chat/markdown';

/** Polls until a condition holds, or gives up loudly rather than silently. */
async function waitFor(condition: () => boolean, timeoutMs = 5000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (!condition()) {
    if (Date.now() > deadline) throw new Error('timed out waiting for the maths renderer');
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
}

describe('markdown renderer', () => {
  describe('safeHref', () => {
    it('permits valid http, https, and mailto links', () => {
      expect(safeHref('https://example.com')).toBe('https://example.com');
      expect(safeHref('http://example.com/path?q=1#anchor')).toBe('http://example.com/path?q=1#anchor');
      expect(safeHref('mailto:contact@example.com')).toBe('mailto:contact@example.com');
      expect(safeHref('  https://example.com  ')).toBe('https://example.com');
    });

    it('rejects active protocols and relative paths', () => {
      expect(safeHref('javascript:alert(1)')).toBeNull();
      expect(safeHref('JAVASCRIPT:alert(1)')).toBeNull();
      expect(safeHref('data:text/html,<script>alert(1)</script>')).toBeNull();
      expect(safeHref('vbscript:msgbox(1)')).toBeNull();
      expect(safeHref('file:///etc/passwd')).toBeNull();
      expect(safeHref('/api/admin/users')).toBeNull();
      expect(safeHref('relative/path')).toBeNull();
      expect(safeHref('')).toBeNull();
    });
  });

  describe('active content rejection', () => {
    it('never creates executable or tracker DOM elements from raw markdown', () => {
      const maliciousMarkdown = [
        '<script>alert(1)</script>',
        '<img src="https://tracker.com/beacon.png">',
        '<iframe src="https://evil.com"></iframe>',
        '<style>body { background: black; }</style>',
        '<svg onload="alert(1)">',
        '[Click](javascript:alert(1))',
        '<javascript:alert(1)>',
      ].join('\n\n');

      const fragment = render(maliciousMarkdown);
      const container = document.createElement('div');
      container.appendChild(fragment);

      // Model text must not translate into active elements.
      expect(container.querySelectorAll('script, img, iframe, style, svg, object, embed').length).toBe(0);

      // The malicious link must not render as an anchor with javascript href.
      const links = container.querySelectorAll('a');
      for (const link of links) {
        expect(link.getAttribute('href')).not.toMatch(/^javascript:/i);
      }
    });

    it('renders image markdown as a safe anchor rather than an img tag', () => {
      const fragment = render('![Tracker](https://tracker.example.com/pixel.png)');
      const container = document.createElement('div');
      container.appendChild(fragment);

      expect(container.querySelectorAll('img').length).toBe(0);
      const a = container.querySelector('a');
      expect(a).not.toBeNull();
      expect(a?.getAttribute('href')).toBe('https://tracker.example.com/pixel.png');
      expect(a?.textContent).toBe('Tracker');
      expect(a?.getAttribute('target')).toBe('_blank');
      expect(a?.getAttribute('rel')).toBe('noopener noreferrer');
    });

    it('downgrades links with rejected schemes to plain spans', () => {
      const fragment = render('[Dangerous Action](javascript:stealData())');
      const container = document.createElement('div');
      container.appendChild(fragment);

      expect(container.querySelectorAll('a').length).toBe(0);
      const span = container.querySelector('span');
      expect(span?.textContent).toBe('Dangerous Action');
    });
  });

  describe('math placeholder lifecycle', () => {
    it('initially renders as source text, then updates when math module resolves', async () => {
      const container = document.createElement('div');
      const text = 'Here is inline math: $E = mc^2$ and block math:\n\n$$\\int_0^1 x dx$$';

      // First render: math chunk is requested dynamically, placeholder rendered immediately.
      renderInto(container, text);
      expect(container.textContent).toContain('E = mc^2');

      // Wait for the chunk rather than for a stopwatch. A fixed delay here is
      // a test that passes on an idle machine and fails on a loaded one: the
      // renderer arrives when the import resolves, and how long that takes is
      // whatever the rest of the suite is doing at the time.
      await waitFor(() => container.querySelectorAll('math, .ai-math').length > 0);

      // After arrival, rendered math formulas should contain MathML or ai-math classes.
      expect(container.querySelectorAll('math, .ai-math').length).toBeGreaterThan(0);
    });
  });

  // Remembering that a marker has no closing must not change what a closed
  // one becomes: the cases below were rendered the same way before it.
  describe('emphasis that closes, beside emphasis that does not', () => {
    const text = (value: string) => ({ type: 'text', value });
    it('closes an em after an opener that never closes', () => {
      expect(parseInline('a * b *c*')).toEqual([text('a * b '), { type: 'em', children: [text('c')] }]);
      expect(parseInline('*a *b*')).toEqual([{ type: 'em', children: [text('a *b')] }]);
    });
    it('keeps strong, em and del apart when one of them is unclosed', () => {
      expect(parseInline('**x *y* ~~z')).toEqual([{ type: 'em', children: [text('*x *y')] }, text(' ~~z')]);
      expect(parseInline('~~a ~~b~~ **c**')).toEqual([
        { type: 'del', children: [text('a ~~b')] }, text(' '), { type: 'strong', children: [text('c')] },
      ]);
    });
    it('still refuses snake_case after an unclosed underscore', () => {
      // The first closing sits inside a word and is refused, which is not a
      // scan that found nothing: the later opener must still be tried.
      expect(parseInline('_a b snake_case_name _c_')).toEqual([
        text('_a b snake_case_name '), { type: 'em', children: [text('c')] },
      ]);
      expect(parseInline('_ a snake_case _x_')).toEqual([text('_ a snake_case '), { type: 'em', children: [text('x')] }]);
    });
    it('closes a later opener when only the earlier one was refused at its first character', () => {
      expect(parseInline('* a *b*')).toEqual([text('* a '), { type: 'em', children: [text('b')] }]);
      expect(parseInline('~~ a ~~b~~')).toEqual([text('~~ a '), { type: 'del', children: [text('b')] }]);
    });
  });

  describe('pathological input resilience (timeout tests)', () => {
    it('does not hang on deeply nested blockquotes', () => {
      const start = Date.now();
      const input = '> '.repeat(100) + 'Deeply nested quote';
      const blocks = parse(input);
      expect(blocks.length).toBeGreaterThan(0);
      expect(Date.now() - start).toBeLessThan(500);
    });

    it('does not hang on deeply nested emphasis and asterisks', () => {
      const start = Date.now();
      const input = '*'.repeat(500) + 'text' + '*'.repeat(500);
      const inlines = parseInline(input);
      expect(inlines.length).toBeGreaterThan(0);
      expect(Date.now() - start).toBeLessThan(500);
    });

    // An unclosed marker scans to the end of the paragraph for a closing that
    // is not there. One such scan is linear; one per opener was quadratic.
    for (const [name, unit] of [
      ['*', 'a *b c '],
      ['**', 'a **b c '],
      ['~~', 'a ~~b c '],
      ['_', 'a _b c '],
      ['__', 'a __b c '],
    ] as const) {
      it(`stays linear on a long paragraph of unclosed ${name}`, () => {
        const input = unit.repeat(Math.ceil(200_000 / unit.length));
        const start = Date.now();
        const inlines = parseInline(input);
        expect(Date.now() - start).toBeLessThan(500);
        expect(inlines.length).toBeGreaterThan(0);
      });
    }

    it('stays linear on the ten thousand character case the em scan was reported on', () => {
      const input = 'a *b c '.repeat(1430);
      const start = Date.now();
      expect(parseInline(input)).toEqual([{ type: 'text', value: input }]);
      expect(Date.now() - start).toBeLessThan(500);
    });

    it('does not hang on pathological backtracking patterns in links', () => {
      const start = Date.now();
      const input = '[a]('.repeat(200) + 'https://example.com' + ')'.repeat(200);
      const inlines = parseInline(input);
      expect(inlines.length).toBeGreaterThan(0);
      expect(Date.now() - start).toBeLessThan(500);
    });

    it('does not hang on extremely long lines without breaks', () => {
      const start = Date.now();
      const input = 'word_with_underscores '.repeat(5000);
      const blocks = parse(input);
      expect(blocks.length).toBe(1);
      expect(Date.now() - start).toBeLessThan(500);
    });

    it('does not hang on unclosed code and math fences with large bodies', () => {
      const start = Date.now();
      const codeInput = '```typescript\n' + 'const a = 1;\n'.repeat(2000);
      const codeBlocks = parse(codeInput);
      expect(codeBlocks.length).toBe(1);
      expect(codeBlocks[0]?.type).toBe('code');

      const mathInput = '$$\n' + 'x = y + z\n'.repeat(2000);
      const mathBlocks = parse(mathInput);
      expect(mathBlocks.length).toBe(1);
      expect(mathBlocks[0]?.type).toBe('math');
      expect(Date.now() - start).toBeLessThan(500);
    });

    it('does not hang on a heading whose text has a long run of spaces inside it', () => {
      const start = Date.now();
      const blocks = parse('# x' + ' '.repeat(50000) + 'y');
      expect(blocks[0]?.type).toBe('heading');
      expect(Date.now() - start).toBeLessThan(500);
    });

    it('does not hang on a heading with a long run of spaces after its hash', () => {
      const start = Date.now();
      const blocks = parse('# ' + ' '.repeat(50000) + 'x');
      expect(blocks).toEqual([{ type: 'heading', level: 1, children: [{ type: 'text', value: 'x' }] }]);
      expect(Date.now() - start).toBeLessThan(500);
    });

    it('does not hang on a long run of backticks that never closes', () => {
      const start = Date.now();
      const blocks = parse('x ' + '`'.repeat(20000) + 'text');
      expect(blocks.length).toBe(1);
      expect(Date.now() - start).toBeLessThan(500);
    });
  });

  describe('code blocks', () => {
    it('wraps a fenced block under a header that names it and acts on it', () => {
      const host = document.createElement('div');
      renderInto(host, '```js\nconst a = 1;\n```');

      const wrap = host.querySelector('.ai-code');
      expect(wrap).not.toBeNull();
      // The header sits in the wrapper, not over the <pre>: the <pre> scrolls
      // sideways, and a control inside it would ride away with a long line.
      expect(wrap?.querySelector('pre > code')?.textContent).toBe('const a = 1;');
      expect(wrap?.querySelector('code')?.className).toBe('language-js');
      expect(wrap?.querySelector('.ai-code-lang')?.textContent).toBe('JavaScript');

      const buttons = wrap?.querySelectorAll('.ai-code-actions button.ai-code-btn');
      expect(buttons?.length).toBe(2);
      expect(buttons?.[0]?.getAttribute('type')).toBe('button');
      // Icon-only, so the name a screen reader reads has to be on the button.
      expect(buttons?.[0]?.getAttribute('aria-label')).toBeTruthy();
      expect(buttons?.[0]?.querySelector('svg')).not.toBeNull();
    });

    it('names an untagged block from its source, which is how most arrive', () => {
      const host = document.createElement('div');
      renderInto(host, '```\npackage main\n\nfunc main() {}\n```');

      expect(host.querySelector('.ai-code-lang')?.textContent).toBe('Go');
      // The class still carries only what the author tagged: a guess is worth
      // a label, not a claim about what the block is.
      expect(host.querySelector('pre > code')?.className).toBe('');
    });

    it('builds the file in the browser rather than asking the server for it', () => {
      const created: string[] = [];
      const saved: Array<{ name: string; href: string }> = [];
      const url = URL as unknown as Record<string, unknown>;
      const previous = { create: url.createObjectURL, revoke: url.revokeObjectURL };
      url.createObjectURL = () => {
        created.push('blob');
        return 'blob:snippet';
      };
      url.revokeObjectURL = () => {};
      const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
        this: HTMLAnchorElement,
      ) {
        saved.push({ name: this.download, href: this.href });
      });

      try {
        const host = document.createElement('div');
        renderInto(host, '```ts\nexport const a: number = 1;\n```');
        const download = host.querySelectorAll('button.ai-code-btn')[0] as HTMLButtonElement;
        download.click();

        expect(created.length).toBe(1);
        expect(saved).toEqual([{ name: 'snippet.ts', href: 'blob:snippet' }]);
      } finally {
        click.mockRestore();
        url.createObjectURL = previous.create;
        url.revokeObjectURL = previous.revoke;
      }
    });

    it('leaves inline code alone, which has nothing worth a control', () => {
      const host = document.createElement('div');
      renderInto(host, 'a `const a = 1;` b');
      expect(host.querySelector('.ai-code')).toBeNull();
      expect(host.querySelector('button.ai-code-btn')).toBeNull();
      expect(host.querySelector('code')?.textContent).toBe('const a = 1;');
    });

    it('re-renders without leaving a second set of controls behind', () => {
      const host = document.createElement('div');
      renderInto(host, '```\nfirst\n```');
      renderInto(host, '```\nsecond\n```');
      expect(host.querySelectorAll('button.ai-code-btn').length).toBe(2);
      expect(host.querySelector('pre > code')?.textContent).toBe('second');
    });
  });

  describe('ATX headings and inline code edges', () => {
    const text = (value: string) => ({ type: 'text', value });

    it('drops closing hashes and the spaces around them, and keeps hashes inside the text', () => {
      expect(parse('## Title ##')).toEqual([{ type: 'heading', level: 2, children: [text('Title')] }]);
      expect(parse('# a # b #')).toEqual([{ type: 'heading', level: 1, children: [text('a # b')] }]);
      expect(parse('# C#')).toEqual([{ type: 'heading', level: 1, children: [text('C')] }]);
    });

    it('takes one to six hashes, and needs a space or a tab after them', () => {
      expect(parse('######## eight')[0]?.type).toBe('paragraph');
      expect(parse('######')[0]?.type).toBe('paragraph');
      expect(parse('#\tcolumn')).toEqual([{ type: 'heading', level: 1, children: [text('column')] }]);
      expect(parse('   ### three spaces')[0]?.type).toBe('heading');
      expect(parse('    # four spaces')[0]?.type).toBe('paragraph');
    });

    it('closes a code span at the first run of the same length, backing off to a shorter opener when needed', () => {
      expect(parseInline('`` ` ``')).toEqual([{ type: 'codespan', value: '`' }]);
      expect(parseInline('``a`b``')).toEqual([{ type: 'codespan', value: 'a`b' }]);
      expect(parseInline('``a`b')).toEqual([{ type: 'codespan', value: '`a' }, text('b')]);
      expect(parseInline('a `b')).toEqual([text('a `b')]);
    });
  });

  // ESCAPABLE is a string, and every string includes the empty one, so a
  // backslash at the end of a block used to append `undefined` to the text.
  // The table branch had no test at all, and the list's continuation rule is
  // deliberately narrower than a paragraph's: these hold both in place.
  describe('tables and list continuations', () => {
    it('renders a header, aligned cells and body rows', () => {
      const table = render('| a | b | c |\n|:--|:-:|--:|\n| 1 | **2** | 3 |\n| 4 | 5 | 6 |').firstChild as HTMLTableElement;
      expect(table.tagName).toBe('TABLE');
      const heads = [...table.querySelectorAll('thead th')] as HTMLElement[];
      expect(heads.map((cell) => cell.textContent)).toEqual(['a', 'b', 'c']);
      expect(heads.map((cell) => cell.style.textAlign)).toEqual(['left', 'center', 'right']);
      const rows = [...table.querySelectorAll('tbody tr')];
      expect(rows).toHaveLength(2);
      const cells = [...rows[0]!.querySelectorAll('td')] as HTMLElement[];
      expect(cells.map((cell) => cell.style.textAlign)).toEqual(['left', 'center', 'right']);
      expect(cells[1]!.querySelector('strong')?.textContent).toBe('2');
    });

    it('keeps pipes in a paragraph when the next line is not a divider', () => {
      const blocks = parse('a | b\nc | d');
      expect(blocks).toHaveLength(1);
      expect(blocks[0]).toMatchObject({ type: 'paragraph' });
      expect(render('a | b\nc | d').querySelector('table')).toBeNull();
    });

    it('nests a heading or quote written under a list item rather than ending the list', () => {
      const blocks = parse('- item\n  # inner\n  > quoted\n- next');
      expect(blocks).toHaveLength(1);
      const list = blocks[0] as { type: string; items: Array<Array<{ type: string }>> };
      expect(list.type).toBe('list');
      expect(list.items).toHaveLength(2);
      expect(list.items[0]!.map((block) => block.type)).toEqual(['paragraph', 'heading', 'blockquote']);
    });

    it('ends a paragraph at a heading, a rule, a quote, an item or a fence', () => {
      for (const start of ['# h', '---', '> q', '- i', '```', '$$x$$']) {
        const blocks = parse(`text\n${start}`);
        expect(blocks[0], start).toMatchObject({ type: 'paragraph' });
        expect(blocks.length, start).toBeGreaterThan(1);
      }
    });
  });

  describe('a backslash at the end of a block', () => {
    it('stays a literal backslash rather than rendering the word undefined', () => {
      expect(render('C:\\Users\\').textContent).toBe('C:\\Users\\');
      expect(render('# Path\\').textContent).not.toContain('undefined');
    });

    it('still escapes a punctuation character that follows it', () => {
      expect(render('a\\*b').textContent).toBe('a*b');
    });
  });
});
