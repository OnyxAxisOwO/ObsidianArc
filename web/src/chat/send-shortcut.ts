import { computed } from 'vue';
import { currentPreferences } from '@/stores/session';

export const sendOnEnter = computed(() => currentPreferences.value['send_on_enter'] === true);

export function isSendKey(event: KeyboardEvent): boolean {
  // Safari can end composition before the confirming keydown; 229 still
  // identifies that IME event when isComposing has already become false.
  if (event.key !== 'Enter' || event.isComposing || event.keyCode === 229 || event.repeat) return false;
  if (event.ctrlKey || event.metaKey || event.altKey) return false;
  return event.shiftKey !== sendOnEnter.value;
}
