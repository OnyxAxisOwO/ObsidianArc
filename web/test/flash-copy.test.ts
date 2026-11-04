// The copy result a button shows, and the expiry a days field means. Both were
// written out by hand on each screen that needed them.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { changeLanguage, t } from '../src/composables/useI18n';
import { FEEDBACK_FLASH_MS, flashCopy } from '../src/lib/flash';
import { DAY_MS, expiresAtFromDays } from '../src/lib/time';

beforeEach(async () => {
  await changeLanguage('en');
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('flashCopy', () => {
  it('says whether the copy landed, then puts the label back', async () => {
    const labels: string[] = [];
    flashCopy(Promise.resolve(false), (text) => labels.push(text));
    await vi.advanceTimersByTimeAsync(0);
    expect(labels).toEqual([t('copyFailed')]);

    await vi.advanceTimersByTimeAsync(FEEDBACK_FLASH_MS);
    expect(labels).toEqual([t('copyFailed'), '']);
  });

  it('writes nothing once cancelled, so a newer copy or an unmount wins', async () => {
    const labels: string[] = [];
    let resolve!: (ok: boolean) => void;
    const cancel = flashCopy(new Promise<boolean>((done) => { resolve = done; }), (text) => labels.push(text));
    cancel();
    resolve(true);
    await vi.advanceTimersByTimeAsync(FEEDBACK_FLASH_MS);
    expect(labels).toEqual([]);
  });

  it('keeps a newer result on screen when the older one is cancelled', async () => {
    const labels: string[] = [];
    const first = flashCopy(Promise.resolve(true), (text) => labels.push(`a:${text}`));
    await vi.advanceTimersByTimeAsync(FEEDBACK_FLASH_MS / 2);
    first();
    flashCopy(Promise.resolve(true), (text) => labels.push(`b:${text}`));
    await vi.advanceTimersByTimeAsync(FEEDBACK_FLASH_MS / 2 + 1);
    // The first one's clear never ran: only the second result is showing.
    expect(labels).toEqual([`a:${t('copied')}`, `b:${t('copied')}`]);
  });
});

describe('expiresAtFromDays', () => {
  it('counts whole 24-hour days from now, and reads blank or zero as never', () => {
    vi.setSystemTime(1_000_000);
    expect(expiresAtFromDays(3)).toBe(1_000_000 + 3 * DAY_MS);
    expect(expiresAtFromDays(0)).toBe(0);
    expect(expiresAtFromDays(null)).toBe(0);
    expect(expiresAtFromDays(undefined)).toBe(0);
    expect(expiresAtFromDays(-1)).toBe(0);
  });
});
