import { HOUR_MS } from '@/lib/time';

/**
 * The until bound for a custom range whose last millisecond is pickedEnd. The
 * server treats until as exclusive and rounds it down to the hour, so a bound
 * inside an hour drops that whole hour from the figures and the ledger. Rounding
 * up to the next hour boundary keeps the hour that holds the picked end.
 */
export function customUntilBound(pickedEnd: number): number {
  return Math.ceil((pickedEnd + 1) / HOUR_MS) * HOUR_MS;
}
