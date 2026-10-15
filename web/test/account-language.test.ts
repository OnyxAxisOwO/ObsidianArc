// The account's language is applied when it signs in, as its theme already
// is. The preference was written to the account and then never read back, so
// a reader who chose Chinese on one computer opened English on the next.

import { afterEach, expect, it, vi } from 'vitest';
import { changeLanguage, currentLanguage, t } from '../src/composables/useI18n';
import type { Account } from '../src/api/auth';
import { adopt, forget } from '../src/stores/session';

const account: Account = {
  id: 'member', username: 'member', email: '', nickname: '', avatar: '', bio: '', role: 'user',
  group_id: '', group_expires_at: 0, group_name: '', status: 'active', created_at: 0, updated_at: 0,
  last_login_at: 0, email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
};

afterEach(async () => {
  forget();
  localStorage.clear();
  await changeLanguage('en');
  vi.restoreAllMocks();
});

it('switches to the language the account chose when it signs in', async () => {
  expect(currentLanguage()).toBe('en');
  adopt(account, { language: 'zh' });
  // The switch waits for the Chinese dictionary to arrive, so it lands after
  // this call returns rather than during it.
  await vi.waitFor(() => expect(currentLanguage()).toBe('zh'));
  expect(t('secAppearance')).toMatch(/[一-鿿]/);
  expect(localStorage.getItem('obsidian-arc-language')).toBe('zh');
});

it('leaves the language alone when the account has not chosen one', async () => {
  // Start from Chinese: afterEach leaves the page in English, and an English
  // start would pass for an implementation that switched to English anyway.
  await changeLanguage('zh');
  adopt(account, {});
  // A switch would be asynchronous, so give it time to land before reading.
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(currentLanguage()).toBe('zh');
});
