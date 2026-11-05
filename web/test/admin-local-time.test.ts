import { describe, expect, it, vi } from 'vitest';
import { daysFromNow } from '../src/views/admin/localTime';

describe('expiry date presets', () => {
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
