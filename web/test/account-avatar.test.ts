// An avatar is free text any account can set, and it is drawn for everyone who
// sees that account. A value a browser resolves to another site is a tracking
// pixel, so only same-site paths, inline images and validated avatar services get through.

import { describe, expect, it } from 'vitest';
import { avatarSources, safeAvatar } from '../src/lib/account';

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


describe('supported avatar services', () => {
  const hash = 'a'.repeat(64);
  it.each(['https://gravatar.com', 'https://www.gravatar.com', 'https://0.gravatar.com', 'https://weavatar.com'])('accepts %s avatar hashes and safe parameters', (origin) => {
    const source = `${origin}/avatar/${hash}?s=160&d=404&r=g`;
    expect(safeAvatar(source)).toBe(source);
    const legacy = `${origin}/avatar/${'b'.repeat(32)}.png`;
    expect(safeAvatar(legacy)).toBe(legacy);
  });
  it.each([
    `https://gravatar.com.evil.example/avatar/${hash}`,
    `https://weavatar.com@evil.example/avatar/${hash}`,
    `https://evil.example/avatar/${hash}`,
    `http://weavatar.com/avatar/${hash}`,
    `https://weavatar.com/redirect/${hash}`,
    `https://weavatar.com/avatar/${hash}?d=https%3A%2F%2Fevil.example%2Fimage.png`,
    `https://weavatar.com/avatar/${hash}?s=99999`,
    `https://weavatar.com/avatar/${hash}?redirect=1`,
    `https://weavatar.com/avatar/not-a-hash`,
  ])('refuses an unsafe service URL: %s', (source) => {
    expect(safeAvatar(source)).toBeNull();
  });
});


describe('avatar fallback order', () => {
  it.each(['gravatar', 'weavatar'])('tries %s before the other provider', (provider) => {
    const source = `https://${provider}.com/avatar/${'a'.repeat(64)}?s=160&d=404&r=g`;
    const other = provider === 'gravatar' ? 'weavatar' : 'gravatar';
    expect(avatarSources(source)).toEqual([source, source.replace(`${provider}.com`, `${other}.com`)]);
  });
  it('keeps local custom avatars local and rejects unsafe sources', () => {
    expect(avatarSources('/custom.png')).toEqual(['/custom.png']);
    expect(avatarSources('https://tracker.example/pixel.png')).toEqual([]);
    expect(avatarSources('')).toEqual([]);
  });
});
