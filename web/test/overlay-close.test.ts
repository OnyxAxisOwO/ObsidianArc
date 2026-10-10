import { describe, it, expect, afterEach, vi } from 'vitest';
import { createApp, h, type App as VueApp } from 'vue';
import OaOverlay from '../src/components/OaOverlay.vue';

// A sheet tells its caller about a close only after its 200 ms fade, and the
// caller's handler usually navigates. A sheet taken away inside that window has
// to leave nothing behind: the fade timer is cleared, and the caller hears nothing.
const FADE_MS = 200;
const PAST_THE_FADE_MS = FADE_MS + 60;

const wait = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

describe('OaOverlay close timer', () => {
  let app: VueApp | null = null;
  let host: HTMLElement | null = null;

  afterEach(() => {
    app?.unmount();
    app = null;
    host?.remove();
    host = null;
    vi.restoreAllMocks();
  });

  function mountSheet(onClose: () => void): void {
    const root = document.createElement('div');
    document.body.appendChild(root);
    host = root;
    app = createApp({
      render: () => h(OaOverlay, { overlayClass: 'test-sheet', onClose }, { default: () => h('p', 'content') }),
    });
    app.mount(root);
  }

  function pressEscape(): void {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  }

  it('tells its caller once the fade is over', async () => {
    let navigated = 0;
    mountSheet(() => { navigated += 1; });

    pressEscape();
    await wait(PAST_THE_FADE_MS);

    expect(navigated).toBe(1);
  });

  it('clears its fade timer and tells nobody when it is taken away during the fade', async () => {
    let navigated = 0;
    mountSheet(() => { navigated += 1; });
    const setTimer = vi.spyOn(window, 'setTimeout');
    const clearTimer = vi.spyOn(window, 'clearTimeout');

    pressEscape();
    const fade = setTimer.mock.calls.findIndex(([, ms]) => ms === FADE_MS);
    const timer = setTimer.mock.results[fade]?.value;
    expect(timer).toBeDefined();

    app?.unmount();
    app = null;
    expect(clearTimer).toHaveBeenCalledWith(timer);

    await wait(PAST_THE_FADE_MS);
    expect(navigated).toBe(0);
  });
});
