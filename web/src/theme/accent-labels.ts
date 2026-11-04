// The names the accent presets and the background tints are offered under.
// A reader's appearance settings and the backoffice's site theme both show
// them, and two copies of a label table drift the day a preset is added.
// Keys, not strings: these tables are evaluated at import time, before the
// language is known, so they are resolved with t() at render.

import type { StringKey } from '@/composables/useI18n';
import type { AccentName } from './color-utils';

export const ACCENT_LABELS: Record<AccentName, StringKey> = {
  violet: 'accentViolet', neutral: 'accentNeutral', red: 'accentRed', pink: 'accentPink',
  indigo: 'accentIndigo', blue: 'accentBlue', cyan: 'accentCyan', teal: 'accentTeal',
  green: 'accentGreen', orange: 'accentOrange',
};

export const BACKGROUND_TINTS: ReadonlyArray<{ value: AccentName; label: StringKey }> = [
  { value: 'orange', label: 'backgroundVanilla' }, { value: 'teal', label: 'backgroundMint' },
  { value: 'blue', label: 'backgroundCloud' }, { value: 'violet', label: 'backgroundLilac' },
  { value: 'pink', label: 'backgroundPeach' },
];
