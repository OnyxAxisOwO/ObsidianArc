import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, nextTick, type App } from 'vue';
import { fetchUsage, type UsageSummary } from '../src/api/usage';
import ComposerMenu from '../src/chat/ComposerMenu.vue';

vi.mock('@/api/usage', () => ({ fetchUsage: vi.fn() }));

const summary = { unlimited: true, windows: [] } as unknown as UsageSummary;

/** Lets the fetch promise settle and Vue apply what it changed. */
async function settle(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
}

describe('ComposerMenu usage reading', () => {
  let app: App | null = null;
  let host: HTMLElement;

  beforeEach(() => {
    document.body.textContent = '';
    host = document.createElement('div');
    document.body.appendChild(host);
    vi.mocked(fetchUsage).mockReset();
    app = createApp(ComposerMenu, { disabled: false });
    app.mount(host);
  });

  afterEach(() => {
    app?.unmount();
    app = null;
    document.body.textContent = '';
  });

  // The trigger toggles the menu, so every second click closes it again.
  function trigger(): HTMLButtonElement {
    return host.querySelector<HTMLButtonElement>('.ai-chat-plus')!;
  }

  it('reads usage again on every open, not only the first', async () => {
    vi.mocked(fetchUsage).mockResolvedValue(summary);

    trigger().click();
    await settle();
    trigger().click();
    await settle();
    trigger().click();
    await settle();

    expect(fetchUsage).toHaveBeenCalledTimes(2);
  });

  it('asks again on the next open after a failed read', async () => {
    vi.mocked(fetchUsage).mockRejectedValueOnce(new Error('upstream down')).mockResolvedValue(summary);

    trigger().click();
    await settle();
    trigger().click();
    await settle();
    trigger().click();
    await settle();

    expect(fetchUsage).toHaveBeenCalledTimes(2);
  });
});
