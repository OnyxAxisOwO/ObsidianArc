// The expiry fields read and write the local wall clock. An instant has to be
// moved by the local offset before it is cut to minutes, whatever zone the
// browser is in, so these checks build their dates in local time.

import { describe, expect, it, vi } from 'vitest';
import { daysFromNow, toLocalInput } from '../src/views/admin/localTime';

describe('datetime-local values', () => {
  it('reads an instant on the local wall clock, to the minute', () => {
    expect(toLocalInput(new Date(2026, 9, 18, 9, 30, 45))).toBe('2026-10-18T09:30');
  });

  it.each([
    { now: new Date(2026, 2, 6, 9, 30), expected: '2026-03-13T09:30' },
    { now: new Date(2026, 9, 30, 9, 30), expected: '2026-11-06T09:30' },
  ])('keeps the local hour a week after $now', ({ now, expected }) => {
    // Calendar days can be 23 or 25 hours across daylight-saving changes.
    vi.useFakeTimers();
    try {
      vi.setSystemTime(now);
      expect(daysFromNow(7)).toBe(expected);
    } finally {
      vi.useRealTimers();
    }
  });
});
