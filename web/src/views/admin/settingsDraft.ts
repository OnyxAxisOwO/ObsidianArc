import { computed, ref } from 'vue';

/**
 * A settings page's draft: what the page last saw for each key, and what it has
 * changed since. A save sends only the changed keys, never the whole form.
 *
 * The settings endpoint writes every key it is given. A form that re-sends a
 * field it loaded earlier would undo another administrator's edit to that
 * field, or another screen's, the moment anyone pressed Save here, so a key
 * this page did not change is not written at all.
 */
export function useSettingsDraft(collect: () => Record<string, string>) {
  // The value each key had when this page last saw the server: the load, then
  // whatever each save sent.
  const saved = ref<Record<string, string> | null>(null);

  /** The keys whose value now differs from what the page last saw. */
  function changes(values: Record<string, string> = collect()): Record<string, string> {
    const known = saved.value ?? {};
    const changed: Record<string, string> = {};
    for (const [key, value] of Object.entries(values)) {
      if (known[key] !== value) changed[key] = value;
    }
    return changed;
  }

  const dirty = computed(() => saved.value !== null && Object.keys(changes()).length > 0);

  // Merged rather than replaced. A save covers only the keys it sent, so the
  // rest keep the value this page last saw; replacing would forget them.
  function accept(values: Record<string, string> = collect()): void {
    saved.value = { ...(saved.value ?? {}), ...values };
  }

  return { dirty, changes, accept };
}
