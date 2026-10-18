// The expiry fields read and write the local wall clock. An instant has to be
// moved by the local offset before it is cut to minutes, whatever zone the
// browser is in, so these checks build their dates in local time.

import { describe, expect, it } from 'vitest';
import { daysFromNow, toLocalInput } from '../src/views/admin/localTime';

describe('datetime-local values', () => {
  it('reads an instant on the local wall clock, to the minute', () => {
    expect(toLocalInput(new Date(2026, 9, 18, 9, 30, 45))).toBe('2026-10-18T09:30');
  });

  it('is a week from now on the local clock', () => {
    const value = daysFromNow(7);
    const expected = toLocalInput(new Date(Date.now() + 7 * 24 * 60 * 60 * 1000));
    // Both are on the same clock, so comparing them as UTC strings is exact to the minute.
    expect(Math.abs(Date.parse(`${value}Z`) - Date.parse(`${expected}Z`))).toBeLessThan(60_000);
  });
});
