// A feedback report or a chat message can nest quotes and list items as deep
// as its length allows. The parser recurses per level, so the depth must be
// bounded: the reported input (8000 '>') used to throw a RangeError, and the
// element it was rendering into was already empty by then.

import { describe, expect, it } from 'vitest';
import { parse, renderInto } from '../src/chat/markdown';

function quoteDepth(blocks: ReturnType<typeof parse>): number {
  let depth = 0;
  let current = blocks;
  for (;;) {
    const quote = current.find((block) => block.type === 'blockquote');
    if (!quote || quote.type !== 'blockquote') return depth;
    depth += 1;
    current = quote.children;
  }
}

describe('markdown nesting depth', () => {
  it('parses a run of quote markers without overflowing the stack', () => {
    const blocks = parse('>'.repeat(8000));
    expect(blocks[0]?.type).toBe('blockquote');
    // Bounded, not truncated to nothing: the first levels are still quotes.
    expect(quoteDepth(blocks)).toBeGreaterThan(1);
    expect(quoteDepth(blocks)).toBeLessThan(64);
  });

  it('keeps the text past the depth limit rather than dropping it', () => {
    const blocks = parse(`${'> '.repeat(200)}hello`);
    const flat = JSON.stringify(blocks);
    expect(flat).toContain('hello');
  });

  it('parses a deeply nested list without overflowing the stack', () => {
    // A list item recurses the same way a quote does, so the same bound holds.
    expect(() => parse(`${'- '.repeat(40000)}x`)).not.toThrow();
  });

  it('renders the reported input into the element instead of leaving it empty', () => {
    const host = document.createElement('div');
    expect(() => renderInto(host, '>'.repeat(8000))).not.toThrow();
    expect(host.textContent).toContain('>');
  });

  it('leaves ordinary nested quotes as they were', () => {
    const blocks = parse('> one\n>> two\n>>> three');
    expect(quoteDepth(blocks)).toBe(3);
  });
});
