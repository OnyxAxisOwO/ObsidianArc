// A destination the server owns is navigated to, not handed to the router. The
// fragment case is the one the prefix test used to get wrong: the page is the
// same, so the router must not draw "no such page" for it.

import { describe, expect, it } from 'vitest';
import { serverOwned } from '@/lib/next';

describe('serverOwned', () => {
  it.each([
    '/oauth/authorize',
    '/oauth/authorize?client_id=x',
    '/oauth/authorize#state',
    '/oauth/authorize?client_id=x#state',
  ])('claims %s for the server', (path) => {
    expect(serverOwned(path)).toBe(true);
  });

  it.each([
    '/oauth/authorizeX',
    '/oauth/authorize/extra',
    '/settings',
    '/api/auth/me',
    '',
  ])('leaves %s to the router', (path) => {
    expect(serverOwned(path)).toBe(false);
  });
});
