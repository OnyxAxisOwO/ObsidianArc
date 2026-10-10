// Where a side panel goes when it moves on or is finished with.
//
// In the chat a panel is a child route of "/" (router/index.ts): opening one is
// a navigation to its address, and closing one goes back to "/". Over the
// backoffice that navigation takes the administration page away with it, and
// the chat flashes up behind the column. So the backoffice draws the panel
// itself and names it in its own address, in one query parameter. Closing
// removes that parameter and leaves the page's path, query and hash where they
// were, and the page itself never unmounts.
//
// AdminPage provides the two operations below. A panel asks for them through
// usePanelExit, which falls back to the router where nothing is provided, so
// one panel serves both places without knowing which one it is in.

import { inject, provide, type InjectionKey } from 'vue';
import { useRouter } from 'vue-router';

/** The query parameter that names a panel drawn over the backoffice. */
export const BACKOFFICE_PANEL_PARAM = 'panel';

export interface BackofficePanels {
  /**
   * Draws the named panel beside the page. It is a history entry of its own,
   * so the browser's Back button takes the panel away again.
   */
  show(name: string): void;
  /** Takes the panel out of the address and keeps everything else in it. */
  hide(): void;
}

export interface PanelExit {
  /** Opens another panel, by its address in the chat: `/usage`, `/x/tips/1`. */
  open(path: string): void;
  /**
   * Ends this panel. In the chat it goes to `/`, by push or by replace as each
   * panel always has; over the backoffice the panel's parameter is removed.
   */
  close(how?: 'push' | 'replace'): void;
}

const BACKOFFICE_PANELS: InjectionKey<BackofficePanels> = Symbol('oa-backoffice-panels');

export function provideBackofficePanels(panels: BackofficePanels): void {
  provide(BACKOFFICE_PANELS, panels);
}

export function usePanelExit(): PanelExit {
  const router = useRouter();
  const over = inject(BACKOFFICE_PANELS, null);
  return {
    open(path) {
      if (over) over.show(path.replace(/^\//, ''));
      else void router.push(path);
    },
    close(how = 'replace') {
      if (over) over.hide();
      else if (how === 'push') void router.push('/');
      else void router.replace('/');
    },
  };
}
