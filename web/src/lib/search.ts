export function matchesSearch(query: string, ...text: string[]): boolean {
  // An empty query matches everything. Checked first so that a screen asking on
  // every render does not normalize its whole text for nothing.
  if (!query.trim()) return true;
  const normalize = (value: string) => value.normalize('NFKC').toLocaleLowerCase();
  const haystack = normalize(text.join(' '));
  return normalize(query).trim().split(/\s+/).every((word) => haystack.includes(word));
}
