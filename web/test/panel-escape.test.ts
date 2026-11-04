import { describe, it, expect, afterEach } from 'vitest';
import { createApp, h, nextTick, ref, type App as VueApp } from 'vue';
import OaPanel from '../src/components/OaPanel.vue';
import { providePanelHost } from '../src/composables/usePanelHost';

// A backoffice drawer and an account panel open beside it. Both listen for
// Escape on document, and one key used to close both: the drawer's unsaved input
// went with it. Only the panel opened last may answer.
describe('OaPanel Escape with two panels open', () => {
  let app: VueApp | null = null;
  let host: HTMLElement | null = null;

  afterEach(() => {
    app?.unmount();
    app = null;
    host?.remove();
    host = null;
  });

  function pressEscape(): void {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  }

  it('closes only the panel opened last, and the one beneath answers once it is gone', async () => {
    const closed: string[] = [];
    const accountOpen = ref(true);

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
          accountOpen.value
            ? h(OaPanel, {
              title: 'Account',
              immediateClose: true,
              onClose: () => { closed.push('account'); },
            })
            : null,
        ]);
      },
    });
    app.mount(root);
    await nextTick();

    pressEscape();
    expect(closed).toEqual(['account']);

    accountOpen.value = false;
    await nextTick();
    pressEscape();
    expect(closed).toEqual(['account', 'drawer']);
  });
});
