import { describe, it, expect } from 'vitest';
import { relativeTime } from '../src/ui/table';

describe('relativeTime', () => {
  // Every phrase it can produce is past tense, so a moment that has not
  // happened yet used to come out as "just now": a reset card expiring in a
  // month was displayed as one expiring this second.
  it('does not describe a future moment in the past tense', () => {
    const month = Date.now() + 30 * 24 * 3600 * 1000;
    expect(relativeTime(month)).toBe(new Date(month).toLocaleString());
  });

  it('still reads the past as it always did', () => {
    expect(relativeTime(0)).toBe('—');
    // A minute ago is a phrase, not a date.
    const minute = Date.now() - 90 * 1000;
    expect(relativeTime(minute)).not.toBe(new Date(minute).toLocaleString());
  });
});
