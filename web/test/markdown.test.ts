import { describe, it, expect } from 'vitest';
import { safeHref, parse, parseInline, render, renderInto } from '../src/chat/markdown';

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

      // Allow the dynamic import microtasks to settle and trigger awaitingMath re-render.
      await new Promise((resolve) => setTimeout(resolve, 60));

      // After arrival, rendered math formulas should contain MathML or ai-math classes.
      expect(container.querySelectorAll('math, .ai-math').length).toBeGreaterThan(0);
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
  });
});
