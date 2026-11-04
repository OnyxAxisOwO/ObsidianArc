// The shared waits and stubs in helpers.ts, and the teardown in setup.ts that
// they lean on. These tests run in order: the second one is only meaningful
// after the first has left its stub behind.

import { describe, expect, it } from 'vitest';
import { stubLocation, until } from './helpers';

describe('a location stub left behind', () => {
  const real = window.location.href;

  it('is in place for the test that made it', () => {
    stubLocation();
    window.location.href = '/elsewhere';
    expect(window.location.href).toBe('/elsewhere');
  });

  it('is gone by the next test, without the first one restoring it', () => {
    expect(window.location.href).toBe(real);
    expect(typeof window.location.assign).toBe('function');
    expect(window.location.origin).not.toBe(undefined);
  });

  it('is gone after a replacement that never went through the helper', () => {
    Object.defineProperty(window, 'location', { configurable: true, writable: true, value: { href: '' } });
    expect(window.location.origin).toBe(undefined);
  });

  it('is gone after that one too', () => {
    expect(window.location.href).toBe(real);
  });
});

describe('until', () => {
  it('returns once the condition holds', async () => {
    let ready = false;
    setTimeout(() => { ready = true; }, 20);
    await until(() => ready, 'the flag');
    expect(ready).toBe(true);
  });

  it('says what it was waiting for when it gives up', async () => {
    await expect(until(() => false, 'a list that never loads', 50))
      .rejects.toThrow('timed out waiting for a list that never loads');
  });
});
