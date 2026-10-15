// Checks what execConsoleLine puts in the request body: request.lang when the
// caller set one, the UI language when it did not. Carrying a `lang` command's
// language from one command to the next is the tab's job, not this module's,
// and that is tested in terminal-session.test.ts.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { execConsoleLine, type ConsoleExecRequest } from '@/api/console';
import { changeLanguage } from '@/composables/useI18n';

function doneStream(lang: string): Response {
  const body = `event: done\ndata: {"ok":true,"code":"","exit":false,"elapsed_ms":1,"json":false,"lang":"${lang}"}\n\n`;
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
}

async function sentLang(request: ConsoleExecRequest): Promise<unknown> {
  const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => doneStream('zh'));
  vi.stubGlobal('fetch', fetchMock);
  await execConsoleLine(request, {}, new AbortController().signal);
  const init = fetchMock.mock.calls[0]?.[1];
  return JSON.parse(String(init?.body)).lang;
}

afterEach(async () => {
  vi.unstubAllGlobals();
  await changeLanguage('en');
});

describe('console exec language', () => {
  it('sends an explicit language on the request', async () => {
    await changeLanguage('en');

    await expect(sentLang({ line: 'help', cols: 80, json: false, lang: 'zh' })).resolves.toBe('zh');
  });

  it('falls back to the UI language before any lang command has run', async () => {
    await changeLanguage('zh');

    await expect(sentLang({ line: 'help', cols: 80, json: false })).resolves.toBe('zh');
    await changeLanguage('en');
    await expect(sentLang({ line: 'help', cols: 80, json: false })).resolves.toBe('en');
  });
});
