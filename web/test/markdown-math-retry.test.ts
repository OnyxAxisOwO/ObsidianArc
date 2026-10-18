import { afterEach, describe, expect, it, vi } from 'vitest';

// The first fetch of the formula chunk fails and every later one succeeds, as
// it does when a deploy lands between two formulas in an open tab.
let attempts = 0;

describe('a formula shown as source while the chunk is being retried', () => {
  afterEach(() => {
    vi.doUnmock('../src/chat/math');
    vi.resetModules();
    document.body.replaceChildren();
    attempts = 0;
  });

  it('is repainted by a later attempt that succeeds, after an earlier one failed', async () => {
    vi.doMock('../src/chat/math', () => {
      attempts += 1;
      if (attempts === 1) throw new Error('chunk not found');
      return {
        renderMath: (latex: string, display = false) => {
          const node = document.createElement(display ? 'div' : 'span');
          node.className = 'rendered-math';
          node.textContent = `[math ${latex}]`;
          return node;
        },
      };
    });
    vi.resetModules();
    const { pendingMathCount, renderInto } = await import('../src/chat/markdown');

    const first = document.createElement('div');
    document.body.appendChild(first);
    renderInto(first, 'Energy: $E = mc^2$');
    expect(first.textContent).toContain('E = mc^2');
    expect(pendingMathCount()).toBe(1);

    // The failed attempt has settled once this resolves. The formula is still
    // in the document, so the failure must not let go of it.
    await vi.dynamicImportSettled();
    expect(attempts).toBe(1);
    expect(pendingMathCount()).toBe(1);

    const second = document.createElement('div');
    document.body.appendChild(second);
    renderInto(second, 'Mass: $m$');

    await vi.waitFor(() => expect(pendingMathCount()).toBe(0));
    expect(attempts).toBe(2);
    expect(first.querySelector('.rendered-math')?.textContent).toBe('[math E = mc^2]');
    expect(first.textContent).not.toContain('$E = mc^2$');
  });
});
