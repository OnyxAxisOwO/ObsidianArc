// Boot awaits startSession before it mounts, and startSession waits on the
// account's language. A dictionary request that is accepted and never answers
// must not hold the mount: the wait is bounded, and the page draws in the
// browser's language until the dictionary lands.

import { afterEach, expect, it, vi } from 'vitest';
import * as authApi from '../src/api/auth';
import * as i18n from '../src/composables/useI18n';
import { siteInfo, startSession } from '../src/stores/session';

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

it('startSession returns even while the account language never arrives', async () => {
  vi.useFakeTimers();
  const account = {
    id: '01ARZ3NDEKTSV4RRFFQ69G5FAV',
    username: 'ada',
    email: '',
    nickname: 'Ada',
    avatar: '',
    bio: '',
    role: 'user',
    group_id: 'g1',
    group_expires_at: 0,
    group_name: 'Default',
    status: 'active',
    created_at: 0,
    updated_at: 0,
    last_login_at: 0,
    email_verified: true,
    allow_stats: true,
    allow_delete_conversations: true,
    api_restricted: false,
    api_restricted_until: 0,
    api_restriction_source: '',
  } as unknown as authApi.Account;
  vi.spyOn(authApi, 'fetchMe').mockResolvedValue({ user: account, preferences: { language: 'zh' } });
  vi.spyOn(authApi, 'fetchSite').mockResolvedValue(siteInfo.value);
  vi.spyOn(i18n, 'currentLanguage').mockReturnValue('en');
  // A dictionary request that is accepted and never answers.
  vi.spyOn(i18n, 'changeLanguage').mockReturnValue(new Promise<void>(() => {}));

  let returned = false;
  const boot = startSession().then(() => {
    returned = true;
  });

  // Exactly the startSession bound: the dictionary's own 10 s wait would not
  // have ended yet, so this passes only if startSession stops waiting first.
  await vi.advanceTimersByTimeAsync(3_000);
  expect(returned).toBe(true);
  await boot;
});
