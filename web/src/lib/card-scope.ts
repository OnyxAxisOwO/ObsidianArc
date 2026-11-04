import { t, type StringKey } from '@/composables/useI18n';

// The windows a card can reset, in the order a card names them. A window is worded
// alone when it is the only one, and by its shorter part when it sits in a list.
const CARD_WINDOWS: readonly { window: string; alone: StringKey; part: StringKey }[] = [
  { window: '5h', alone: 'cardScope5H', part: 'cardScopeLabel5H' },
  { window: '1w', alone: 'cardScope1W', part: 'cardScopeLabel1W' },
  { window: '1m', alone: 'cardScope1M', part: 'cardScopeLabel1M' },
];

/** What a card resets, in words. Shared by the usage screen and the admin screens, so they name a card the same way. */
export function formatCardScope(card: { windows?: string[] }): string {
  const wins = card.windows ?? [];
  if (wins.length === 0 || wins.includes('full')) {
    return t('cardFullReset');
  }
  const order = CARD_WINDOWS.map((row) => row.window);
  const sorted = [...wins].sort((a, b) => order.indexOf(a) - order.indexOf(b));
  const known = (w: string) => CARD_WINDOWS.find((row) => row.window === w);
  const lone = sorted.length === 1 ? known(sorted[0]!) : undefined;
  if (lone) return t(lone.alone);
  // A window this build has no name for is shown as the server spelled it,
  // rather than dropped from the list of what the card resets.
  const labels = sorted.map((w) => {
    const row = known(w);
    return row ? t(row.part) : w;
  });
  return t('cardScopeCombined', { windows: labels.join(' + ') });
}

/** The name a card was given, or else what it resets. */
export function cardTitle(card: { name?: string; windows?: string[] }): string {
  if (card.name && card.name.trim()) {
    return card.name.trim();
  }
  return formatCardScope(card);
}

/** The three windows as choices, shortest first, labelled the way the quota names them. */
export function windowChoices(): Array<{ value: string; label: string }> {
  return CARD_WINDOWS.map((row) => ({ value: row.window, label: t(row.part) }));
}
