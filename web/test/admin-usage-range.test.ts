import { describe, it, expect } from 'vitest';
import { customUntilBound } from '../src/admin/usageRange';

const HOUR = 3_600_000;

// The server rounds until down to the hour and excludes it, so the bound must
// be the next hour boundary after the picked minute for that hour to count.
describe('the until bound of a custom usage range', () => {
  it('includes the whole hour holding a picked end that is not on the hour', () => {
    const pickedEnd = Date.UTC(2026, 9, 15, 10, 30, 59, 999);
    expect(customUntilBound(pickedEnd)).toBe(Date.UTC(2026, 9, 15, 11));
  });

  it('includes the whole hour when the picked end is its last minute', () => {
    const pickedEnd = Date.UTC(2026, 9, 15, 10, 59, 59, 999);
    expect(customUntilBound(pickedEnd)).toBe(Date.UTC(2026, 9, 15, 11));
  });

  it('moves a picked end in the last hour of the day into the next day', () => {
    const pickedEnd = Date.UTC(2026, 9, 15, 23, 59, 59, 999);
    expect(customUntilBound(pickedEnd)).toBe(Date.UTC(2026, 9, 16));
  });

  it('is always a whole hour strictly after the picked end', () => {
    for (const minute of [0, 1, 29, 30, 59]) {
      const pickedEnd = Date.UTC(2026, 9, 15, 10, minute, 59, 999);
      const bound = customUntilBound(pickedEnd);
      expect(bound % HOUR).toBe(0);
      expect(bound).toBeGreaterThan(pickedEnd);
      expect(bound - pickedEnd).toBeLessThanOrEqual(HOUR);
    }
  });
});
