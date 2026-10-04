// The side panel's lifetime on a list page: opened for a row, slid shut after
// a save, and drawn again for the next row.
//
// Closing is not instant — the panel slides out and only then tells its page
// — so for a third of a second after a save the page still believes it is
// open. A row clicked in that window would be filled into a panel that is
// about to vanish with it. `show` therefore remounts the panel when it finds
// one on its way out, which is also how a replacement keeps the column.

import { ref } from 'vue';

export function usePanelSlot() {
  const open = ref(false);
  const panel = ref<{ close(): void } | null>(null);
  /** Bound to the panel's `key`: a new value is a new panel. */
  const key = ref(0);
  let leaving = false;

  function show(): void {
    if (leaving) {
      key.value += 1;
      leaving = false;
    }
    open.value = true;
  }

  /** Slides it away. Without a mounted panel, there is nothing to wait for. */
  function hide(): void {
    if (!panel.value) {
      open.value = false;
      return;
    }
    leaving = true;
    panel.value.close();
  }

  /** The panel's own `close` event, after it has finished leaving. */
  function closed(): void {
    leaving = false;
    open.value = false;
  }

  return { open, panel, key, show, hide, closed };
}
