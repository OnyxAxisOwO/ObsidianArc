// Two things in the header that only a pointer used to find, or that only
// looked right on one fill. The account address is a line in the menu's head,
// so a keyboard, a touch screen and a screen reader reach it too. The unread
// dots on the account chip and the bell are set apart by a hole cut out of
// what they sit on rather than by a ring painted in one fill, so the gap
// shows the hover fill and the frosted wallpaper chip as well as the rest one.

import fs from 'node:fs';
import path from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createApp, h, nextTick, ref, type App } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import type { Account } from '../src/api/auth';
import AnnounceBell from '../src/announce/AnnounceBell.vue';
import AccountMenu from '../src/layouts/AccountMenu.vue';
import { changeLanguage } from '../src/composables/useI18n';
import { feedbackUnread } from '../src/stores/feedback';
import { forget } from '../src/stores/session';
import { makeAccount } from './fixtures/account';

vi.mock('../src/stores/feedback', () => ({
  feedbackUnread: ref(0),
  refreshFeedbackUnread: vi.fn(async () => {}),
  forgetFeedbackUnread: vi.fn(),
}));

// Hoisted with the mock that reads it, which runs before this file's imports.
const feed = vi.hoisted(() => ({ unread: 0 }));
vi.mock('../src/api/announcements', () => ({
  fetchAnnouncements: vi.fn(async () => ({ announcements: [], unread: feed.unread, popup: null })),
  fetchAnnouncement: vi.fn(),
  markRead: vi.fn(async () => undefined),
  markAllRead: vi.fn(async () => undefined),
}));

let app: App | undefined;

function mount(render: () => ReturnType<typeof h>): HTMLElement {
  const host = document.createElement('div');
  document.body.appendChild(host);
  app = createApp({ render });
  app.use(createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] }));
  app.mount(host);
  return host;
}

async function openMenu(account: Account): Promise<HTMLElement> {
  const host = mount(() => h(AccountMenu, { account }));
  await nextTick();
  host.querySelector<HTMLButtonElement>('.oa-account-btn')!.click();
  await vi.waitFor(() => expect(host.querySelector('.oa-acct-head')).not.toBeNull());
  await nextTick();
  return document.querySelector<HTMLElement>('.oa-menu-account')!;
}

beforeEach(async () => { await changeLanguage('en'); });
afterEach(() => {
  app?.unmount();
  app = undefined;
  document.body.textContent = '';
  feedbackUnread.value = 0;
  feed.unread = 0;
  forget();
});

describe('the account address in the menu head', () => {
  it('is a visible line of text under the handle, not only a hover title', async () => {
    const menu = await openMenu(makeAccount({ email: 'ada.lovelace@example.com' }));
    const id = menu.querySelector('.oa-acct-id')!;

    expect([...id.children].map((node) => node.className))
      .toEqual(['oa-acct-name', 'oa-acct-handle', 'oa-acct-email']);
    expect(id.querySelector('.oa-acct-email')?.textContent).toBe('ada.lovelace@example.com');
    // Read as text by anything walking the menu, not stashed on the head.
    expect(menu.querySelector('.oa-acct-head')?.hasAttribute('title')).toBe(false);
  });

  it('leaves no empty line for an account without one', async () => {
    const menu = await openMenu(makeAccount({ email: '' }));

    expect(menu.querySelector('.oa-acct-email')).toBeNull();
  });
});

describe('the unread dots', () => {
  it('marks the account chip it cuts into only while an answer is waiting', async () => {
    const host = mount(() => h(AccountMenu, { account: makeAccount() }));
    await nextTick();
    const chip = host.querySelector('.oa-account-btn')!;
    expect(chip.classList.contains('has-unread')).toBe(false);
    expect(chip.querySelector('.oa-account-dot')).toBeNull();

    feedbackUnread.value = 1;
    await nextTick();
    expect(chip.classList.contains('has-unread')).toBe(true);
    expect(chip.querySelector('.oa-account-dot')).not.toBeNull();
  });

  it('marks the bell it cuts into only while something is unread', async () => {
    let host = mount(() => h(AnnounceBell));
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
    expect(host.querySelector('.oa-bell')?.classList.contains('has-unread')).toBe(false);
    expect(host.querySelector('.oa-bell-dot')).toBeNull();

    app?.unmount();
    document.body.textContent = '';
    feed.unread = 2;
    host = mount(() => h(AnnounceBell));
    await new Promise((resolve) => setTimeout(resolve, 0));
    await nextTick();
    expect(host.querySelector('.oa-bell')?.classList.contains('has-unread')).toBe(true);
    expect(host.querySelector('.oa-bell-dot')).not.toBeNull();
  });
});

// jsdom does not lay out or composite, so what the gap shows is held in the
// stylesheet source.
describe('the stylesheet behind them', () => {
  const app = ['_app.scss', 'account-menu.scss'].map((name) => fs.readFileSync(path.resolve(__dirname, '../src/styles', name), 'utf8')).join('\n')
    .replace(/\/\*[\s\S]*?\*\//g, '');
  // Every rule in the file as [selector, body], nested ones included: the
  // innermost braces are always a selector and its declarations. A Sass
  // variable set before a rule ends in a semicolon, so it is cut off the
  // selector there.
  const rules = [...app.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .map((m) => [(m[1] ?? '').split(';').pop()!.trim(), m[2] ?? ''] as const);
  const rule = (selector: string): string => {
    const found = rules.find(([name]) => name === selector);
    expect(found, selector).toBeDefined();
    return found![1];
  };

  it('paints no ring around either dot, since no single fill is right beneath it', () => {
    const dots = rules.filter(([name]) => /\.oa-(account|bell)-dot\b/.test(name));
    expect(dots.length).toBeGreaterThanOrEqual(3);
    for (const [, body] of dots) expect(body).not.toMatch(/box-shadow/);
  });

  it('cuts the gap out of the avatar and the glyph instead', () => {
    expect(rule('.oa-account-btn.has-unread .oa-avatar')).toMatch(/@include unread-cutout\(/);
    expect(rule('.oa-bell.has-unread > svg')).toMatch(/@include unread-cutout\(/);
    const mixin = rule('@mixin unread-cutout($x, $y)');
    expect(mixin).toMatch(/(^|\s)mask-image: radial-gradient\(/);
    expect(mixin).toMatch(/-webkit-mask-image: radial-gradient\(/);
  });

  it('lets the address fill the column without setting the menu width', () => {
    expect(rule('.oa-acct-email')).toMatch(/contain: inline-size/);
    expect(rule('.oa-acct-handle,\n.oa-acct-email')).toMatch(/@include ellipsis/);
  });
});
