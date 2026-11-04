// What one terminal tab carries from one command to the next, and what its
// prompt does with the server's answers. The api module is replaced at its
// edge, so these tests see exactly the request the tab builds and the frames
// it is handed back.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ConsoleExecHandlers, ConsoleExecRequest } from '../src/api/console';
import { changeLanguage, currentLanguage, t } from '../src/composables/useI18n';
import { createTerminalSession } from '../src/terminal/session';

vi.mock('../src/api/console', () => ({
  fetchConsoleSpec: vi.fn(),
  completeConsoleLine: vi.fn(),
  execConsoleLine: vi.fn(),
}));

import { completeConsoleLine, execConsoleLine } from '../src/api/console';

// The server builds a fresh session per request from the request's language,
// `lang <en|zh>` changes it, and the done frame echoes whatever it ended on
// (internal/console/handlers.go). A `lang` that fails changes nothing.
function serverAnswers(request: ConsoleExecRequest, handlers: ConsoleExecHandlers): void {
  const sent = request.lang ?? currentLanguage();
  const words = request.line.trim().split(/\s+/);
  let lang = sent === 'zh' ? 'zh' : 'en';
  let ok = true;
  if (words[0] === 'lang') {
    if (words[1] === 'en' || words[1] === 'zh') lang = words[1];
    else ok = false;
  }
  handlers.onDone?.({ ok, code: ok ? '' : 'parse_error', exit: false, elapsed_ms: 1, json: false, lang });
}

/** The language each request carried, in order; undefined means the request sent none and the UI language applied. */
function sentLanguages(): (string | undefined)[] {
  return vi.mocked(execConsoleLine).mock.calls.map(([request]) => request.lang);
}

beforeEach(() => {
  vi.mocked(execConsoleLine).mockReset();
  vi.mocked(execConsoleLine).mockImplementation(async (request, handlers) => {
    serverAnswers(request, handlers);
  });
});

afterEach(async () => {
  await changeLanguage('en');
});

describe('the language a tab carries', () => {
  it('keeps a lang command in force for later commands, after the UI language changes', async () => {
    await changeLanguage('zh');
    const session = createTerminalSession({ id: 'lang-carry', scrollbackCap: 100, getColumns: () => 100 });
    await session.run('lang zh');

    await changeLanguage('en');
    await session.run('help');

    expect(sentLanguages()).toEqual([undefined, 'zh']);
  });

  it('follows the UI language when no lang command has run', async () => {
    const session = createTerminalSession({ id: 'lang-ui', scrollbackCap: 100, getColumns: () => 100 });
    await changeLanguage('zh');
    await session.run('help');

    await changeLanguage('en');
    await session.run('help');

    expect(sentLanguages()).toEqual([undefined, undefined]);
  });

  it('does not adopt a language from a lang command the server refused', async () => {
    await changeLanguage('zh');
    const session = createTerminalSession({ id: 'lang-refused', scrollbackCap: 100, getColumns: () => 100 });
    await session.run('lang fr');

    await changeLanguage('en');
    await session.run('help');

    expect(sentLanguages()).toEqual([undefined, undefined]);
  });

  it('completes in the language the tab was set to with lang', async () => {
    vi.mocked(completeConsoleLine).mockResolvedValue({ from: 0, items: [] });
    const session = createTerminalSession({ id: 'lang-complete', scrollbackCap: 100, getColumns: () => 100 });

    await session.complete('he', 2);
    expect(vi.mocked(completeConsoleLine).mock.lastCall?.[2]).toBeUndefined();

    await session.run('lang zh');
    await session.complete('he', 2);
    expect(vi.mocked(completeConsoleLine).mock.lastCall?.[2]).toBe('zh');
  });
});

describe('what the scrollback shows of a secret line', () => {
  function shown(session: ReturnType<typeof createTerminalSession>): string[] {
    return session.scrollback.value.map((block) => block.tokens.map((token) => token.text).join(''));
  }

  it('echoes a setting set line as its command words and a mask, not the key', async () => {
    const session = createTerminalSession({ id: 'echo-setting', scrollbackCap: 100, getColumns: () => 100 });
    await session.run('setting set oauth.github_client_secret abc123');

    expect(shown(session)).toContain('setting set ***');
    expect(shown(session).join('\n')).not.toContain('abc123');
    expect(vi.mocked(execConsoleLine).mock.calls[0]?.[0].line).toBe('setting set oauth.github_client_secret abc123');
  });

  it('stops the echo at the first flag and masks everything after it', async () => {
    const session = createTerminalSession({ id: 'echo-flag', scrollbackCap: 100, getColumns: () => 100 });
    await session.run('provider create --name OpenAI --api-key sk-abc');

    expect(shown(session)).toContain('provider create ***');
    expect(shown(session).join('\n')).not.toContain('sk-abc');
  });
});

describe('a command whose output is cut short', () => {
  it('ends with an error block when the stream closes before a done frame', async () => {
    vi.mocked(execConsoleLine).mockImplementationOnce(async () => {});
    const session = createTerminalSession({ id: 'stream-cut', scrollbackCap: 100, getColumns: () => 100 });

    const outcome = await session.run('help');

    expect(outcome).toEqual({ ok: false, code: 'stream', exit: false });
    const errors = session.scrollback.value.filter((block) => block.kind === 'error');
    expect(errors.map((block) => block.tokens.map((token) => token.text).join(''))).toEqual([
      t('streamInterrupted'),
    ]);
  });

  it('keeps the scrollback within its token budget and holds on to the newest output', async () => {
    // Each coloured character is a token of its own, so this is well over the budget.
    const chunk = '\x1b[31mx\x1b[0m'.repeat(60_000);
    vi.mocked(execConsoleLine).mockImplementationOnce(async (_request, handlers) => {
      handlers.onOut?.(chunk + '\x1b[32mlast\x1b[0m');
      handlers.onDone?.({ ok: true, code: '', exit: false, elapsed_ms: 1, json: false, lang: 'en' });
    });
    const session = createTerminalSession({ id: 'token-budget', scrollbackCap: 10_000, getColumns: () => 100 });

    await session.run('help');

    const retained = session.scrollback.value.reduce((sum, block) => sum + block.tokens.length, 0);
    expect(retained).toBeGreaterThan(40_000);
    expect(retained).toBeLessThanOrEqual(50_000 + 400);
    const newest = session.scrollback.value[session.scrollback.value.length - 1];
    expect(newest?.tokens.map((token) => token.text).join('')).toContain('last');
  });
});

describe('completion offsets', () => {
  // The server counts UTF-8 bytes, the draft is held in UTF-16 units. Each CJK
  // character is one unit and three bytes, so the two diverge as soon as one
  // precedes the caret.
  it('sends the caret as a byte offset and reads the answer back as a UTF-16 index', async () => {
    vi.mocked(completeConsoleLine).mockResolvedValue({ from: 13, items: [] });
    const session = createTerminalSession({ id: 'complete-cjk', scrollbackCap: 100, getColumns: () => 100 });
    const line = 'user list 张三';

    const result = await session.complete(line, line.length);

    expect(vi.mocked(completeConsoleLine).mock.calls[0]?.[1]).toBe(16);
    expect(result.from).toBe(11);
  });

  it('keeps ASCII offsets unchanged', async () => {
    vi.mocked(completeConsoleLine).mockResolvedValue({ from: 5, items: [{ value: 'list', label: 'list', hint: '' }] });
    const session = createTerminalSession({ id: 'complete-ascii', scrollbackCap: 100, getColumns: () => 100 });

    const result = await session.complete('user li', 7);

    expect(vi.mocked(completeConsoleLine).mock.calls[0]?.[1]).toBe(7);
    expect(result).toEqual({ from: 5, items: [{ value: 'list', label: 'list', hint: '' }] });
  });

  it('treats an offset past the end of the line as the end of the line', async () => {
    vi.mocked(completeConsoleLine).mockResolvedValue({ from: 99, items: [] });
    const session = createTerminalSession({ id: 'complete-past-end', scrollbackCap: 100, getColumns: () => 100 });

    const result = await session.complete('张三', 2);

    expect(result.from).toBe(2);
  });

  it('leaves the line alone when the completion request fails', async () => {
    vi.mocked(completeConsoleLine).mockRejectedValue(new Error('offline'));
    const session = createTerminalSession({ id: 'complete-offline', scrollbackCap: 100, getColumns: () => 100 });

    await expect(session.complete('user li', 7)).resolves.toEqual({ from: 7, items: [] });
  });
});
