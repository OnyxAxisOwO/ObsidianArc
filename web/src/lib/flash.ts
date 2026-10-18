// A control's word for what just happened ("Saved", "Copied"), shown on the
// control itself and put back after a moment. One length for all of them, so
// the backoffice's buttons do not each settle on a different pause.

import { t } from '@/composables/useI18n';

export const FEEDBACK_FLASH_MS = 1500;

/**
 * Writes a copy's result onto its button and clears it after the flash.
 *
 * The clipboard call is the caller's (lib/clipboard), passed in as its promise,
 * so this file stays free of how copying is done. A refused copy says so: a
 * button that stayed silent would read as having copied.
 *
 * The returned function cancels both the pending write and the clear. A caller
 * calls it before the next copy, so an older result cannot clear a newer one
 * early, and on unmount, so nothing writes to a screen that has gone.
 */
export function flashCopy(pending: Promise<boolean>, setLabel: (text: string) => void): () => void {
  let cancelled = false;
  let timer = 0;
  void pending.then((ok) => {
    if (cancelled) return;
    setLabel(ok ? t('copied') : t('copyFailed'));
    timer = window.setTimeout(() => { setLabel(''); }, FEEDBACK_FLASH_MS);
  });
  return () => {
    cancelled = true;
    window.clearTimeout(timer);
  };
}
