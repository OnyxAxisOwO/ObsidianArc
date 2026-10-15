// A menu that is taken away in the same tick it is opened. show() waits one
// tick before it attaches its observer and window listener, and the unmount
// has to stop that attach: nothing would be left to remove them afterwards.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import OaMenu from '../src/components/OaMenu.vue';

let app: App | undefined;

afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('OaMenu unmounted during show()', () => {
  it('attaches no observer and no resize listener once it has gone', async () => {
    const observed: unknown[] = [];
    class CountingObserver {
      constructor() { observed.push(this); }
      observe(): void {}
      disconnect(): void {}
    }
    vi.stubGlobal('ResizeObserver', CountingObserver);
    const resizeListeners = vi.spyOn(window, 'addEventListener');

    const host = document.createElement('div');
    document.body.append(host);
    const menu = ref<InstanceType<typeof OaMenu> | null>(null);
    app = createApp({
      render: () => h(OaMenu, { ref: menu }, {
        trigger: () => h('button', { type: 'button' }, 'Open'),
        default: () => h('p', 'Item'),
      }),
    });
    app.mount(host);
    await nextTick();

    menu.value!.open();
    app.unmount();
    app = undefined;
    await nextTick();
    await nextTick();

    expect(observed).toHaveLength(0);
    expect(resizeListeners.mock.calls.filter(([type]) => type === 'resize')).toHaveLength(0);
  });
});
