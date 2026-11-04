import { describe, it, expect } from 'vitest';
import { windowFigure } from '../src/admin/quota';

// The three backoffice forms that save a window share this helper. A negative
// figure must reach the save's own check, which refuses it before anything is
// written; dropping it to "no limit" saved an unenforced window with no message.
describe('windowFigure', () => {
  it('sends a zero window as blank, which is no limit', () => {
    expect(windowFigure(0)).toBeNull();
  });

  it('leaves a blank window blank', () => {
    expect(windowFigure(null)).toBeNull();
  });

  it('passes a positive figure through', () => {
    expect(windowFigure(5)).toBe(5);
  });

  it('passes a negative figure through so the save can refuse it', () => {
    expect(windowFigure(-3)).toBe(-3);
  });
});
