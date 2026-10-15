// Every error the client builds carries a sentence a reader can read; the browser's own text is kept in details for debugging.
// The network failure and the empty-body fallback used to be English sentences, and the
// browser's own engine text went straight onto the screen.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { changeLanguage, t } from '@/composables/useI18n';
import { ApiError, api } from '@/api/client';
import { sendTurn } from '@/api/chat';
import { execConsoleLine } from '@/api/console';
import { streamTrial } from '@/api/trial';

afterEach(async () => {
  vi.unstubAllGlobals();
  await changeLanguage('en');
});

// The streamed clients read their own body, so they never reach `request`; a
// connection that fails before the response must still come out as the same
// network error a plain request gets.
async function failedConnection(run: () => Promise<unknown>): Promise<ApiError> {
  vi.stubGlobal('fetch', vi.fn(async () => {
    throw new TypeError('Failed to fetch');
  }));
  const error = await run().catch((reason: unknown) => reason);

  expect(error).toBeInstanceOf(ApiError);
  expect((error as ApiError).code).toBe('network');
  expect((error as ApiError).message).toBe(t('connectionFailed'));
  expect((error as ApiError).details).toEqual({ detail: 'Failed to fetch' });
  return error as ApiError;
}

describe('api client error text', () => {
  it('shows a translated line for a network failure and keeps the browser text in details', async () => {
    await changeLanguage('zh');
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new TypeError('Failed to fetch');
    }));

    const error = await api.get('/api/health').catch((reason: unknown) => reason);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).code).toBe('network');
    expect((error as ApiError).message).toBe(t('connectionFailed'));
    expect((error as ApiError).message).not.toContain('Failed to fetch');
    expect((error as ApiError).details).toEqual({ detail: 'Failed to fetch' });
  });

  it('falls back to a translated generic line when an error has no body', async () => {
    await changeLanguage('zh');
    vi.stubGlobal('fetch', vi.fn(async () => new Response('<html>bad gateway</html>', { status: 502 })));

    const error = await api.get('/api/health').catch((reason: unknown) => reason);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(502);
    expect((error as ApiError).message).toBe(t('failed'));
    expect((error as ApiError).message).not.toContain('Request failed with HTTP');
  });

  it('maps a failed connection on the streamed turn to the network error', async () => {
    await changeLanguage('zh');

    await failedConnection(() => sendTurn({ model_id: 'm' }, {}, new AbortController().signal));
  });

  it('maps a failed connection on the streamed trial to the network error', async () => {
    await changeLanguage('zh');

    await failedConnection(() => streamTrial([{ role: 'user', content: 'hi' }], 'c1', () => undefined));
  });

  it('maps a failed connection on the console exec stream to the network error', async () => {
    await changeLanguage('zh');

    await failedConnection(() =>
      execConsoleLine({ line: 'help', cols: 80, json: false }, {}, new AbortController().signal),
    );
  });

  it('lets an aborted turn reject with the abort itself, not a network error', async () => {
    const controller = new AbortController();
    vi.stubGlobal('fetch', vi.fn(async () => {
      controller.abort();
      throw new DOMException('The operation was aborted.', 'AbortError');
    }));

    const error = await sendTurn({ model_id: 'm' }, {}, controller.signal).catch((reason: unknown) => reason);

    expect(error).not.toBeInstanceOf(ApiError);
    expect((error as Error).name).toBe('AbortError');
  });
});
