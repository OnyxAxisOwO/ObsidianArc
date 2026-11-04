// Commands that take a password as a bare word, with no flag for the flag
// filter to catch. The console masks their tails on the server; the terminal
// must keep the same lines out of the history it persists to localStorage.

import { describe, expect, it } from 'vitest';
import { isSensitiveTerminalLine } from '../src/terminal/session';

describe('positional passwords are kept out of the history', () => {
  it.each([
    'user passwd alice hunter2',
    'user passwd bob Winter2026!',
    '  USER   PASSWD alice hunter2',
    'user passwd alice',
    'watch user passwd alice hunter2',
    'watch --count 2 -- user passwd alice hunter2',
    // The second positional of `user create` is the password; the console declares only the name.
    'user create bob hunter22',
    'me passwd oldpass newpass',
    // Quotes do not change what the console reads, so they must not change this either.
    "user create bob '--password' hunter22",
    "'user passwd' bob hunter22",
    'user create bob "--password=hunter22"',
    // A quoted dash-led password: stripping the quotes makes it a flag-looking token, so only the raw line matches.
    'user create bob "-hunter2"',
    "user create bob '-hunter2'",
    // A quoted command word: only the stripped line matches.
    '"user" create bob hunter2',
  ])('keeps %s out', (line) => {
    expect(isSensitiveTerminalLine(line)).toBe(true);
  });

  it.each([
    'user list --q passwd',
    'user passwdx alice',
    'user edit alice --nickname Bob',
    'user create bob',
    'user create bob --email bob@example.com',
    // Watching an ordinary command is not a secret, whatever it repeats.
    'watch usage rpm',
    'watch --interval 10s --count 6 health status',
  ])('still keeps ordinary lines in history: %s', (line) => {
    expect(isSensitiveTerminalLine(line)).toBe(false);
  });
});
