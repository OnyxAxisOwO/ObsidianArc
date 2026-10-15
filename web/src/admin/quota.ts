/**
 * A window figure the operator left at zero is sent as blank, "no limit": the
 * server refuses a window that is not positive, because a window at zero would
 * show in the badge and enforce nothing, and failing the whole save over a
 * zero left in a field is worse than dropping it. Anything else, negatives
 * included, is passed through so the save's own check can refuse it before
 * anything is written. Dropping a negative here would save an unenforced window
 * with no message.
 */
export function windowFigure(value: number | null): number | null {
  return value === 0 ? null : value;
}
