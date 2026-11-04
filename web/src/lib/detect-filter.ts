// The detected-models pickers filter what the provider listed; a provider such
// as an aggregator lists hundreds, and scrolling is no way to find one.
export function filterDetected<T extends { model_id: string; display_name: string }>(
  entries: readonly T[],
  query: string,
): T[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return [...entries];
  return entries.filter((entry) => entry.model_id.toLowerCase().includes(needle)
    || entry.display_name.toLowerCase().includes(needle));
}
