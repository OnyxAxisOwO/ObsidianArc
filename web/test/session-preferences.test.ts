// What the session store does with the account's stored preferences and with
// the per-store resets a sign-out runs.

import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/api/auth', async (original) => ({
  ...(await original<typeof import('@/api/auth')>()),
  savePreferences: vi.fn(),
}));

import type { Account } from '@/api/auth';
import { savePreferences } from '@/api/auth';
import { ApiError } from '@/api/client';
import { adopt, forget, onSignOut, syncPreferences } from '@/stores/session';
import { accentPreference, backgroundAccent, setAccentPreference, setBackgroundAccent } from '@/theme/theme';

const account = {
  id: 'u1', username: 'u1', email: '', nickname: '', avatar: '', bio: '', role: 'user',
  group_id: '', group_expires_at: 0, group_name: '', status: 'active', created_at: 0, updated_at: 0,
  last_login_at: 0, email_verified: true, allow_stats: true, allow_delete_conversations: true,
  api_restricted: false, api_restricted_until: 0, api_restriction_source: '',
} as Account;

afterEach(() => {
  forget();
  vi.restoreAllMocks();
});

describe('the account preferences', () => {
  it('keeps the reader’s accent when the stored one is no preset this build has', () => {
    setAccentPreference({ accent: 'blue', customAccent: '' });
    setBackgroundAccent('teal');

    adopt(account, { accent: 'constructor', background_accent: 'not-a-colour' });

    expect(accentPreference().accent).toBe('blue');
    expect(backgroundAccent()).toBe('teal');
  });

  it('takes a stored preset, and an empty background as the reader choosing none', () => {
    setAccentPreference({ accent: 'blue', customAccent: '' });
    setBackgroundAccent('teal');

    adopt(account, { accent: 'green', background_accent: '' });

    expect(accentPreference().accent).toBe('green');
    expect(backgroundAccent()).toBe('');
  });

  it('says so when the server refuses a preference, and not when the network failed', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    adopt(account, {});

    vi.mocked(savePreferences).mockRejectedValueOnce(new ApiError(0, 'network', 'offline'));
    syncPreferences({ theme: 'dark' });
    await Promise.resolve();
    await Promise.resolve();
    expect(warn).not.toHaveBeenCalledWith('the server refused a preference', expect.anything(), expect.anything());

    vi.mocked(savePreferences).mockRejectedValueOnce(new ApiError(400, 'invalid', 'no such theme'));
    syncPreferences({ theme: 'dark' });
    await Promise.resolve();
    await Promise.resolve();
    expect(warn).toHaveBeenCalledWith('the server refused a preference', { theme: 'dark' }, expect.any(ApiError));
  });
});

describe('forget', () => {
  it('runs every reset registered after one that throws', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    const later = vi.fn();
    onSignOut(() => {
      throw new Error('a store that could not clear');
    });
    onSignOut(later);

    adopt(account, {});
    forget();

    expect(later).toHaveBeenCalledTimes(1);
  });
});
