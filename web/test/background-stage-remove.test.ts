// Removing an uploaded login background discards its HTML source too, so it
// takes the same two clicks as every other destructive control in the
// backoffice: the first arms the button, and only the second removes.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import { adminApi } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import type { SiteBackground } from '../src/theme/theme';
import { setThemeMode } from '../src/theme/theme';
import BackgroundStage from '../src/views/admin/BackgroundStage.vue';

const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
};

let app: App | undefined;
let host: HTMLElement;
const originalMatchMedia = window.matchMedia;

beforeEach(async () => {
  await changeLanguage('en');
  window.matchMedia = vi.fn((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(() => false),
  })) as unknown as typeof window.matchMedia;
  setThemeMode('light');
  host = document.createElement('div');
  document.body.append(host);
});

afterEach(() => {
  app?.unmount();
  app = undefined;
  host.remove();
  window.matchMedia = originalMatchMedia;
  setThemeMode('auto');
  vi.restoreAllMocks();
});

async function mountStage(): Promise<void> {
  // Both schemes are set, so the stage has a background whichever one the
  // theme is in when it mounts.
  const backgrounds: Record<string, SiteBackground> = {
    landscape_light: { url: '/light.png', html: false },
    landscape_dark: { url: '/dark.png', html: false },
  };
  app = createApp({ render: () => h(BackgroundStage, { prefix: '', backgrounds }) });
  app.mount(host);
  await settle();
}

describe('removing a login background', () => {
  it('removes it only on the second click, and the first click sends nothing', async () => {
    const remove = vi.spyOn(adminApi, 'deleteLoginBackground').mockResolvedValue(undefined as never);
    await mountStage();

    const trash = host.querySelector<HTMLButtonElement>('.oa-bg-stage-actions button.oa-icon-btn')!;
    expect(trash).not.toBeNull();
    trash.click();
    await nextTick();
    expect(remove).not.toHaveBeenCalled();

    trash.click();
    await settle();
    expect(remove).toHaveBeenCalledTimes(1);
    expect(remove.mock.calls[0]![0]).toMatch(/^landscape_(light|dark)$/);
  });

  it('opens an empty editor after a removal, not the draft typed for the page that was removed', async () => {
    vi.spyOn(adminApi, 'deleteLoginBackground').mockResolvedValue(undefined as never);
    await mountStage();

    const edit = () => [...host.querySelectorAll<HTMLButtonElement>('.oa-bg-stage-actions button')]
      .find((button) => button.textContent?.includes(t('bgHtmlEdit')))!;
    const source = () => document.querySelector<HTMLTextAreaElement>('.oa-bg-html-source');

    edit().click();
    await settle();
    const field = source()!;
    field.value = 'unsaved draft';
    field.dispatchEvent(new Event('input'));
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    // The sheet fades for 200ms before it reports the close.
    await new Promise((resolve) => setTimeout(resolve, 260));
    await settle();
    expect(source()).toBeNull();

    const trash = host.querySelector<HTMLButtonElement>('.oa-bg-stage-actions button.oa-icon-btn')!;
    trash.click();
    await nextTick();
    trash.click();
    await settle();

    edit().click();
    await settle();
    expect(source()!.value).toBe('');
  });
});
