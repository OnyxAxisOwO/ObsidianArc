// The Chinese dictionary is accepted and never answers. Boot must not wait on
// it forever: past the deadline the page is English, as it is when the chunk
// fails, and the stored choice is kept for the next boot.

import { afterEach, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.useRealTimers();
  vi.doUnmock('../src/i18n.zh');
  localStorage.clear();
  vi.restoreAllMocks();
  vi.resetModules();
});

it('finishes boot in English when the Chinese dictionary never answers', async () => {
  localStorage.setItem('obsidian-arc-language', 'zh');
  vi.resetModules();
  vi.doMock('../src/i18n.zh', () => new Promise(() => {}));
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  const { currentLanguage, primeLanguage, t } = await import('../src/composables/useI18n');

  vi.useFakeTimers();
  const boot = primeLanguage();
  await vi.advanceTimersByTimeAsync(10_000);

  await expect(boot).resolves.toBeUndefined();
  expect(t('languageLoadFailed')).toBe('Could not load that language. The page stays as it was.');
  expect(currentLanguage()).toBe('en');
  expect(localStorage.getItem('obsidian-arc-language')).toBe('zh');
});
