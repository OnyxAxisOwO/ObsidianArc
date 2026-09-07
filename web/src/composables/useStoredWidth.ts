// The width a column was last dragged to.
//
// Read in two places that must agree: the resizer handle, which writes it,
// and the column itself, which needs it before the handle has mounted — a
// panel that opened at its default width and then snapped to the stored one
// would animate the wrong distance.

export function storedWidth(key: string, fallback: number): number {
  try {
    const raw = localStorage.getItem(key);
    const parsed = raw === null ? NaN : Number(raw);
    return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
  } catch {
    return fallback;
  }
}

export function rememberWidth(key: string, value: number): void {
  try {
    localStorage.setItem(key, String(Math.round(value)));
  } catch {
    // Best effort; the width still applies for this page.
  }
}
