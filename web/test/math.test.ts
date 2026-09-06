import { describe, it, expect } from 'vitest';
import { renderMath, renderMathInline, renderMathBlock } from '../src/chat/math';

describe('LaTeX MathML renderer', () => {
  describe('malformed LaTeX fault tolerance', () => {
    const malformedCases = [
      '\\frac{1}',
      '\\frac',
      '\\sqrt[',
      '\\left(',
      '\\right)',
      '\\begin{pmatrix} 1 & 2',
      '\\begin{unknown_env} x \\end{unknown_env}',
      '\\unknowncmd{arg}',
      '^^^^^^^^',
      '{{{{{{{{',
      '}}}}}}}}',
      '\\text{unclosed text brace',
      '\\\\\\',
      '& & & &',
      '\\binom{1}',
    ];

    for (const latex of malformedCases) {
      it(`handles malformed formula without throwing: ${latex}`, () => {
        expect(() => {
          const el = renderMath(latex);
          expect(el).toBeInstanceOf(HTMLElement);
        }).not.toThrow();
      });
    }

    it('falls back to literal text content when parsing throws internally', () => {
      // Incomplete command that triggers parser error inside try block
      const broken = '\\frac{1}';
      const el = renderMath(broken);
      // Either parses partially or safely retains fallback text
      expect(el).toBeInstanceOf(HTMLElement);
      expect(el.textContent?.length).toBeGreaterThan(0);
    });
  });

  describe('strict attribute provenance and safety', () => {
    it('never injects arbitrary attributes or markup from input', () => {
      const injectionAttempt = '\\text{<img src=x onerror=alert(1)>} + \\mathrm{"><script>alert(1)</script>}';
      const container = renderMath(injectionAttempt);

      // Must not create active HTML tags.
      expect(container.querySelectorAll('script, img, svg, iframe').length).toBe(0);

      // Verify that all attributes across all descendants conform to the known MathML attribute table.
      const allowedAttributes = new Set([
        'class',
        'display',
        'mathvariant',
        'largeop',
        'width',
        'linethickness',
        'accent',
        'accentunder',
        'stretchy',
      ]);

      const allDescendants = container.querySelectorAll('*');
      for (const el of allDescendants) {
        for (const attr of el.attributes) {
          expect(allowedAttributes.has(attr.name)).toBe(true);
          // Attribute values must not be arbitrary script or javascript urls.
          expect(attr.value).not.toMatch(/script|onerror|onload|javascript:/i);
        }
      }
    });

    it('constructs MathML nodes in the MathML namespace', () => {
      const container = renderMath('x^2 + y^2 = z^2');
      const math = container.querySelector('math');
      expect(math).not.toBeNull();
      expect(math?.namespaceURI).toBe('http://www.w3.org/1998/Math/MathML');
    });

    it('sets display mode correctly for inline vs block rendering', () => {
      const inlineEl = renderMathInline('x');
      expect(inlineEl.querySelector('math')?.getAttribute('display')).toBe('inline');

      const blockEl = renderMathBlock('x');
      expect(blockEl.querySelector('math')?.getAttribute('display')).toBe('block');
    });
  });

  describe('deep nesting stack safety', () => {
    it('does not blow stack on deeply nested fractions', () => {
      let formula = '1';
      for (let i = 0; i < 80; i++) {
        formula = `\\frac{1}{${formula}}`;
      }
      expect(() => {
        const el = renderMath(formula);
        expect(el).toBeInstanceOf(HTMLElement);
      }).not.toThrow();
    });

    it('does not blow stack on deeply nested braces and square roots', () => {
      let formula = 'x';
      for (let i = 0; i < 80; i++) {
        formula = `\\sqrt{${formula}}`;
      }
      expect(() => {
        const el = renderMath(formula);
        expect(el).toBeInstanceOf(HTMLElement);
      }).not.toThrow();
    });

    it('does not blow stack on deeply nested superscripts', () => {
      const formula = 'x' + '^{2}'.repeat(80);
      expect(() => {
        const el = renderMath(formula);
        expect(el).toBeInstanceOf(HTMLElement);
      }).not.toThrow();
    });
  });
});
