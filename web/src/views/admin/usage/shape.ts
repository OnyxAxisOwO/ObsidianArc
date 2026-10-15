// The shapes the usage charts share, in a module of their own because a
// component's <script setup> can declare them but cannot export them.

import type { UsageBreakdown } from '@/admin/api';

export interface PlotPoint {
  at: number;
  value: number;
}

/** What a board ranks by. `users` is popularity: how many people, not how much. */
export type BoardMetric = 'users' | 'requests' | 'tokens' | 'credits';
// `status` is the outcome of a request, not a provider: its rows are labels
// like "Failed", which the provider category must not mask.
export type BoardKind = 'model' | 'user' | 'group' | 'provider' | 'status';

/**
 * The widest audience among the rows. A reduce, not Math.max over a spread:
 * the spread passes every row as an argument, and a large instance has enough
 * rows for the call to overflow the stack.
 */
export function largestUsers(rows: readonly Pick<UsageBreakdown, 'users'>[]): number {
  return rows.reduce((max, row) => Math.max(max, row.users ?? 0), 0);
}

export function metricOf(row: UsageBreakdown, metric: BoardMetric): number {
  if (metric === 'users') return row.users ?? 0;
  if (metric === 'requests') return row.requests;
  if (metric === 'tokens') return row.total_tokens;
  return row.credits;
}
