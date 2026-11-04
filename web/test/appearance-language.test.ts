// Switching language when the dictionary for it cannot be fetched. The page
// must stay in the language it was drawn in, and the account must not be told
// about a choice the reader never saw — or the next visit would open on a
// language that cannot load.

import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { createApp, type App } from 'vue';
import AppearanceSection from '../src/views/settings/AppearanceSection.vue';
import { currentLanguage, t } from '../src/composables/useI18n';
import * as auth from '../src/api/auth';
import { adopt, forget } from '../src/stores/session';
import { settle } from './helpers';

// The Chinese dictionary is a chunk fetched on demand; this is a chunk that
// fails to arrive.
vi.mock('../src/i18n.zh', () => {
  throw new Error('chunk did not arrive');
});

const account: auth.Account = {
  id: 'member', username: 'member', email: '', nickname: '', avatar: '', bio: '', role: 'user',
  group_id: '', group_expires_at: 0, group_name: '', status: 'active', created_at: 0, updated_at: 0,
  last_login_at: 0, email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

let app: App | undefined;
beforeEach(() => {
  vi.spyOn(auth, 'savePreferences').mockResolvedValue({ preferences: {} });
  adopt(account);
});
afterEach(() => {
  app?.unmount(); app = undefined; document.body.textContent = '';
  localStorage.clear(); forget(); vi.restoreAllMocks();
});

it('keeps the page in its language, and does not save a choice whose dictionary did not load', async () => {
  const host = document.createElement('div'); document.body.appendChild(host);
  app = createApp(AppearanceSection); app.mount(host);
  expect(currentLanguage()).toBe('en');

  host.querySelector<HTMLButtonElement>('button[role="combobox"]')!.click();
  await settle();
  const chinese = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((node) => node.textContent?.includes('中文'))!;
  chinese.click();
  await settle();

  expect(currentLanguage()).toBe('en');
  expect(t('languageLoadFailed')).toBe('Could not load that language. The page stays as it was.');
  expect(host.textContent).toContain(t('languageLoadFailed'));
  expect(auth.savePreferences).not.toHaveBeenCalledWith({ language: 'zh' });
  expect(localStorage.getItem('obsidian-arc-language')).not.toBe('zh');
});
