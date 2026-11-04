// A menu item that asks once before it acts, for the rail's deletes.
//
// Deleting a conversation is permanent and has no undo, and the menu it sits
// in is where the reader already is, so the question is asked there: the first
// press arms the item and the second carries it out. A confirmation dialog is
// avoided because a browser may suppress it, which leaves a button that does
// nothing at all.

import { onBeforeUnmount, ref } from 'vue';

const ARM_MS = 4000;

export function useArmedItem() {
  const armed = ref('');
  let timer = 0;

  function disarm(): void {
    window.clearTimeout(timer);
    timer = 0;
    armed.value = '';
  }

  /** True on the second press for `id`. The first press only arms it. */
  function press(id: string): boolean {
    if (armed.value === id) {
      disarm();
      return true;
    }
    disarm();
    armed.value = id;
    timer = window.setTimeout(disarm, ARM_MS);
    return false;
  }

  // The timer must not fire into a list that has been replaced.
  onBeforeUnmount(disarm);

  return { armed, press };
}
