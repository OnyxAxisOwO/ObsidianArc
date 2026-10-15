// The arithmetic under the usage charts. The axis and the window are both
// cut by the server on the reader's clock; these hold that the reader's side
// reads the same grid, whatever the browser's zone says and across a change of
// clock, and that the figures and the fill come out as they are shown.

import { describe, expect, it } from 'vitest';
import { fillBuckets, percent, tickFormat, timeTicks } from '@/views/admin/usage/scale';

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

describe('timeTicks', () => {
  it('labels every day on the grid the server cut, across a change of clock', () => {
    // A reader on EDT (-4h). Local midnight is 04:00 UTC every day, including
    // the days before a change to EST, when the browser would have read those
    // instants at -5h.
    const ats = Array.from({ length: 7 }, (_, day) => Date.UTC(2026, 2, 1 + day) + 4 * HOUR);
    const { indices, step } = timeTicks(ats, DAY, -4 * HOUR);
    expect(step).toBe(DAY);
    expect(indices).toEqual([0, 1, 2, 3, 4, 5, 6]);
  });

  it('picks the step that keeps the labels within the count, and reports it', () => {
    // Hourly points across 71 hours: the 12-hour step is the first that keeps
    // the labels at six or fewer intervals.
    const start = Date.UTC(2026, 9, 10, 0);
    const ats = Array.from({ length: 72 }, (_, hour) => start + hour * HOUR);
    const { indices, step } = timeTicks(ats, HOUR, 0);
    expect(step).toBe(12 * HOUR);
    expect(indices).toEqual([0, 12, 24, 36, 48, 60]);
  });
});

describe('tickFormat', () => {
  it('reads a label as a date when the ticks are a day or more apart', () => {
    expect(tickFormat(DAY, 6 * DAY)).toEqual({ month: 'numeric', day: 'numeric' });
  });

  it('adds the date to sub-day ticks across more than a day', () => {
    // A 71-hour hourly series: 00:00 and 12:00 on each day, all of them
    // needing the day to be told apart.
    expect(tickFormat(12 * HOUR, 71 * HOUR)).toEqual({
      month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit',
    });
  });

  it('reads a label as a time inside one day', () => {
    expect(tickFormat(4 * HOUR, 20 * HOUR)).toEqual({ hour: '2-digit', minute: '2-digit' });
  });
});

describe('fillBuckets', () => {
  const zero = (at: number) => ({ at, value: 0 });

  it('extends the grid back to a first bucket the server cut one boundary before the reader asked', () => {
    // The reader's window starts at 05:30; its grid starts at 05:00. The
    // server's earliest bucket is 04:00, cut a moment before the reader's
    // request. The fill starts there rather than returning the series bare.
    const since = Date.UTC(2026, 9, 10, 5, 30);
    const until = Date.UTC(2026, 9, 10, 7, 0);
    const points = [
      { at: Date.UTC(2026, 9, 10, 4), value: 3 },
      { at: Date.UTC(2026, 9, 10, 6), value: 5 },
    ];
    const filled = fillBuckets(points, HOUR, { since, until, offsetMs: 0 }, zero);
    expect(filled.map((point) => point.at)).toEqual([
      Date.UTC(2026, 9, 10, 4),
      Date.UTC(2026, 9, 10, 5),
      Date.UTC(2026, 9, 10, 6),
      Date.UTC(2026, 9, 10, 7),
    ]);
    expect(filled[1]!.value).toBe(0);
  });

  it('still returns the series bare when its buckets are off the grid', () => {
    // Cut on another clock: filling around them would replace real buckets.
    const since = Date.UTC(2026, 9, 10, 5, 30);
    const points = [{ at: Date.UTC(2026, 9, 10, 4, 15), value: 3 }];
    const filled = fillBuckets(points, HOUR, { since, until: Date.UTC(2026, 9, 10, 7), offsetMs: 0 }, zero);
    expect(filled).toEqual(points);
  });

  it('does not extend the grid back past one boundary, so a bucket two before the start comes back bare', () => {
    // The server allows one boundary of drift, not two: a bucket further back
    // was not cut by the same clock, and the fill would invent the hours between.
    const since = Date.UTC(2026, 9, 10, 5, 30);
    const points = [
      { at: Date.UTC(2026, 9, 10, 3), value: 3 },
      { at: Date.UTC(2026, 9, 10, 6), value: 5 },
    ];
    const filled = fillBuckets(points, HOUR, { since, until: Date.UTC(2026, 9, 10, 7), offsetMs: 0 }, zero);
    expect(filled).toEqual(points);
  });
});

describe('percent', () => {
  it('does not print a minus on a change too small to show', () => {
    expect(percent(-0.00004, true)).toBe('0%');
    expect(percent(0, true)).toBe('0%');
  });

  it('keeps the sign on a change that does show', () => {
    expect(percent(-0.05, true)).toBe('-5%');
    expect(percent(0.05, true)).toBe('+5%');
    expect(percent(0.1234)).toBe('12.3%');
  });
});
