// What the signed-in user has spent, and against what limits.
//
// The server is the authority: these numbers are for display, and the quota
// that actually stops a request is enforced in the chat gateway. The
// interface never decides whether a turn is allowed.

import { ApiError, api } from './client';

export type WindowKind = '5h' | '1w' | '1m';

export interface UsageWindow {
  kind: WindowKind;
  /** Whether this window is enforced at all for this account. */
  enforced: boolean;
  used_requests: number;
  used_tokens: number;
  used_credits: number;
  /** Null means "no limit on this dimension". */
  limit_requests: number | null;
  limit_tokens: number | null;
  limit_credits: number | null;
  /** Epoch milliseconds at which the window rolls over. */
  resets_at: number;
}

/**
 * How the instance wants an allowance phrased. The same numbers either way —
 * an operator running generous limits wants "80% left" to reassure, one
 * running tight ones wants "20% used" or the figures themselves.
 */
export type UsageDisplay = 'absolute' | 'remaining' | 'used';

export interface UsageSummary {
  /** True when nothing constrains this account — an administrator, usually. */
  unlimited: boolean;
  /** Older builds do not send this; absolute is what they always did. */
  display?: UsageDisplay;
  windows: UsageWindow[];
}

/**
 * Returns null when this build does not report usage yet, so a caller can
 * simply hide the row rather than special-casing a shape it cannot draw.
 */
export async function fetchUsage(): Promise<UsageSummary | null> {
  try {
    return await api.get<UsageSummary>('/api/usage/me');
  } catch (error) {
    if (error instanceof ApiError && (error.status === 404 || error.status === 501)) return null;
    throw error;
  }
}

/**
 * The tightest ratio across the dimensions a window limits, as 0-1. Which
 * dimension is closest to its ceiling is what a user actually needs to know,
 * and showing three bars for one window would be noise.
 */
export function windowPressure(window: UsageWindow): number | null {
  const ratios: number[] = [];
  if (window.limit_requests) ratios.push(window.used_requests / window.limit_requests);
  if (window.limit_tokens) ratios.push(window.used_tokens / window.limit_tokens);
  if (window.limit_credits) ratios.push(window.used_credits / window.limit_credits);
  if (!ratios.length) return null;
  return Math.min(1, Math.max(...ratios));
}

/** The used/limit pair for whichever dimension is under the most pressure. */
export function windowFigures(window: UsageWindow): { used: number; limit: number } | null {
  const candidates: Array<{ used: number; limit: number }> = [];
  if (window.limit_requests) candidates.push({ used: window.used_requests, limit: window.limit_requests });
  if (window.limit_tokens) candidates.push({ used: window.used_tokens, limit: window.limit_tokens });
  if (window.limit_credits) candidates.push({ used: window.used_credits, limit: window.limit_credits });
  if (!candidates.length) return null;
  return candidates.reduce((worst, entry) => (entry.used / entry.limit > worst.used / worst.limit ? entry : worst));
}
