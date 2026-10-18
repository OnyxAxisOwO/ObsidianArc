import type { QuotaWindowKind } from '@/admin/api';
import type { StringKey } from '@/i18n';
import { t } from '@/composables/useI18n';

/**
 * The windows a limit is set in, shortest first: the order every form and
 * list draws them in, and the one a card's scope is spelled out in. One copy,
 * so a window added later is added everywhere at once.
 */
export const QUOTA_WINDOWS: readonly QuotaWindowKind[] = ['5h', '1w', '1m'];

/** A window's own name, as a list of allowances heads its column. */
export const QUOTA_WINDOW_NAME: Record<QuotaWindowKind, StringKey> = {
  '5h': 'quota5h',
  '1w': 'quotaWeek',
  '1m': 'quotaMonth',
};

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

/**
 * The reset presets a card is issued with: everything, one window, or a
 * hand-picked set. Here rather than beside formatCardScope in lib/card-scope.ts
 * because that module rides in the first paint and these words are the
 * backoffice's own (i18n.admin.ts): named from the main chunk, they would
 * render as their keys for a reader who has not opened the backoffice.
 */
export function scopePresetChoices(): Array<{ value: string; label: string }> {
  return [
    { value: 'full', label: t('cardResetFull') },
    { value: '5h', label: t('cardReset5H') },
    { value: '1w', label: t('cardReset1W') },
    { value: '1m', label: t('cardReset1M') },
    { value: 'custom', label: t('cardResetCustom') },
  ];
}
