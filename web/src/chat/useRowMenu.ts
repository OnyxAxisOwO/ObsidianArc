// The "more" menu on a conversation row, shared by the history list and the
// conversations under a project. They were two copies once, and the project
// one kept measuring against the window after the history one learned to
// measure against its list — so the same menu opened in different directions
// depending on which tab it was in.

import { ref } from 'vue';

// Roughly three rows and their padding; the menu is measured against the list
// it hangs in, not the window, because that list clips it.
const MENU_HEIGHT = 124;

export function useRowMenu() {
  /** Rows whose menu opens upwards, by conversation id. */
  const openUp = ref<Record<string, boolean>>({});

  function trigger(event: MouseEvent, id: string, toggle: () => void): void {
    const target = event.currentTarget as HTMLElement | null;
    if (target) {
      const rect = target.getBoundingClientRect();
      const box = target.closest('.ai-chat-list')?.getBoundingClientRect();
      const below = (box ? box.bottom : window.innerHeight) - rect.bottom;
      const above = rect.top - (box ? box.top : 0);
      openUp.value[id] = below < MENU_HEIGHT && above > below;
      // A short list has room in neither direction, and the menu would sit
      // half under the list's edge. It does extend the list's scrollable area,
      // so bring it into view once its opening transition has sized it.
      if (below < MENU_HEIGHT && above < MENU_HEIGHT) {
        window.setTimeout(() => {
          target.closest('.ai-chat-list-item')
            ?.querySelector('.ai-chat-context-menu')
            ?.scrollIntoView({ block: 'nearest' });
        }, 180);
      }
    }
    toggle();
  }

  // A right-click opens the same menu the row's own button does, through that
  // button, so the placement above runs for it too.
  function contextMenu(event: MouseEvent): void {
    const rowEl = event.currentTarget as HTMLElement | null;
    rowEl?.querySelector<HTMLButtonElement>('.ai-chat-list-more')?.click();
  }

  return { openUp, trigger, contextMenu };
}
