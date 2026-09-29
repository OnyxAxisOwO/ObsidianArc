import { afterEach, describe, expect, it } from 'vitest';
import type { Account } from '../src/api/auth';
import { router } from '../src/router';
import { adopt, forget } from '../src/stores/session';

// A partner or friend's invite link, opened by somebody who already has an
// account. There is no sign-up left for it to fill in, but the code is still
// worth redeeming — the router sends this visitor to the claim box instead
// of letting the ordinary "already signed in" bounce drop it on the floor.

const ACCOUNT: Account = {
  id: '01ARZ3NDEKTSV4RRFFQ69G5FAV',
  username: 'ada',
  email: 'ada@example.com',
  nickname: 'Ada',
  avatar: '',
  bio: '',
  role: 'user',
  group_id: 'g1',
  group_expires_at: 0,
  group_name: 'Default',
  status: 'active',
  created_at: Date.now(),
  updated_at: Date.now(),
  last_login_at: Date.now(),
  email_verified: true,
  allow_stats: true,
  allow_delete_conversations: true,
  api_restricted: false,
  api_restricted_until: 0,
  api_restriction_source: '',
  signup_user_agent: 'Mozilla/5.0 ObsidianArcTest/1.0',
};

async function go(path: string): Promise<void> {
  await router.replace(path);
  await router.isReady();
}

afterEach(async () => {
  forget();
  await go('/');
});

describe('a signed-in visitor opening an invite link', () => {
  it('is sent to the claim box, code and all, rather than the chat', async () => {
    adopt(ACCOUNT);
    await go('/register?invite=PARTNER1');

    expect(router.currentRoute.value.path).toBe('/settings');
    expect(router.currentRoute.value.query).toEqual({ tab: 'invites', claim: 'PARTNER1' });
  });

  it('still bounces a plain /register to the chat, invite or not', async () => {
    adopt(ACCOUNT);
    await go('/register');

    expect(router.currentRoute.value.path).toBe('/');
  });

  it('leaves a signed-out visitor on the sign-up form to use the code there', async () => {
    forget();
    await go('/register?invite=PARTNER1');

    expect(router.currentRoute.value.path).toBe('/register');
    expect(router.currentRoute.value.query['invite']).toBe('PARTNER1');
  });
});
