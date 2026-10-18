import { t } from '@/composables/useI18n';

/**
 * What the windows of a card allow, in words. No window, or the full one, is a
 * full reset; a window this build has no name for is shown as the server spelled it.
 */
export function formatCardScope(windows?: string[]): string {
  const wins = windows ?? [];
  if (wins.length === 0 || wins.includes('full')) {
    return t('cardFullReset');
  }
  // Shortest window first, so a card lists its windows in the order the quota
  // itself reads them, whatever order the server sent them in.
  const order = ['5h', '1w', '1m'];
  const sorted = [...wins].sort((a, b) => order.indexOf(a) - order.indexOf(b));
  // Two wordings of each window on purpose: one that stands alone as a card's
  // whole scope ("5-hour reset"), and one that reads inside a list of several
  // ("Reset 5 hours + 1 week").
  if (sorted.length === 1) {
    if (sorted[0] === '5h') return t('cardScope5H');
    if (sorted[0] === '1w') return t('cardScope1W');
    if (sorted[0] === '1m') return t('cardScope1M');
  }
  const labels = sorted.map((w) => {
    if (w === '5h') return t('cardScopeLabel5H');
    if (w === '1w') return t('cardScopeLabel1W');
    if (w === '1m') return t('cardScopeLabel1M');
    return w;
  });
  return t('cardScopeCombined', { windows: labels.join(' + ') });
}

/** The card's own name when it has one, otherwise what it allows. */
export function cardTitle(card: { name?: string; windows?: string[] }): string {
  if (card.name && card.name.trim()) {
    return card.name.trim();
  }
  return formatCardScope(card.windows);
}

/** The three windows as choices, shortest first, labelled the way the quota names them. */
export function windowChoices(): Array<{ value: string; label: string }> {
  return [
    { value: '5h', label: t('cardScopeLabel5H') },
    { value: '1w', label: t('cardScopeLabel1W') },
    { value: '1m', label: t('cardScopeLabel1M') },
  ];
}
