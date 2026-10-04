// How many rows a list shows per page, remembered across reloads.
//
// One answer for every list rather than one per screen: somebody who asked
// for a hundred rows on the models page wants a hundred on the users page,
// and every list used to start again at 20 on each visit. The pagination
// control writes it; each list reads it for its starting value.

const KEY = 'obsidian-arc-page-size';
const SIZES = [10, 20, 50, 100, 200];

export const PAGE_SIZES = SIZES;

export function rememberedPageSize(): number {
  try {
    const stored = Number(localStorage.getItem(KEY));
    return SIZES.includes(stored) ? stored : 20;
  } catch {
    return 20;
  }
}

export function rememberPageSize(size: number): void {
  try {
    localStorage.setItem(KEY, String(size));
  } catch {
    // Best effort; the choice still applies until the page is reloaded.
  }
}
