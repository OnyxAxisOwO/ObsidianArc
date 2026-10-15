// Escape with a side panel, a sheet and a menu open at once. The key goes to
// the surface opened last, and a key one of them has already handled is not
// passed on: before, a sheet over a panel closed the panel too, and a menu did
// the same through its window-level listener.

import { afterEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, ref, type App as VueApp } from 'vue';
import OaMenu from '../src/components/OaMenu.vue';
import OaOverlay from '../src/components/OaOverlay.vue';
import OaPanel from '../src/components/OaPanel.vue';
import { providePanelHost } from '../src/composables/usePanelHost';

// The sheet's fade is 200 ms before it tells its caller it has closed.
const PAST_THE_FADE_MS = 260;
const wait = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

describe('Escape across a panel, a sheet and a menu', () => {
  let app: VueApp | null = null;
  let host: HTMLElement | null = null;

  afterEach(() => {
    app?.unmount();
    app = null;
    host?.remove();
    host = null;
  });

  function pressEscape(): void {
    // Cancelable as a real keydown is: the menu and the sheet say they took the key by preventing it.
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true }));
  }

  it('closes the sheet on top and leaves the panel beneath open until the sheet is gone', async () => {
    const closed: string[] = [];
    const sheetOpen = ref(false);

    const root = document.createElement('div');
    document.body.appendChild(root);
    host = root;
    app = createApp({
      setup() {
        const row = document.createElement('div');
        root.appendChild(row);
        providePanelHost(ref(row));
        return () => h('div', [
          h(OaPanel, {
            title: 'Drawer',
            immediateClose: true,
            onClose: () => { closed.push('drawer'); },
          }),
          sheetOpen.value
            ? h(OaOverlay, {
              overlayClass: 'test-sheet',
              onClose: () => { sheetOpen.value = false; },
            }, { default: () => h('p', 'Sheet') })
            : null,
        ]);
      },
    });
    app.mount(root);
    await nextTick();

    sheetOpen.value = true;
    await nextTick();
    pressEscape();
    await wait(PAST_THE_FADE_MS);
    await nextTick();
    expect(sheetOpen.value).toBe(false);
    expect(closed).toEqual([]);

    pressEscape();
    expect(closed).toEqual(['drawer']);
  });

  it('lets an open menu take the key without closing the panel beneath it', async () => {
    const closed: string[] = [];
    const menu = ref<InstanceType<typeof OaMenu> | null>(null);

    const root = document.createElement('div');
    document.body.appendChild(root);
    host = root;
    app = createApp({
      setup() {
        const row = document.createElement('div');
        root.appendChild(row);
        providePanelHost(ref(row));
        return () => h('div', [
          h(OaPanel, {
            title: 'Drawer',
            immediateClose: true,
            onClose: () => { closed.push('drawer'); },
          }),
          h(OaMenu, { ref: menu }, {
            trigger: () => h('button', { type: 'button' }, 'Menu'),
            default: () => h('p', 'Item'),
          }),
        ]);
      },
    });
    app.mount(root);
    await nextTick();

    menu.value?.open();
    expect(menu.value?.isOpen()).toBe(true);
    pressEscape();
    expect(menu.value?.isOpen()).toBe(false);
    expect(closed).toEqual([]);

    pressEscape();
    expect(closed).toEqual(['drawer']);
  });
});
