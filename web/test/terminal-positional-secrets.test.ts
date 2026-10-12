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
  ])('keeps %s out', (line) => {
    expect(isSensitiveTerminalLine(line)).toBe(true);
  });

  it.each([
    'user list --q passwd',
    'user passwdx alice',
    'user edit alice --nickname Bob',
  ])('still keeps ordinary lines in history: %s', (line) => {
    expect(isSensitiveTerminalLine(line)).toBe(false);
  });
});
