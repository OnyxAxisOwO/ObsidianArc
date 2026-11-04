// A session that ends while a page is open signs the tab out in memory, and the
// route stays where it was: the guard only runs on navigation. The router has
// to take a signed-in tab off the protected page itself, keeping where it was
// as `next` so signing in brings the reader back to it.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../src/api/client';
import { router } from '../src/router';
import { adopt, currentUser, forget } from '../src/stores/session';
import { makeAccount } from './fixtures/account';

const ACCOUNT = makeAccount({
  id: '01ARZ3NDEKTSV4RRFFQ69G5FAV', created_at: Date.now(), updated_at: Date.now(), last_login_at: Date.now(),
  signup_user_agent: 'Mozilla/5.0 ObsidianArcTest/1.0',
});

function refuse(code: string): void {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(
    JSON.stringify({ error: { code, message: 'Sign in to continue.' } }),
    { status: 401, headers: { 'Content-Type': 'application/json' } },
  )));
}

async function go(path: string): Promise<void> {
  await router.replace(path);
  await router.isReady();
}

afterEach(async () => {
  vi.unstubAllGlobals();
  forget();
  await go('/');
});

describe('a session that ends on a protected page', () => {
  it('moves the tab to sign-in, with the page it was on as next', async () => {
    adopt(ACCOUNT);
    await go('/settings?tab=keys');

    refuse('session_required');
    await api.get('/api/chats').catch(() => undefined);

    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/login'));
    expect(currentUser.value).toBeNull();
    expect(router.currentRoute.value.query['next']).toBe('/settings?tab=keys');
  });

  it('leaves an anonymous tab on the page it was already on', async () => {
    forget();
    await go('/register');

    refuse('session_required');
    await api.get('/api/chats').catch(() => undefined);

    expect(router.currentRoute.value.path).toBe('/register');
  });
});
