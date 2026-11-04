// Choosing Chinese while its dictionary is still loading, and a later pick
// taking over before it arrives. The late Chinese pick must not be saved to
// the account over the choice the reader made after it, or the next visit
// would open in Chinese although the page was left in English.

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, type App } from 'vue';
import AppearanceSection from '../src/views/settings/AppearanceSection.vue';
import * as i18n from '../src/composables/useI18n';
import * as auth from '../src/api/auth';
import { adopt, forget } from '../src/stores/session';
import { settle } from './helpers';

// The Chinese chunk stays on its way until the test releases it.
const chunk = vi.hoisted(() => {
  let release!: () => void;
  const arrives = new Promise<void>((resolve) => { release = resolve; });
  return { arrives, release: () => release() };
});

vi.mock('../src/i18n.zh', async () => {
  await chunk.arrives;
  return vi.importActual<typeof import('../src/i18n.zh')>('../src/i18n.zh');
});

const account: auth.Account = {
  id: 'member', username: 'member', email: '', nickname: '', avatar: '', bio: '', role: 'user',
  group_id: '', group_expires_at: 0, group_name: '', status: 'active', created_at: 0, updated_at: 0,
  last_login_at: 0, email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

let app: App | undefined;
beforeEach(() => {
  localStorage.clear();
  vi.spyOn(auth, 'savePreferences').mockResolvedValue({ preferences: {} });
  adopt(account);
});
afterEach(() => {
  app?.unmount(); app = undefined; document.body.textContent = '';
  localStorage.clear(); forget(); vi.restoreAllMocks();
});

it('does not save Chinese when a later pick took over while its dictionary was loading', async () => {
  // Every pick is recorded so the test can wait for the Chinese one itself,
  // rather than for a fixed number of ticks that a slow chunk could outlast.
  const picks: Array<Promise<void>> = [];
  const changeLanguage = i18n.changeLanguage;
  vi.spyOn(i18n, 'changeLanguage').mockImplementation((next) => {
    const pick = changeLanguage(next);
    picks.push(pick);
    return pick;
  });

  const host = document.createElement('div'); document.body.appendChild(host);
  app = createApp(AppearanceSection); app.mount(host);
  expect(i18n.currentLanguage()).toBe('en');

  host.querySelector<HTMLButtonElement>('button[role="combobox"]')!.click();
  await settle();
  [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((node) => node.textContent?.includes('中文'))!
    .click();
  await settle();
  expect(i18n.currentLanguage()).toBe('en');
  const chinesePick = picks[0]!;

  // The select still shows English, so choosing English from it would emit
  // nothing. A later pick arrives the way an account preference applies one.
  await i18n.changeLanguage('en');
  chunk.release();
  await chinesePick;

  expect(i18n.currentLanguage()).toBe('en');
  expect(localStorage.getItem('obsidian-arc-language')).toBe('en');
  expect(auth.savePreferences).not.toHaveBeenCalledWith({ language: 'zh' });
});
