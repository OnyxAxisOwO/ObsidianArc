// The arithmetic behind the backoffice's usage charts.
//
// Pure and apart from the components, like lib/chart.ts, so the geometry can
// be read and tested without a renderer — and under views/admin so none of it
// reaches the chat's first paint: only screens in the backoffice chunk import
// it.

/**
 * A ceiling and the ticks up to it, at 1, 2 or 5 times a power of ten.
 *
 * The axis used to be the peak rounded up to one significant figure and cut
 * into quarters, which put ticks at 17.5 and 52.5: numbers nobody counts in,
 * on a chart of requests that can only be whole. A step from the 1-2-5
 * series lands every tick on a figure a person would say out loud.
 */
export function niceScale(peak: number, count = 4): { max: number; ticks: number[] } {
  if (!(peak > 0) || !Number.isFinite(peak)) {
    return { max: count, ticks: Array.from({ length: count + 1 }, (_, i) => i) };
  }
  const rough = peak / count;
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const residual = rough / magnitude;
  const step = (residual > 5 ? 10 : residual > 2 ? 5 : residual > 1 ? 2 : 1) * magnitude;
  const max = clean(Math.ceil(peak / step - 1e-9) * step);
  const ticks: number[] = [];
  for (let value = 0; value <= max + step / 2; value += step) ticks.push(clean(value));
  return { max, ticks };
}

/** Float noise from repeated addition, trimmed: 0.30000000000000004 → 0.3. */
function clean(value: number): number {
  return Number(value.toPrecision(12));
}

/**
 * The series with every empty bucket in the window put back as a zero.
 *
 * The server returns only the buckets something happened in — a GROUP BY has
 * nothing to group for an hour nobody asked anything — and a line drawn
 * through what it returns runs straight across a silent night as if traffic
 * had tapered off gradually. Buckets are laid out the way the server cuts
 * them: from `since` rounded down to a bucket boundary on the reader's
 * clock, which is where `offsetMs` puts it.
 */
export function fillBuckets<T extends { at: number }>(
  points: readonly T[],
  bucketMs: number,
  window: { since: number; until: number; offsetMs: number },
  zero: (at: number) => T,
): T[] {
  if (!(bucketMs > 0)) return [...points];
  const sorted = [...points].sort((a, b) => a.at - b.at);
  const floor = (at: number): number => at - (((at + window.offsetMs) % bucketMs) + bucketMs) % bucketMs;
  const start = window.since > 0 ? floor(window.since) : undefined;
  let first = start ?? sorted[0]?.at;
  // The server cut its window a moment before the reader asked for it, so its
  // earliest bucket can sit one boundary before this grid. Extending the grid
  // to it keeps the fill — but only when it lands on this grid. One that does
  // not was cut on another clock, and the guard below must still see it.
  if (start !== undefined && sorted.length && start - sorted[0]!.at === bucketMs) {
    first = sorted[0]!.at;
  }
  const last = sorted.length ? Math.max(sorted.at(-1)!.at, floor(window.until)) : floor(window.until);
  if (first === undefined || last < first) return sorted;
  // A window so long that its buckets would be thousands of points is left as
  // it came: nobody reads that many, and the gaps are the least of it.
  if ((last - first) / bucketMs > 2000) return sorted;
  // Points the grid does not land on were cut on another clock — the offset
  // changed between the request and now, say. Filling around them would
  // replace real buckets with zeros, which is worse than a straight line.
  if (sorted.some((point) => point.at < first || (point.at - first) % bucketMs !== 0)) return sorted;

  const byStart = new Map(sorted.map((point) => [point.at, point]));
  const out: T[] = [];
  for (let at = first; at <= last; at += bucketMs) out.push(byStart.get(at) ?? zero(at));
  return out;
}

/**
 * An SVG path through the points, curved but never overshooting them.
 *
 * Monotone cubic interpolation (Fritsch–Carlson): a plain spline rounds a
 * quiet hour between two busy ones into a dip below zero, which on a chart of
 * requests draws a negative number of them. This keeps each segment inside
 * the range of its two ends, so the curve only ever reads as the data does.
 */
export function smoothPath(points: ReadonlyArray<readonly [number, number]>): string {
  const n = points.length;
  if (n === 0) return '';
  const fmt = (value: number): string => value.toFixed(2);
  if (n === 1) return `M ${fmt(points[0]![0])} ${fmt(points[0]![1])}`;
  if (n === 2) return `M ${fmt(points[0]![0])} ${fmt(points[0]![1])} L ${fmt(points[1]![0])} ${fmt(points[1]![1])}`;

  const dx: number[] = [];
  const slope: number[] = [];
  for (let i = 0; i < n - 1; i += 1) {
    const run = points[i + 1]![0] - points[i]![0];
    dx.push(run);
    slope.push(run === 0 ? 0 : (points[i + 1]![1] - points[i]![1]) / run);
  }
  const tangent: number[] = [slope[0]!];
  for (let i = 1; i < n - 1; i += 1) {
    const before = slope[i - 1]!;
    const after = slope[i]!;
    // A turning point, or a flat side, gets a flat tangent: that is what
    // stops the curve from swinging past the value it is turning at.
    tangent.push(before * after <= 0 ? 0 : 3 * (dx[i - 1]! + dx[i]!) /
      ((2 * dx[i]! + dx[i - 1]!) / before + (dx[i]! + 2 * dx[i - 1]!) / after));
  }
  tangent.push(slope[n - 2]!);

  let path = `M ${fmt(points[0]![0])} ${fmt(points[0]![1])}`;
  for (let i = 0; i < n - 1; i += 1) {
    const [x0, y0] = points[i]!;
    const [x1, y1] = points[i + 1]!;
    const third = dx[i]! / 3;
    path += ` C ${fmt(x0 + third)} ${fmt(y0 + tangent[i]! * third)} ${fmt(x1 - third)} ${fmt(y1 - tangent[i + 1]! * third)} ${fmt(x1)} ${fmt(y1)}`;
  }
  return path;
}

export const HOUR = 3_600_000;
export const DAY = 24 * HOUR;
const TICK_STEPS = [HOUR, 2 * HOUR, 3 * HOUR, 4 * HOUR, 6 * HOUR, 12 * HOUR, DAY, 2 * DAY, 7 * DAY, 14 * DAY, 30 * DAY, 91 * DAY, 182 * DAY, 365 * DAY];

/**
 * Which buckets get a label on the time axis: the ones that start on a round
 * hour or day on the reader's clock, one step apart — and that step, which
 * the label format follows.
 *
 * Spreading labels evenly by index put them at 05:00, 10:00, 14:00 and 19:00,
 * which is even on the screen and nowhere else — nobody counts a day in
 * irregular hours. A step from a fixed list keeps them on 00:00, 04:00, 08:00,
 * and falls back to the even spread only when nothing lands on the grid.
 *
 * `offsetMs` is the offset fillBuckets was given, so the grid is read on the
 * clock the server cut it on. Each instant's own offset would put a day on
 * either side of a change of clock off the grid, and that day would go
 * unlabelled.
 */
export function timeTicks(
  ats: readonly number[],
  bucketMs: number,
  offsetMs: number,
  most = 6,
): { indices: number[]; step: number } {
  const span = ats.length > 1 ? ats.at(-1)! - ats[0]! : 0;
  const step = TICK_STEPS.find((candidate) => candidate >= bucketMs && span / candidate <= most) ?? TICK_STEPS.at(-1)!;
  if (ats.length <= 1) return { indices: ats.map((_, index) => index), step };
  const picked: number[] = [];
  ats.forEach((at, index) => {
    if ((at + offsetMs) % step === 0) picked.push(index);
  });
  if (picked.length >= 2) return { indices: picked, step };
  const count = Math.min(ats.length, most);
  return {
    indices: Array.from({ length: count }, (_, i) => Math.round(i * (ats.length - 1) / (count - 1))),
    step,
  };
}

/**
 * How a time-axis label reads, given the step its ticks were chosen at and the
 * span they cover.
 *
 * A label is a date when the ticks are a day or more apart. Finer ticks across
 * more than a day need the date as well: "00:00" and "12:00" over three days
 * read as the same two times repeated. A window inside one day needs only the
 * time.
 */
export function tickFormat(step: number, span: number): Intl.DateTimeFormatOptions {
  const date: Intl.DateTimeFormatOptions = { month: 'numeric', day: 'numeric' };
  const time: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' };
  if (step >= DAY) return date;
  return span > DAY ? { ...date, ...time } : time;
}

/**
 * How a figure moved against the same span before it.
 *
 * Null when there is nothing to compare with — "all time" has no before, and
 * a change from zero is not a percentage — so the screen can say "new"
 * instead of inventing an infinite rise.
 */
export function change(current: number, previous: number | undefined | null): number | null {
  if (previous === undefined || previous === null || !(previous > 0)) return null;
  return (current - previous) / previous;
}

/** 0.1234 → "12.3%", with the sign a change needs and a share does not. */
export function percent(ratio: number, signed = false): string {
  const value = Math.abs(ratio) >= 0.1 ? (ratio * 100).toFixed(1) : (ratio * 100).toFixed(2);
  const trimmed = value.replace(/\.?0+$/, '');
  // A change too small to show rounds to "-0.00" above; "-0%" would read as a
  // fall that did not happen.
  if (Number(trimmed) === 0) return '0%';
  return `${signed && ratio > 0 ? '+' : ''}${trimmed}%`;
}

/** A mean duration read the way people read waiting: 850 ms, 4.2 s, 1.3 min. */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return '—';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
  return `${(ms / 60_000).toFixed(1)} min`;
}

/**
 * How strongly a cell is filled, from 0 to 1.
 *
 * A square root rather than a straight ratio: usage is long-tailed, and on a
 * linear scale one busy hour paints every other cell the same pale shade. The
 * root keeps the quiet end tellable apart without letting it look busy.
 */
export function intensity(value: number, peak: number): number {
  if (!(peak > 0) || !(value > 0)) return 0;
  return Math.sqrt(Math.min(1, value / peak));
}
