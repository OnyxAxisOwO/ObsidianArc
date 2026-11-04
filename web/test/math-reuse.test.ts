import { describe, expect, it } from 'vitest';
import { renderMath } from '../src/chat/math';

// A streamed answer draws the same formulas again on every frame. Each draw
// must get a tree of its own, so that a node the transcript has changed does
// not come back changed for the next draw.
describe('formulas drawn again while a reply streams', () => {
  it('gives every caller the same markup, as a tree of its own', () => {
    const first = renderMath('x^2 + y^2');
    const again = renderMath('x^2 + y^2');
    expect(again.outerHTML).toBe(first.outerHTML);
    expect(again).not.toBe(first);

    again.textContent = 'changed by a reader of the transcript';
    expect(renderMath('x^2 + y^2').outerHTML).toBe(first.outerHTML);
  });

  it('keeps display and inline formulas apart under the same source', () => {
    expect(renderMath('x', true).className).toBe('ai-math ai-math-block');
    expect(renderMath('x', false).className).toBe('ai-math ai-math-inline');
  });
});
