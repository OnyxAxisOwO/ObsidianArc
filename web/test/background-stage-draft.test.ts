// The HTML background editor keeps what the operator typed. Closing the sheet
// with Escape or a click outside it must not throw the draft away, and a stored
// page that arrives late must not overwrite typing already on screen. Cancel is
// the one control that discards on purpose.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, shallowRef, type App } from 'vue';
import { adminApi } from '../src/admin/api';
import { changeLanguage, t } from '../src/composables/useI18n';
import { setThemeMode, type SiteBackground } from '../src/theme/theme';
import BackgroundStage from '../src/views/admin/BackgroundStage.vue';

const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await nextTick();
};
// Reading a response body takes more than one turn of the event loop.
const flush = async () => {
  for (let i = 0; i < 5; i++) await settle();
};

const stored = '<main>stored page</main>';

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
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

async function mountStage(onStatus: (message: string, ok: boolean) => void = () => {}): Promise<void> {
  // Both schemes hold the same HTML page, so the stage has one whichever scheme
  // the theme is in when it mounts.
  const page = { url: '/stage.html', html: true };
  app = createApp({
    render: () => h(BackgroundStage, { prefix: '', backgrounds: { landscape_light: page, landscape_dark: page }, onStatus }),
  });
  app.mount(host);
  await settle();
}

const editButton = () => [...host.querySelectorAll<HTMLButtonElement>('.oa-bg-stage-actions button')]
  .find((button) => button.textContent?.includes(t('bgHtmlEdit')))!;
// The sheet teleports to the body, so it is looked for there.
const source = () => document.querySelector<HTMLTextAreaElement>('.oa-bg-html-source');
const cancelButton = () => [...document.querySelectorAll<HTMLButtonElement>('.oa-bg-html-actions button')]
  .find((button) => button.textContent?.trim() === t('cancel'))!;
const typeIn = (field: HTMLTextAreaElement, value: string) => {
  field.value = value;
  field.dispatchEvent(new Event('input'));
};
const pressEscape = () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));

describe('the HTML background editor', () => {
  it('keeps a typed draft when the sheet is closed and opened again', async () => {
    const fetchPage = vi.fn(async () => new Response(stored, { status: 200 }));
    vi.stubGlobal('fetch', fetchPage);
    await mountStage();

    editButton().click();
    await flush();
    expect(source()!.value).toBe(stored);

    typeIn(source()!, 'my unsaved draft');
    pressEscape();
    // The sheet fades for 200ms before it reports the close.
    await new Promise((resolve) => setTimeout(resolve, 260));
    await flush();
    expect(source()).toBeNull();

    editButton().click();
    await flush();
    expect(source()!.value).toBe('my unsaved draft');
    // The draft is somebody's work: reopening does not read the stored page over it.
    expect(fetchPage).toHaveBeenCalledTimes(1);
  });

  it('keeps the editor shut and says so when the stored page cannot be read', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('upstream gone', { status: 500 })));
    const onStatus = vi.fn();
    await mountStage(onStatus);

    editButton().click();
    await flush();
    // Opened empty, the editor would read as a variant with no page, and Save
    // would replace the stored one. Shut, the operator is told and can try again.
    expect(source()).toBeNull();
    expect(onStatus).toHaveBeenCalledWith(t('failed'), false);
  });

  it('does not replace typing on screen when the stored page arrives late', async () => {
    let answer!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { answer = resolve; })));
    await mountStage();

    editButton().click();
    await settle();
    typeIn(source()!, 'typed first');
    answer(new Response(stored, { status: 200 }));
    await flush();

    expect(source()!.value).toBe('typed first');
  });

  it('throws the draft away only when Cancel is pressed', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(stored, { status: 200 })));
    await mountStage();

    editButton().click();
    await flush();
    typeIn(source()!, 'discard me');
    cancelButton().click();
    await flush();
    expect(source()).toBeNull();

    editButton().click();
    await flush();
    expect(source()!.value).toBe(stored);
  });

  it('does not put a page still being fetched into the editor once the page has been removed', async () => {
    const page = { url: '/stage.html', html: true };
    const plain = { url: '/stage.png', html: false };
    let answer!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { answer = resolve; })));
    const backgrounds = shallowRef<Record<string, SiteBackground>>({ landscape_light: page, landscape_dark: page });
    app = createApp({ render: () => h(BackgroundStage, { prefix: '', backgrounds: backgrounds.value }) });
    app.mount(host);
    await settle();

    editButton().click();
    await settle();
    pressEscape();
    await new Promise((resolve) => setTimeout(resolve, 260));
    await flush();

    // The page is gone, and the editor is opened again for the variant that no longer has one.
    backgrounds.value = { landscape_light: plain, landscape_dark: plain };
    await settle();
    editButton().click();
    await settle();
    answer(new Response(stored, { status: 200 }));
    await flush();

    expect(source()!.value).toBe('');
  });

  it('does not put a page still being fetched into the editor when the page is removed under it', async () => {
    const page = { url: '/stage.html', html: true };
    let answer!: (response: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { answer = resolve; })));
    const remove = vi.spyOn(adminApi, 'deleteLoginBackground').mockResolvedValue(undefined as never);
    app = createApp({ render: () => h(BackgroundStage, { prefix: '', backgrounds: { landscape_light: page, landscape_dark: page } }) });
    app.mount(host);
    await settle();

    editButton().click();
    await settle();
    const trash = host.querySelector<HTMLButtonElement>('.oa-bg-stage-actions button.oa-icon-btn')!;
    trash.click();
    await nextTick();
    trash.click();
    await settle();
    expect(remove).toHaveBeenCalledTimes(1);

    answer(new Response(stored, { status: 200 }));
    await flush();

    expect(source()!.value).toBe('');
  });

  it('does not show a draft typed for one screen under another, nor upload it as that one', async () => {
    const desktop = { url: '/stage.html', html: true };
    const phone = { url: '/phone.html', html: true };
    const fetchPage = vi.fn(async (url: string) => new Response(url === phone.url ? '<main>phone page</main>' : stored, { status: 200 }));
    vi.stubGlobal('fetch', fetchPage);
    app = createApp({
      render: () => h(BackgroundStage, {
        prefix: '',
        backgrounds: { landscape_light: desktop, landscape_dark: desktop, portrait_light: phone, portrait_dark: phone },
      }),
    });
    app.mount(host);
    await settle();

    editButton().click();
    await flush();
    typeIn(source()!, 'desktop only');
    pressEscape();
    await new Promise((resolve) => setTimeout(resolve, 260));
    await flush();

    const phoneScreen = [...host.querySelectorAll<HTMLButtonElement>('.oa-bg-switch button')]
      .find((button) => button.textContent?.includes(t('bgPhone')))!;
    phoneScreen.click();
    await settle();
    editButton().click();
    await flush();

    expect(source()!.value).not.toContain('desktop only');
    expect(source()!.value).toBe('<main>phone page</main>');
    expect(fetchPage).toHaveBeenLastCalledWith(phone.url, { credentials: 'omit' });
  });
});
