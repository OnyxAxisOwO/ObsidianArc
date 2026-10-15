// Putting the standing notice away is remembered against its wording. The
// wording is two fields, and the line may hold line breaks, so the way the two
// are joined must not let one notice's dismissal hide a different notice.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { createApp, h, nextTick, type App } from 'vue';
import HomeNotice from '@/announce/HomeNotice.vue';
import { changeLanguage } from '@/composables/useI18n';
import { site, siteInfo } from '@/stores/session';

const DISMISSED_KEY = 'obsidian-arc-home-notice-dismissed';

// The digest HomeNotice used before the wording was hashed as JSON. Copied
// here rather than exported from the component: it exists only so a dismissal
// saved by an earlier build can be recognised.
function legacyDigest(value: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < value.length; i++) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(36);
}

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

let app: App | null = null;
let host: HTMLElement;

async function mount(): Promise<void> {
  app = createApp({ render: () => h(HomeNotice) });
  app.mount(host);
  await settle();
}

const strip = (): HTMLElement | null => host.querySelector<HTMLElement>('.oa-home-notice');

beforeEach(async () => {
  await changeLanguage('en');
  localStorage.clear();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  site.value = null;
  app?.unmount();
  app = null;
  document.body.textContent = '';
});

describe('HomeNotice dismissal', () => {
  it('comes back when the wording changes to a different line and body with the same joined text', async () => {
    say({ text: 'a\nb', body: 'c' });
    await mount();
    host.querySelector<HTMLElement>('.oa-home-notice-close')!.click();
    await settle();
    expect(strip()).toBeNull();

    say({ text: 'a', body: 'b\nc' });
    await settle();

    expect(strip()).not.toBeNull();
  });

  it('stays hidden for the same wording it was dismissed under', async () => {
    say({ text: 'Maintenance on Sunday', body: 'from 02:00' });
    await mount();
    host.querySelector<HTMLElement>('.oa-home-notice-close')!.click();
    await settle();
    app?.unmount();
    app = null;

    host = document.createElement('div');
    document.body.appendChild(host);
    await mount();

    expect(strip()).toBeNull();
  });

  it('still honours a dismissal saved under the earlier digest', async () => {
    localStorage.setItem(DISMISSED_KEY, legacyDigest('Maintenance on Sunday\nfrom 02:00'));
    say({ text: 'Maintenance on Sunday', body: 'from 02:00' });

    await mount();

    expect(strip()).toBeNull();
  });
});
