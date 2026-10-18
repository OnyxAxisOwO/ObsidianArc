// The account's language is applied when it signs in, as its theme already
// is. The preference was written to the account and then never read back, so
// a reader who chose Chinese on one computer opened English on the next.

import { afterEach, expect, it, vi } from 'vitest';
import { changeLanguage, currentLanguage, t } from '../src/composables/useI18n';
import { adopt, forget } from '../src/stores/session';
import { makeAccount } from './fixtures/account';

const account = makeAccount({
  id: 'member', username: 'member', email: '', nickname: '', group_id: '', group_name: '', created_at: 0,
  updated_at: 0,
});

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
