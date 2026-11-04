// A session that ends while a page is open must sign the tab out. The server
// refuses the next request with session_required, and only that code does it: a
// wrong password or a second factor still due keeps the tab where it is.

import { afterEach, describe, expect, it, vi } from 'vitest';
import type { Account } from '@/api/auth';
import { sendTurn } from '@/api/chat';
import { api } from '@/api/client';
import { execConsoleLine } from '@/api/console';
import { SESSION_ENDED, SESSION_LOST } from '@/api/session-ended';
import { adopt, currentUser, forget } from '@/stores/session';

function refuse(code: string): void {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(
    JSON.stringify({ error: { code, message: 'Sign in to continue.' } }),
    { status: 401, headers: { 'Content-Type': 'application/json' } },
  )));
}

function signIn(): void {
  adopt({ id: 'user-1', username: 'ada', role: 'user' } as unknown as Account);
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('expired session', () => {
  it('signs the tab out when a request finds its session gone', async () => {
    signIn();
    expect(currentUser.value).not.toBeNull();
    refuse('session_required');
    const ended = vi.fn();
    window.addEventListener(SESSION_ENDED, ended);

    await api.get('/api/chats').catch(() => undefined);

    window.removeEventListener(SESSION_ENDED, ended);
    expect(ended).toHaveBeenCalledOnce();
    expect(currentUser.value).toBeNull();
  });

  // The router listens for this to leave a protected page: a tab that was
  // signed in must say so, or the page keeps drawing with no account behind it.
  it('announces the sign-out so the router can leave a protected page', async () => {
    signIn();
    refuse('session_required');
    const lost = vi.fn();
    window.addEventListener(SESSION_LOST, lost);

    await api.get('/api/chats').catch(() => undefined);

    window.removeEventListener(SESSION_LOST, lost);
    expect(lost).toHaveBeenCalledOnce();
  });

  // An anonymous visitor on the chat front door was never signed in, so there
  // is no page to leave and nothing to announce.
  it('does not announce a sign-out to a tab that was already anonymous', async () => {
    forget();
    refuse('session_required');
    const lost = vi.fn();
    window.addEventListener(SESSION_LOST, lost);

    await api.get('/api/chats').catch(() => undefined);

    window.removeEventListener(SESSION_LOST, lost);
    expect(lost).not.toHaveBeenCalled();
  });

  it('keeps the tab signed in for a refusal that is not about the session', async () => {
    signIn();
    refuse('two_factor_pending');

    const error = await api.get('/api/chats').catch((reason: unknown) => reason);

    expect((error as { code?: string }).code).toBe('two_factor_pending');
    expect(currentUser.value).not.toBeNull();
  });

  // The streams never reach request(), so they carry their own check. A chat or
  // console left open must sign out just as a JSON call does.
  it('signs the tab out when a chat stream finds its session gone', async () => {
    signIn();
    refuse('session_required');
    const ended = vi.fn();
    window.addEventListener(SESSION_ENDED, ended);

    await sendTurn({ model_id: 'm' }, {}, new AbortController().signal).catch(() => undefined);

    window.removeEventListener(SESSION_ENDED, ended);
    expect(ended).toHaveBeenCalledOnce();
    expect(currentUser.value).toBeNull();
  });

  it('signs the tab out when a console line finds its session gone', async () => {
    signIn();
    refuse('session_required');
    const ended = vi.fn();
    window.addEventListener(SESSION_ENDED, ended);

    await execConsoleLine({ line: 'help', cols: 80, json: false }, {}, new AbortController().signal)
      .catch(() => undefined);

    window.removeEventListener(SESSION_ENDED, ended);
    expect(ended).toHaveBeenCalledOnce();
    expect(currentUser.value).toBeNull();
  });

  it('keeps the tab signed in when a chat stream is refused for another reason', async () => {
    signIn();
    refuse('two_factor_pending');

    await sendTurn({ model_id: 'm' }, {}, new AbortController().signal).catch(() => undefined);

    expect(currentUser.value).not.toBeNull();
  });
});
