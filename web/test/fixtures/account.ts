import type { Account } from '../../src/api/auth';

// The account the tests share: a signed-in member of a group with the
// everyday permissions on. A test that needs another shape passes only the
// fields that differ, so a field added to Account is written once, here,
// instead of in every file that mocks one.
export function makeAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: 'account-1',
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
    created_at: 1,
    updated_at: 1,
    last_login_at: 0,
    email_verified: true,
    allow_stats: true,
    allow_delete_conversations: true,
    api_restricted: false,
    api_restricted_until: 0,
    api_restriction_source: '',
    ...overrides,
  };
}
