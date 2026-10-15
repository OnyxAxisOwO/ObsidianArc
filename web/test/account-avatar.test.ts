// An avatar is free text any account can set, and it is drawn for everyone who
// sees that account. A value a browser resolves to another site is a tracking
// pixel, so only same-site paths and inline images get through.

import { describe, expect, it } from 'vitest';
import { safeAvatar } from '../src/lib/account';

describe('safeAvatar', () => {
  it.each([
    ['/\\tracker.example/p.gif', 'a slash and a backslash'],
    ['/\\\\tracker.example/p.gif', 'a slash and two backslashes'],
    ['/\t/tracker.example/p.gif', 'a slash, a tab and a second slash'],
    ['/\n/tracker.example/p.gif', 'a slash, a newline and a second slash'],
    ['//tracker.example/p.gif', 'a protocol-relative URL'],
    ['https://tracker.example/p.gif', 'an absolute URL'],
  ])('refuses %s (%s)', (value) => {
    expect(safeAvatar(value)).toBeNull();
  });

  it('keeps a same-site path and an inline image unchanged', () => {
    expect(safeAvatar('/avatars/a.png')).toBe('/avatars/a.png');
    expect(safeAvatar('  /avatars/a.png  ')).toBe('/avatars/a.png');
    const inline = 'data:image/png;base64,iVBORw0KGgo=';
    expect(safeAvatar(inline)).toBe(inline);
  });

  it('refuses an empty value', () => {
    expect(safeAvatar('   ')).toBeNull();
  });
});
