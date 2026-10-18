// A stream refused before it opened reads its error the way a JSON request
// does, through the one reader in api/client.ts, and raises the same notices.
// The chat and the terminal both read their own streams and never reach the
// JSON client, so a refusal they missed left the page without its prompt.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { BACKOFFICE_LOCKED } from '@/api/client';
import { SESSION_ENDED } from '@/api/session-ended';
import { streamRefusal } from '@/api/sse';
import { streamTrial } from '@/api/trial';
import { t } from '@/composables/useI18n';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function refusal(code: string, status = 403): Response {
  return new Response(JSON.stringify({ error: { code, message: 'No.', field: 'x' } }), { status });
}

describe('a refused stream', () => {
  it('raises the backoffice prompt when the visit lapsed', async () => {
    const locked = vi.fn();
    window.addEventListener(BACKOFFICE_LOCKED, locked);
    const error = await streamRefusal(refusal('two_factor_backoffice_verify'));
    window.removeEventListener(BACKOFFICE_LOCKED, locked);

    expect(locked).toHaveBeenCalledOnce();
    expect(error.code).toBe('two_factor_backoffice_verify');
    expect(error.details).toEqual({ field: 'x' });
  });

  it('signs the tab out when the session ended, and raises nothing else', async () => {
    const locked = vi.fn();
    const ended = vi.fn();
    window.addEventListener(BACKOFFICE_LOCKED, locked);
    window.addEventListener(SESSION_ENDED, ended);
    await streamRefusal(refusal('session_required', 401));
    window.removeEventListener(BACKOFFICE_LOCKED, locked);
    window.removeEventListener(SESSION_ENDED, ended);

    expect(ended).toHaveBeenCalledOnce();
    expect(locked).not.toHaveBeenCalled();
  });

  it("keeps the defaults for a proxy's error page", async () => {
    const error = await streamRefusal(new Response('<html>Bad gateway</html>', { status: 502 }));

    expect(error.status).toBe(502);
    expect(error.code).toBe('error');
    expect(error.message).toBe(t('failed'));
    expect(error.details).toEqual({});
  });

  it("gives the trial its own sentence for a proxy's error page", async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('<html>Bad gateway</html>', { status: 502 })));

    const error = await streamTrial([], '', () => undefined).catch((reason: unknown) => reason);

    expect(error).toMatchObject({ status: 502, code: 'trial', message: t('trialFailed') });
  });
});
