import { describe, it, expect } from 'vitest';
import { safeIntro } from '../src/landing/landing-page';

// Turns a fragment into an inspectable element hierarchy without attaching
// to the live document.
function toElement(fragment: DocumentFragment): HTMLElement {
  const container = document.createElement('div');
  container.appendChild(fragment);
  return container;
}

describe('safeIntro sanitizer', () => {
  describe('dangerous tags subtree removal', () => {
    const payloads: readonly (readonly [string, string])[] = [
      ['script tag', '<script>alert("xss")</script>'],
      ['script with src', '<script src="https://evil.com/payload.js"></script>'],
      ['style tag', '<style>body { display: none !important; }</style>'],
      ['iframe tag', '<iframe src="https://evil.com"></iframe>'],
      ['form tag', '<form action="/api/admin"><input name="bad" value="1"><button>Submit</button></form>'],
      ['object tag', '<object data="evil.swf"></object>'],
      ['template tag', '<template><p>hidden payload</p><script>alert(1)</script></template>'],
      ['embed tag', '<embed src="evil.swf">'],
      ['svg tag', '<svg><circle cx="5" cy="5" r="5"/><script>alert(1)</script></svg>'],
      ['math tag', '<math><mtext>formula</mtext></math>'],
    ];

    for (const [name, payload] of payloads) {
      it(`drops ${name} entirely`, () => {
        const root = toElement(safeIntro(payload));
        expect(root.children.length).toBe(0);
        expect(root.textContent).toBe('');
      });
    }

    it('drops dangerous tags nested inside allowed markup without leaking contents', () => {
      const html = '<div><p>Safe start<script>alert("evil")</script><style>.bad{}</style>Safe end</p></div>';
      const root = toElement(safeIntro(html));
      expect(root.querySelectorAll('script, style').length).toBe(0);
      expect(root.textContent).toBe('Safe startSafe end');
    });
  });

  describe('disallowed protocol handling on anchors', () => {
    const dangerousHrefs = [
      'javascript:alert(1)',
      'JAVASCRIPT:alert(1)',
      'JaVaScRiPt:alert(1)',
      '   javascript:alert(1)',
      '\njavascript:alert(1)',
      '\r\njavascript:alert(1)',
      '\tjavascript:alert(1)',
      'java\nscript:alert(1)',
      'java\tscript:alert(1)',
      '&#106;avascript:alert(1)',
      '&#x6a;avascript:alert(1)',
      '&Tab;javascript:alert(1)',
      'data:text/html,<script>alert(1)</script>',
      'DATA:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==',
      'vbscript:msgbox(1)',
      'VBSCRIPT:msgbox(1)',
      'file:///etc/passwd',
    ];

    for (const href of dangerousHrefs) {
      it(`strips href for dangerous URL: ${JSON.stringify(href)}`, () => {
        const html = `<a href="${href}">Click</a>`;
        const root = toElement(safeIntro(html));
        const a = root.querySelector('a');
        expect(a).not.toBeNull();
        expect(a?.hasAttribute('href')).toBe(false);
        expect(a?.getAttribute('rel')).toBe('noopener noreferrer');
        expect(a?.textContent).toBe('Click');
      });
    }

    it('preserves safe URLs with http, https, mailto, and tel', () => {
      const safe = [
        ['https://example.com/path?a=1#hash', 'https://example.com/path?a=1#hash'],
        ['http://example.com', 'http://example.com'],
        ['mailto:admin@example.com', 'mailto:admin@example.com'],
        ['tel:+1234567890', 'tel:+1234567890'],
      ];

      for (const [input, expected] of safe) {
        const root = toElement(safeIntro(`<a href="${input}" title="Tip">Link</a>`));
        const a = root.querySelector('a');
        expect(a?.getAttribute('href')).toBe(expected);
        expect(a?.getAttribute('title')).toBe('Tip');
        expect(a?.getAttribute('rel')).toBe('noopener noreferrer');
      }
    });
  });

  describe('attribute stripping and sanitization', () => {
    it('strips all attributes except safe href, title, and rel on anchors', () => {
      const html = '<a href="https://example.com" title="Safe" id="link" class="cta" style="color:red" onclick="alert(1)" target="_blank" download>Text</a>';
      const root = toElement(safeIntro(html));
      const a = root.querySelector('a')!;
      expect(a.getAttribute('href')).toBe('https://example.com');
      expect(a.getAttribute('title')).toBe('Safe');
      expect(a.getAttribute('rel')).toBe('noopener noreferrer');
      expect(a.hasAttribute('id')).toBe(false);
      expect(a.hasAttribute('class')).toBe(false);
      expect(a.hasAttribute('style')).toBe(false);
      expect(a.hasAttribute('onclick')).toBe(false);
      expect(a.hasAttribute('target')).toBe(false);
      expect(a.hasAttribute('download')).toBe(false);
    });

    it('strips all attributes on non-anchor elements', () => {
      const html = '<p id="para" class="lead" style="font-size:20px" onclick="evil()" data-bind="secret">Paragraph <strong title="bold" class="important">text</strong></p>';
      const root = toElement(safeIntro(html));
      const p = root.querySelector('p')!;
      expect(p.attributes.length).toBe(0);
      const strong = root.querySelector('strong')!;
      expect(strong.attributes.length).toBe(0);
      expect(strong.textContent).toBe('text');
    });
  });

  describe('unwhitelisted non-dangerous tags unpacking', () => {
    it('unpacks unknown tags while preserving inner text and child elements', () => {
      const html = '<marquee><custom-element>Hello <strong>World</strong></custom-element></marquee>';
      const root = toElement(safeIntro(html));
      expect(root.querySelector('marquee')).toBeNull();
      expect(root.querySelector('custom-element')).toBeNull();
      expect(root.querySelector('strong')?.textContent).toBe('World');
      expect(root.textContent).toBe('Hello World');
    });
  });

  describe('mutation XSS and parser rewrite defenses', () => {
    it('neutralizes parser mutation attacks using foreign content', () => {
      // Browsers can rewrite SVG style payloads across namespace boundaries
      const mXssPayload = '<svg><style><a title="</style><img src=x onerror=alert(1)>">';
      const root = toElement(safeIntro(mXssPayload));
      expect(root.querySelectorAll('svg, style, img, a').length).toBe(0);
      expect(root.textContent).toBe('');
    });

    it('neutralizes math mutation payloads', () => {
      const mathPayload = '<math><style><a title="</style><img src=x onerror=alert(1)>">';
      const root = toElement(safeIntro(mathPayload));
      expect(root.querySelectorAll('math, style, img, a').length).toBe(0);
      expect(root.textContent).toBe('');
    });
  });

  describe('valid document structure preservation', () => {
    it('preserves allowed tags and hierarchy cleanly', () => {
      const html = `
        <h1>Heading 1</h1>
        <p>This is a paragraph with <a href="https://example.com">a link</a> and <code>code snippet</code>.</p>
        <ul>
          <li>First item</li>
          <li>Second item with <em>emphasis</em></li>
        </ul>
        <table>
          <thead><tr><th>Col 1</th><th>Col 2</th></tr></thead>
          <tbody><tr><td>Val 1</td><td>Val 2</td></tr></tbody>
        </table>
      `;
      const root = toElement(safeIntro(html));
      expect(root.querySelector('h1')?.textContent).toBe('Heading 1');
      expect(root.querySelectorAll('li').length).toBe(2);
      expect(root.querySelectorAll('th').length).toBe(2);
      expect(root.querySelectorAll('td').length).toBe(2);
      expect(root.querySelector('code')?.textContent).toBe('code snippet');
      expect(root.querySelector('em')?.textContent).toBe('emphasis');
    });
  });
});
