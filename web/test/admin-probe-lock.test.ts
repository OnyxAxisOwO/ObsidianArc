// A backoffice visit that lapsed must draw the code prompt wherever the
// refusal came from. The model probe streams and so calls fetch itself, and it
// once refused with the same code as every other request but left the prompt
// undrawn, with no way to unlock from that page.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { adminApi } from '@/admin/api';
import { ApiError, BACKOFFICE_LOCKED } from '@/api/client';
import { SESSION_ENDED } from '@/api/session-ended';
import { t } from '@/composables/useI18n';

function refuse(code: string, status = 403): void {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(
    JSON.stringify({ error: { code, message: 'Verify to continue.' } }),
    { status, headers: { 'Content-Type': 'application/json' } },
  )));
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('model probe backoffice lock', () => {
  it('tells the shell the visit lapsed when the probe is refused for that reason', async () => {
    refuse('two_factor_backoffice_verify');
    const locked = vi.fn();
    window.addEventListener(BACKOFFICE_LOCKED, locked);

    const error = await adminApi.probeAllModels(() => undefined).catch((reason: unknown) => reason);

    window.removeEventListener(BACKOFFICE_LOCKED, locked);
    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('two_factor_backoffice_verify');
    expect(locked).toHaveBeenCalledOnce();
  });

  it('does not raise the prompt for an unrelated refusal', async () => {
    refuse('forbidden');
    const locked = vi.fn();
    window.addEventListener(BACKOFFICE_LOCKED, locked);

    await adminApi.probeAllModels(() => undefined).catch(() => undefined);

    window.removeEventListener(BACKOFFICE_LOCKED, locked);
    expect(locked).not.toHaveBeenCalled();
  });

  // The probe reads its own stream, so a refusal with no live session has to
  // sign the tab out here too, or the open backoffice keeps its account screen.
  it('signs the tab out when the probe is refused because the session ended', async () => {
    refuse('session_required', 401);
    const ended = vi.fn();
    window.addEventListener(SESSION_ENDED, ended);

    await adminApi.probeAllModels(() => undefined).catch(() => undefined);

    window.removeEventListener(SESSION_ENDED, ended);
    expect(ended).toHaveBeenCalledOnce();
  });

  it('does not sign the tab out for a 401 that is not a lapsed session', async () => {
    refuse('two_factor_pending', 401);
    const ended = vi.fn();
    window.addEventListener(SESSION_ENDED, ended);

    await adminApi.probeAllModels(() => undefined).catch(() => undefined);

    window.removeEventListener(SESSION_ENDED, ended);
    expect(ended).not.toHaveBeenCalled();
  });

  // A connection that fails before any response arrives must read like every
  // other request's failure, not like the engine's own TypeError text.
  it('turns a failed connection into the translated network error', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new TypeError('Failed to fetch');
    }));

    const error = await adminApi.probeAllModels(() => undefined).catch((reason: unknown) => reason);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('network');
    expect((error as ApiError).message).toBe(t('connectionFailed'));
    expect((error as ApiError).details).toEqual({ detail: 'Failed to fetch' });
  });
});
