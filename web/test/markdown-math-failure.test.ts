import { describe, expect, it, vi } from 'vitest';
import { pendingMathCount, renderInto } from '../src/chat/markdown';

// The chunk that draws formulas cannot be fetched, as it cannot be in a tab
// that was left open across a deploy that removed it.
vi.mock('../src/chat/math', () => {
  throw new Error('chunk not found');
});

describe('a math chunk that never arrives', () => {
  it('lets go of the elements that were waiting on it once the attempt fails', async () => {
    const element = document.createElement('div');
    renderInto(element, 'Energy: $E = mc^2$');
    expect(pendingMathCount()).toBe(1);

    // Waits for the import to settle rather than for a stopwatch. The loop
    // gives up without failing the assertion below if it never does, which is
    // the case the assertion is there to catch.
    for (let tick = 0; tick < 200 && pendingMathCount() > 0; tick += 1) {
      await new Promise((resolve) => setTimeout(resolve, 5));
    }

    expect(pendingMathCount()).toBe(0);
    expect(element.textContent).toContain('E = mc^2');
  });
});
