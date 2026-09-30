// The standing notice: a thin strip, and what opens from it. What is held here
// is that the strip is a button only when there is something behind it, that
// what opens is rendered as Markdown and never as markup, that a warning is a
// tone and not a colour, and that putting it away is remembered against the
// wording — an operator who changes the notice is saying something new.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import HomeNotice from '@/announce/HomeNotice.vue';
import { changeLanguage, t } from '@/composables/useI18n';
import { site, siteInfo } from '@/stores/session';

let app: App | null = null;
let host: HTMLElement;

type Notice = NonNullable<typeof siteInfo.value.home_notice>;

function say(notice: Partial<Notice>): void {
  site.value = {
    ...siteInfo.value,
    home_notice: { text: '', dismissible: true, body: '', tone: 'info', ...notice },
  };
}

async function settle(): Promise<void> {
  await nextTick();
  await nextTick();
}

async function mount(props: Record<string, unknown> = {}): Promise<void> {
  app = createApp({ render: () => h(HomeNotice, props) });
  app.mount(host);
  await settle();
}

const strip = (): HTMLElement | null => host.querySelector<HTMLElement>('.oa-home-notice');

beforeEach(async () => {
  await changeLanguage('en');
  localStorage.clear();
  host = document.createElement('div');
  document.body.appendChild(host);
  say({});
});

afterEach(() => {
  site.value = null;
  app?.unmount();
  app = null;
  document.body.textContent = '';
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('HomeNotice', () => {
  it('draws nothing when there is no notice', async () => {
    await mount();
    expect(strip()).toBeNull();
  });

  it('is only the line when nothing is written behind it: not a button, and shown whole', async () => {
    say({ text: 'Maintenance on Sunday\nfrom 02:00' });
    await mount();
    expect(strip()!.textContent).toContain('Maintenance on Sunday');
    expect(strip()!.classList.contains('has-body')).toBe(false);
    expect(host.querySelector('button.oa-home-notice-main')).toBeNull();
  });

  it('is a button as a whole when there is a text behind it, and opens that text over the page', async () => {
    say({ text: 'Maintenance on Sunday', body: '## When\n\n02:00–03:00 UTC.\n\n- it is **short**' });
    await mount();
    const button = host.querySelector<HTMLButtonElement>('button.oa-home-notice-main')!;
    expect(strip()!.classList.contains('has-body')).toBe(true);
    expect(button.getAttribute('aria-haspopup')).toBe('dialog');
    expect(button.textContent).toContain('Maintenance on Sunday');

    button.click();
    await settle();

    const sheet = document.querySelector('.oa-announce')!;
    expect(sheet.querySelector('.oa-announce-title')!.textContent).toBe('Maintenance on Sunday');
    expect(sheet.querySelector('.oa-announce-body')!.textContent).toContain('When');
    // Rendered, not shown as the source: the marks are gone.
    expect(sheet.querySelector('.oa-announce-body')!.textContent).not.toContain('##');
    expect(sheet.querySelector('strong')?.textContent).toBe('short');
  });

  it('draws the text as Markdown nodes and never as markup', async () => {
    say({ text: 'Notice', body: '<img src=x onerror=alert(1)> and <script>alert(2)</script>' });
    await mount();
    host.querySelector<HTMLButtonElement>('button.oa-home-notice-main')!.click();
    await settle();

    const sheet = document.querySelector('.oa-announce')!;
    expect(sheet.querySelector('img')).toBeNull();
    expect(sheet.querySelector('script')).toBeNull();
    expect(sheet.textContent).toContain('onerror=alert(1)');
  });

  it('closes the sheet with its button and leaves the strip where it was', async () => {
    vi.useFakeTimers();
    say({ text: 'Notice', body: 'Body' });
    await mount();
    host.querySelector<HTMLButtonElement>('button.oa-home-notice-main')!.click();
    await settle();
    const close = [...document.querySelectorAll<HTMLButtonElement>('.oa-announce-foot button')].find((b) => b.textContent === t('close'))!;
    close.click();
    await vi.advanceTimersByTimeAsync(300);
    await settle();

    expect(document.querySelector('.oa-announce')).toBeNull();
    expect(strip()).not.toBeNull();
  });

  it('is drawn in the warning tone for a warning, and as information for anything else', async () => {
    say({ text: 'Notice', tone: 'warning' });
    await mount();
    expect(strip()!.classList.contains('tone-warning')).toBe(true);
    app?.unmount();
    app = null;
    host.textContent = '';

    // A server that predates tones, or a row it would never have accepted.
    say({ text: 'Notice', tone: 'rainbow' as unknown as 'info' });
    await mount();
    expect(strip()!.classList.contains('tone-info')).toBe(true);
    expect(strip()!.classList.contains('tone-warning')).toBe(false);
  });

  it('can be fixed to the top of the window, for a page that is one centred card', async () => {
    say({ text: 'Notice' });
    await mount({ floating: true });
    expect(strip()!.classList.contains('floating')).toBe(true);
  });

  it('is put away with its cross, and stays away for the same wording', async () => {
    say({ text: 'Notice', body: 'Body' });
    await mount();
    host.querySelector<HTMLButtonElement>('.oa-home-notice-close')!.click();
    await settle();
    expect(strip()).toBeNull();

    app?.unmount();
    app = null;
    host.textContent = '';
    await mount();
    expect(strip()).toBeNull();
  });

  it('comes back for somebody who put it away when the line or the text behind it changes', async () => {
    say({ text: 'Notice', body: 'Body' });
    await mount();
    host.querySelector<HTMLButtonElement>('.oa-home-notice-close')!.click();
    await settle();
    expect(strip()).toBeNull();

    say({ text: 'Notice', body: 'Body, changed' });
    await settle();
    expect(strip()).not.toBeNull();
  });

  it('has no cross when the operator said it may not be put away, whatever was stored', async () => {
    say({ text: 'Notice', dismissible: false });
    await mount();
    expect(host.querySelector('.oa-home-notice-close')).toBeNull();
  });

  it('stays up when storage refuses to remember', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('denied'); });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('denied'); });
    say({ text: 'Notice' });
    await mount();
    expect(strip()).not.toBeNull();
    host.querySelector<HTMLButtonElement>('.oa-home-notice-close')!.click();
    await settle();
    expect(strip()).toBeNull();
  });
});
