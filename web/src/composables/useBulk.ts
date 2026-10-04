// Acting on several ticked rows at once.
//
// There is no bulk endpoint. Each row goes through the request its own panel
// uses, so every check that request makes — the last administrator, a model
// still routed to — applies here unchanged, and a new rule on the server
// cannot be skipped by ticking a box. They run one after another rather than
// together: a few dozen rows is a second or two, and a burst of writes at one
// SQLite file is not worth saving it.

import { computed, ref } from 'vue';
import { ApiError } from '@/api/client';
import { t } from '@/composables/useI18n';

export function useBulk<T>(rows: () => readonly T[], key: (row: T) => string) {
  const selected = ref<string[]>([]);
  const busy = ref(false);
  const error = ref('');

  const chosen = computed(() => {
    const keys = new Set(selected.value);
    return rows().filter((row) => keys.has(key(row)));
  });

  function selectAll(): void {
    selected.value = rows().map(key);
  }

  function clear(): void {
    selected.value = [];
    error.value = '';
  }

  /**
   * Runs `step` over the ticked rows, then `done` — the page's own reload.
   *
   * What worked is unticked and what did not stays ticked, so a retry is one
   * click and the screen never claims a row was changed that was not.
   */
  async function run(step: (row: T) => Promise<unknown>, done: () => unknown): Promise<void> {
    if (busy.value) return;
    busy.value = true;
    error.value = '';
    const targets = chosen.value;
    const failed: string[] = [];
    let reason = '';
    for (const row of targets) {
      try {
        await step(row);
      } catch (failure) {
        failed.push(key(row));
        reason ||= failure instanceof ApiError ? failure.message : String(failure);
      }
    }
    selected.value = failed;
    if (failed.length) error.value = t('bulkPartial', { failed: failed.length, total: targets.length, reason });
    try {
      await done();
    } finally {
      busy.value = false;
    }
  }

  return { selected, busy, error, selectAll, clear, run };
}
