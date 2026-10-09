// What the terminal's prompt remembers, and for whom.
//
// The history used to be one localStorage list for the whole browser, and it
// kept every line typed — `provider create --api-key sk-…`, `user passwd
// --password …` — until somebody cleared site data by hand. These tests hold
// the three things that fixed it: a credential is never kept, one account's
// lines are not another's, and signing out leaves nothing behind.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Account } from '../src/api/auth';
import type { ConsoleExecHandlers } from '../src/api/console';
import { adopt, forget } from '../src/stores/session';
import {
  TERMINAL_HISTORY_KEY_PREFIX,
  createTerminalSession,
  isSensitiveTerminalLine,
  terminalHistorySnapshot,
} from '../src/terminal/session';

vi.mock('../src/api/console', () => ({
  fetchConsoleSpec: vi.fn(),
  completeConsoleLine: vi.fn(),
  execConsoleLine: vi.fn(),
}));

import { execConsoleLine } from '../src/api/console';

function account(id: string): Account {
  return { id } as unknown as Account;
}

async function type(...lines: string[]): Promise<void> {
  const session = createTerminalSession({ id: 'history', scrollbackCap: 100, getColumns: () => 100 });
  for (const line of lines) await session.run(line);
}

function storedKeys(): string[] {
  const keys: string[] = [];
  for (let i = 0; i < localStorage.length; i += 1) keys.push(localStorage.key(i) ?? '');
  return keys.sort();
}

beforeEach(() => {
  localStorage.clear();
  vi.mocked(execConsoleLine).mockImplementation(async (_request, handlers: ConsoleExecHandlers) => {
    handlers.onDone?.({ ok: true, code: '', exit: false, elapsed_ms: 1, json: false, lang: 'en' });
  });
});

afterEach(() => {
  forget();
  localStorage.clear();
  vi.resetModules();
});

describe('which lines are kept out of the history', () => {
  it.each([
    'provider create --name OpenAI --kind openai --base-url https://api.openai.com --api-key sk-abc',
    'provider edit OpenAI --api-key=sk-abc',
    'provider edit OpenAI --headers "Authorization=Bearer abc"',
    'user create bob --password hunter22',
    'user passwd bob --password hunter22',
    'me passwd --current-password old --new-password new',
    'mail set --password hunter22',
    'usercheck set --api-key abc',
    'credit redeem --code ABCD-EFGH',
    'credit redeem --turnstile 0.abc',
    'code create --code SECRET1',
    'setting set oauth.github_client_secret abc',
    'setting set turnstile.secret_key abc',
    'setting set some.plugin_api_key abc',
    `setting import '{"site.name":"Arc"}' --yes`,
    '2fa enable 123456 --yes',
    '2fa disable 123456 --yes',
    '2fa recovery 123456 --yes',
    '2fa backoffice 123456',
    'me invite claim PARTNER1',
    'backup import {"conversations":[]} --yes',
    'login-bg set landscape_light iVBORw0KGgo',
    'logo set iVBORw0KGgo',
    'watch --count 3 -- 2fa backoffice 123456',
    '  USER PASSWD bob --PASSWORD hunter22',
  ])('keeps %s out', (line) => {
    expect(isSensitiveTerminalLine(line)).toBe(true);
  });

  it.each([
    'help',
    'whoami',
    'user list --q alice --limit 10',
    'setting list --q secret',
    'setting get oauth.github_client_secret',
    'setting set site.name Arc',
    'feedback list --status open',
    '2fa status',
    'me invite',
    'key create --name laptop',
    'echo password',
  ])('keeps %s', (line) => {
    expect(isSensitiveTerminalLine(line)).toBe(false);
  });
});

describe('the history a signed-in tab keeps', () => {
  it('never records a credential, in memory or in storage', async () => {
    adopt(account('u-ada'));
    await type('user list', 'user create bob --password hunter22', '2fa backoffice 123456', 'setting set site.name Arc');

    expect(terminalHistorySnapshot()).toEqual(['user list', 'setting set site.name Arc']);
    const stored = localStorage.getItem(`${TERMINAL_HISTORY_KEY_PREFIX}:u-ada`) ?? '';
    expect(JSON.parse(stored)).toEqual(['user list', 'setting set site.name Arc']);
    expect(stored).not.toContain('hunter22');
    expect(stored).not.toContain('123456');
  });

  it('is one list per account, even when the browser is shared without signing out', async () => {
    adopt(account('u-ada'));
    await type('ada-only-command');

    adopt(account('u-bob'));
    expect(terminalHistorySnapshot()).toEqual([]);
    await type('bob-only-command');
    expect(terminalHistorySnapshot()).toEqual(['bob-only-command']);

    adopt(account('u-ada'));
    expect(terminalHistorySnapshot()).toEqual(['ada-only-command']);
  });

  it('drops a credential that an earlier version already stored', async () => {
    localStorage.setItem(
      `${TERMINAL_HISTORY_KEY_PREFIX}:u-ada`,
      JSON.stringify(['user list', 'provider create --api-key sk-leaked', 'help']),
    );
    adopt(account('u-ada'));

    expect(terminalHistorySnapshot()).toEqual(['user list', 'help']);
  });

  it('sweeps the list every account used to share', async () => {
    localStorage.setItem(TERMINAL_HISTORY_KEY_PREFIX, JSON.stringify(['user passwd bob --password hunter22']));
    vi.resetModules();
    await import('../src/terminal/session');

    expect(localStorage.getItem(TERMINAL_HISTORY_KEY_PREFIX)).toBeNull();
  });
});

describe('signing out', () => {
  it('forgets every stored history and leaves the other stored things alone', async () => {
    localStorage.setItem(TERMINAL_HISTORY_KEY_PREFIX, '["legacy"]');
    localStorage.setItem(`${TERMINAL_HISTORY_KEY_PREFIX}:u-bob`, '["bob"]');
    localStorage.setItem('obsidian-arc-terminal-prefs', '{"fontSize":14}');
    adopt(account('u-ada'));
    await type('ada-command');
    expect(storedKeys()).toContain(`${TERMINAL_HISTORY_KEY_PREFIX}:u-ada`);

    forget();

    expect(storedKeys()).toEqual(['obsidian-arc-terminal-prefs']);
    expect(terminalHistorySnapshot()).toEqual([]);
  });

  it('leaves the next account to sign in an empty prompt', async () => {
    adopt(account('u-ada'));
    await type('ada-command');
    forget();

    adopt(account('u-bob'));
    expect(terminalHistorySnapshot()).toEqual([]);
  });

  it('survives a localStorage that throws', () => {
    adopt(account('u-ada'));
    vi.spyOn(Storage.prototype, 'key').mockImplementation(() => {
      throw new Error('blocked');
    });

    expect(() => forget()).not.toThrow();
    vi.restoreAllMocks();
  });
});
