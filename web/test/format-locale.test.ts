import { afterEach, describe, expect, it } from 'vitest';
import { changeLanguage } from '../src/composables/useI18n';
import { absoluteTime, localeTag, relativeTime } from '../src/lib/format';

// Dates follow the interface's language, not the browser's: a reader who picks
// Chinese on an English browser must not get English dates in a Chinese sentence.
describe('dates in the interface language', () => {
  afterEach(async () => {
    await changeLanguage('en');
  });

  it('prints an absolute time in Chinese once the interface is Chinese', async () => {
    await changeLanguage('zh');
    const at = Date.UTC(2026, 9, 18, 7, 0, 0);
    expect(localeTag()).toBe('zh-CN');
    expect(absoluteTime(at)).toBe(new Date(at).toLocaleString('zh-CN'));
  });

  it('prints an absolute time in English for the English interface', async () => {
    await changeLanguage('en');
    const at = Date.UTC(2026, 9, 18, 7, 0, 0);
    expect(localeTag()).toBe('en-US');
    expect(absoluteTime(at)).toBe(new Date(at).toLocaleString('en-US'));
  });

  it('gives a date more than a month old in the interface language', async () => {
    await changeLanguage('zh');
    const old = Date.now() - 60 * 24 * 3600 * 1000;
    expect(relativeTime(old)).toBe(new Date(old).toLocaleDateString('zh-CN'));
  });
});
