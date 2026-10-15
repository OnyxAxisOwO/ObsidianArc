// The challenge script's loader. A script that could not be fetched used to be
// remembered for the page's lifetime, so one blip on the network left every
// later sign-in and feedback form without a challenge until a reload.

import { afterEach, describe, expect, it } from 'vitest';
import { loadTurnstile } from '../src/composables/useTurnstile';

const scripts = (): HTMLScriptElement[] =>
  [...document.head.querySelectorAll<HTMLScriptElement>('script[src*="challenges.cloudflare.com"]')];

afterEach(() => {
  // A script that loads is not removed by the loader, so tests leave them behind.
  for (const script of scripts()) script.remove();
});

describe('loadTurnstile after the script failed', () => {
  it('tries the script again on the next call instead of remembering the failure', async () => {
    const first = loadTurnstile();
    expect(scripts()).toHaveLength(1);
    scripts()[0]!.dispatchEvent(new Event('error'));
    await expect(first).resolves.toBeNull();

    const second = loadTurnstile();
    expect(scripts()).toHaveLength(1);
    expect(second).not.toBe(first);
    scripts()[0]!.dispatchEvent(new Event('error'));
    await expect(second).resolves.toBeNull();
  });
});

describe('loadTurnstile after the script ran without its API', () => {
  it('tries the script again on the next call when no API appeared', async () => {
    expect(window.turnstile).toBeUndefined();
    const first = loadTurnstile();
    expect(scripts()).toHaveLength(1);
    scripts()[0]!.dispatchEvent(new Event('load'));
    await expect(first).resolves.toBeNull();

    const second = loadTurnstile();
    expect(scripts()).toHaveLength(1);
    expect(second).not.toBe(first);
    scripts()[0]!.dispatchEvent(new Event('error'));
    await expect(second).resolves.toBeNull();
  });
});
