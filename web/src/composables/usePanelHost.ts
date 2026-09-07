// Where a side panel goes.
//
// A panel is a column of the flex row it belongs to — the chat's row or the
// backoffice's — not a sheet over the page, and it is opened from screens
// several components deep in that row. The row provides its own element
// here; `OaPanel` teleports into it.
//
// This is the same arrangement the hand-written version had (`openPanel`
// took a `host` and appended to it), with the difference that a component
// cannot now be handed the wrong host: there is exactly one in scope, and it
// is the row the component is already inside.

import { inject, provide, type InjectionKey, type Ref } from 'vue';

const PANEL_HOST: InjectionKey<Ref<HTMLElement | null>> = Symbol('oa-panel-host');

export function providePanelHost(host: Ref<HTMLElement | null>): void {
  provide(PANEL_HOST, host);
}

export function usePanelHost(): Ref<HTMLElement | null> {
  const host = inject(PANEL_HOST, null);
  if (!host) throw new Error('OaPanel used outside a layout that provides a panel host');
  return host;
}
