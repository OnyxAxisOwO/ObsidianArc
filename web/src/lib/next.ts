/**
 * A destination carried through the sign-in page, kept to this site.
 *
 * The value ends up in a navigation, so anything that could name another host
 * — an absolute URL, a protocol-relative "//elsewhere", a backslash some
 * browsers read as a slash — is dropped rather than repaired. The server
 * applies the same rule to the one it builds; this is the half that runs
 * where the value is actually used.
 *
 * Here rather than beside the router, for the reason formatUptime is here:
 * the sign-in card needs this one function, and importing it from the router
 * would pull every screen in the routing table into the card's own graph.
 */
export function safeNext(raw: unknown): string {
  if (typeof raw !== 'string') return '';
  const value = raw.trim();
  if (!value.startsWith('/') || value.startsWith('//')) return '';
  // Anywhere in the value, not only the front: a URL parser deletes TAB, CR
  // and LF before it looks at the string, so "/<TAB>/evil.example" is
  // "//evil.example" by the time it is followed, and a backslash is read as a
  // slash wherever it falls.
  if (/[\u0000-\u001f\u007f\\]/.test(value)) return '';
  return value;
}

/**
 * Whether a destination belongs to the server rather than to the interface.
 *
 * The one that matters is /oauth/authorize: another site sends a signed-out
 * visitor through the sign-in page with it as `next`, and the router has no
 * such screen — handing it to the router drew "no such page" in place of the
 * request the other site is waiting on. It needs a real navigation.
 */
export function serverOwned(path: string): boolean {
  return path.startsWith('/oauth/authorize') || path.startsWith('/api/');
}
