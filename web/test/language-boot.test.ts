// The Chinese dictionary is a chunk fetched during boot. When it does not
// arrive, boot must still finish — in English — and keep the stored choice so
// the next boot asks for the dictionary again.

import { afterEach, expect, it, vi } from 'vitest';

vi.mock('../src/i18n.zh', () => {
  throw new Error('chunk did not arrive');
});

afterEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
  vi.resetModules();
});

it('finishes boot in English when the Chinese dictionary cannot be fetched', async () => {
  localStorage.setItem('obsidian-arc-language', 'zh');
  vi.resetModules();
  vi.spyOn(console, 'warn').mockImplementation(() => {});
  // Imported after the stored choice is set: the language is read once, when the module loads.
  const { currentLanguage, primeLanguage, t } = await import('../src/composables/useI18n');

  await expect(primeLanguage()).resolves.toBeUndefined();
  expect(t('languageLoadFailed')).toBe('Could not load that language. The page stays as it was.');
  // Components that key a date locale or the API's lang off the language, not
  // off t(), must agree with the English the page shows.
  expect(currentLanguage()).toBe('en');
  expect(localStorage.getItem('obsidian-arc-language')).toBe('zh');
});
