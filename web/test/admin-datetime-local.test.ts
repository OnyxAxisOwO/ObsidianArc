// The one place the admin screens turn an instant into the value a
// datetime-local field takes. Every expectation is built from local clock
// fields, so the test holds in whatever zone the suite runs in.

import { describe, expect, it } from 'vitest';
import { toDateTimeLocal } from '../src/views/admin/shared';

describe('toDateTimeLocal', () => {
  it('prints the local wall clock to the minute, zero-padded', () => {
    const at = new Date(2026, 0, 5, 7, 3, 42);
    expect(toDateTimeLocal(at)).toBe('2026-01-05T07:03');
    expect(toDateTimeLocal(at.getTime())).toBe('2026-01-05T07:03');
  });

  it('reads the offset of the instant it is given, summer and winter alike', () => {
    const summer = new Date(2026, 6, 15, 23, 59);
    const winter = new Date(2026, 11, 31, 0, 0);
    expect(toDateTimeLocal(summer)).toBe('2026-07-15T23:59');
    expect(toDateTimeLocal(winter)).toBe('2026-12-31T00:00');
  });
});
