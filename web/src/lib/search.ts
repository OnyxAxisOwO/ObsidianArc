export function matchesSearch(query: string, ...text: string[]): boolean {
  const normalize = (value: string) => value.normalize('NFKC').toLocaleLowerCase();
  const haystack = normalize(text.join(' '));
  return normalize(query).trim().split(/\s+/).every((word) => haystack.includes(word));
}
