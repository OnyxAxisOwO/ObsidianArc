// The expiry presets count months. setMonth alone rolls the day over, so a
// month on from 31 January used to land in early March.

import { describe, expect, it } from 'vitest';
import { addMonths } from '../src/views/admin/shared';

const ymd = (date: Date) => [date.getFullYear(), date.getMonth() + 1, date.getDate()];

describe('addMonths for the expiry presets', () => {
  it('clamps the day to the last day of a shorter month', () => {
    expect(ymd(addMonths(new Date(2026, 0, 31), 1))).toEqual([2026, 2, 28]);
    expect(ymd(addMonths(new Date(2026, 7, 31), 6))).toEqual([2027, 2, 28]);
    expect(ymd(addMonths(new Date(2026, 10, 30), 3))).toEqual([2027, 2, 28]);
  });

  it('a year on from 29 February is 28 February', () => {
    expect(ymd(addMonths(new Date(2028, 1, 29), 12))).toEqual([2029, 2, 28]);
  });

  it('keeps the day and the time of day when the month has that day', () => {
    const later = addMonths(new Date(2026, 3, 15, 9, 30), 3);
    expect(ymd(later)).toEqual([2026, 7, 15]);
    expect(later.getHours()).toBe(9);
    expect(later.getMinutes()).toBe(30);
  });
});
