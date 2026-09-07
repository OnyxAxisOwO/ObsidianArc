// How a reasoning style is named on screen.
//
// Shared by the providers screen and the models screen, which offers the same
// list plus an "inherit" entry.

import { t } from '@/composables/useI18n';
import type { ReasoningStyle } from '@/admin/api';

export function reasoningLabel(style: ReasoningStyle): string {
  switch (style) {
    case 'auto': return t('styleAuto');
    case 'none': return t('styleNone');
    case 'anthropic': return t('styleAnthropic');
    case 'openai_effort': return t('styleEffort');
    case 'openrouter': return t('styleOpenRouter');
    case 'qwen': return t('styleQwen');
  }
}
