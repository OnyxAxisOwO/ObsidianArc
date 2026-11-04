// Which link targets the interface will open. Kept out of the chat renderer
// that uses it most, so a screen that only needs the check — the update
// notice's release link — does not import a markdown parser to get it.

export function safeHref(value: string): string | null {
  const href = String(value ?? '').trim();
  // Anything with a scheme that is not one of these two, and anything with no
  // scheme at all, is not something this interface can usefully open.
  return /^(?:https?:\/\/|mailto:)[^\s]+$/i.test(href) ? href : null;
}
